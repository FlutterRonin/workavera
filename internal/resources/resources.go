// Package resources owns the per-user resource library: uploaded source files,
// their converted Markdown, and the short-lived download leases that can hand
// a source file to an external service.
package resources

import (
	"errors"
	"io"
	"net/http"
	"net/url"
	"path"
	"strings"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/apis"
	"github.com/pocketbase/pocketbase/core"
)

const (
	CollectionName       = "user_resources"
	LeasesCollectionName = "resource_download_leases"

	// MaxFileSize is the strict upper bound of a source file, in bytes.
	MaxFileSize = 6 * 1024 * 1024
	// MaxMarkdownSize is the upper bound of the converted Markdown, in bytes.
	MaxMarkdownSize = 8 * 1024 * 1024
	// MaxNameLength bounds original and display names, in characters.
	MaxNameLength = 255
)

// Stable error codes returned by the HTTP API and the Assistant tools.
const (
	CodeFileRequired             = "file_required"
	CodeFileEmpty                = "file_empty"
	CodeFileTooLarge             = "file_too_large"
	CodeUnsupportedMediaType     = "unsupported_media_type"
	CodeUnsupportedEncoding      = "unsupported_encoding"
	CodeInvalidFilename          = "invalid_filename"
	CodeConversionFailed         = "conversion_failed"
	CodeConversionTimeout        = "conversion_timeout"
	CodeDocumentEncrypted        = "document_encrypted"
	CodeDocumentMalformed        = "document_malformed"
	CodeDocumentNeedsOCR         = "document_needs_ocr"
	CodeConvertedContentEmpty    = "converted_content_empty"
	CodeConvertedContentTooLarge = "converted_content_too_large"
	CodeResourceNotFound         = "resource_not_found"
	CodeInvalidResourceCursor    = "invalid_resource_cursor"
	CodeDownloadLeaseExpired     = "download_lease_expired"
	CodeInvalidInput             = "invalid_input"
	CodeDownloadLinksUnavailable = "download_links_unavailable"
	CodeResourceHasNoText        = "resource_has_no_text"
)

// converterStoreKey keeps the registered converter in the app store, so
// server-side uploads share its warm-up and concurrency limit.
const converterStoreKey = "resources.converter"

// Error is a failure with a stable code and a description that is safe to
// show to users and models. The wrapped cause, which may carry parser output
// or paths, is only ever logged.
type Error struct {
	Code    string
	Message string
	Status  int
	cause   error
}

func (e *Error) Error() string { return e.Code + ": " + e.Message }

func (e *Error) Unwrap() error { return e.cause }

func newError(code string, status int, message string) *Error {
	return &Error{Code: code, Status: status, Message: message}
}

func wrapError(code string, status int, message string, cause error) *Error {
	return &Error{Code: code, Status: status, Message: message, cause: cause}
}

var errNotFound = newError(CodeResourceNotFound, http.StatusNotFound, "Resource not found. It may have been deleted.")

// IsNotFound reports whether err means the resource does not exist for the
// caller, which deliberately covers resources owned by someone else.
func IsNotFound(err error) bool {
	var resourceErr *Error
	return errors.As(err, &resourceErr) && resourceErr.Code == CodeResourceNotFound
}

type service struct {
	app       core.App
	converter *converter
}

func Register(app core.App) {
	register(app, &service{app: app, converter: newConverter(app)})
}

func register(app core.App, s *service) {
	app.Store().Set(converterStoreKey, s.converter)

	app.OnServe().BindFunc(func(event *core.ServeEvent) error {
		s.converter.warmInBackground()

		group := event.Router.Group("/api").Bind(apis.RequireAuth("users"))
		// The multipart envelope adds a little to the file itself; the size
		// limit that matters is enforced again on the bytes actually read.
		group.POST("/resources", s.uploadResource).Bind(apis.BodyLimit(MaxFileSize + 1024*1024))
		group.DELETE("/resources/{id}", s.deleteResource)
		group.POST("/resources/{id}/download-leases", s.createLease)
		group.DELETE("/resource-download-leases/{id}", s.revokeLease)

		// Download URLs carry a bearer token in the path, so the request
		// logger must never record them. The handler never returns an error,
		// which keeps failed requests out of the log as well.
		event.Router.GET("/api/resource-downloads/{token}/{filename}", s.download).Bind(apis.SkipSuccessActivityLog())
		return event.Next()
	})

	app.OnRecordUpdateRequest(CollectionName).BindFunc(guardResourceUpdate)

	app.OnRecordEnrich(CollectionName).BindFunc(func(event *core.RecordEnrichEvent) error {
		event.Record.WithCustomData(true)
		event.Record.Set("url", fileURL(event.Record))
		return event.Next()
	})

	if err := app.Cron().Add("resourceDownloadLeasesCleanup", "23 * * * *", func() {
		if err := deleteExpiredLeases(app); err != nil {
			app.Logger().Warn("resource download lease cleanup failed", "error", err)
		}
	}); err != nil {
		app.Logger().Error("could not schedule resource download lease cleanup", "error", err)
	}
}

// immutableFields are fixed at upload; only display_name is editable.
var immutableFields = []string{"owner", "original_name", "media_type", "size", "sha256", "converted_markdown", "converted_size"}

func guardResourceUpdate(event *core.RecordRequestEvent) error {
	original := event.Record.Original()
	for _, field := range immutableFields {
		if event.Record.GetString(field) != original.GetString(field) {
			return event.BadRequestError("Only the display name of a resource can be changed.", nil)
		}
	}
	if len(event.Record.GetUnsavedFiles("file")) > 0 || event.Record.GetString("file") != original.GetString("file") {
		return event.BadRequestError("Only the display name of a resource can be changed.", nil)
	}
	name := sanitizeName(event.Record.GetString("display_name"))
	if name == "" || len([]rune(name)) > MaxNameLength {
		return event.BadRequestError("The display name must be 1 to 255 characters.", nil)
	}
	event.Record.Set("display_name", name)
	return event.Next()
}

// fileURL is the authenticated URL of the protected source file. It needs a
// PocketBase file token and is never usable as a public link.
func fileURL(record *core.Record) string {
	return "/api/files/" + url.PathEscape(CollectionName) + "/" + url.PathEscape(record.Id) + "/" + url.PathEscape(record.GetString("file"))
}

// Resource is the API projection of a user_resources record. It mirrors the
// record's own field names so the frontend handles upload responses and
// Records API responses alike, and never carries the Markdown or the hash.
type Resource struct {
	ID            string `json:"id"`
	DisplayName   string `json:"display_name"`
	OriginalName  string `json:"original_name"`
	MediaType     string `json:"media_type"`
	Size          int64  `json:"size"`
	ConvertedSize int64  `json:"converted_size"`
	File          string `json:"file"`
	URL           string `json:"url"`
	Created       string `json:"created"`
	Updated       string `json:"updated"`
}

func resourceFromRecord(record *core.Record) Resource {
	return Resource{
		ID:            record.Id,
		DisplayName:   record.GetString("display_name"),
		OriginalName:  record.GetString("original_name"),
		MediaType:     record.GetString("media_type"),
		Size:          int64(record.GetInt("size")),
		ConvertedSize: int64(record.GetInt("converted_size")),
		File:          record.GetString("file"),
		URL:           fileURL(record),
		Created:       record.GetDateTime("created").String(),
		Updated:       record.GetDateTime("updated").String(),
	}
}

func findOwnedResource(app core.App, ownerID, id string) (*core.Record, error) {
	id = strings.TrimSpace(id)
	if ownerID == "" || id == "" {
		return nil, errNotFound
	}
	record, err := app.FindFirstRecordByFilter(CollectionName, "id = {:id} && owner = {:owner}", dbx.Params{"id": id, "owner": ownerID})
	if err != nil {
		return nil, errNotFound
	}
	return record, nil
}

// Rename changes the display name of one of the owner's resources, under the
// same rules as renaming it on the Resources page.
func Rename(app core.App, ownerID, id, name string) (Resource, error) {
	record, err := findOwnedResource(app, ownerID, id)
	if err != nil {
		return Resource{}, err
	}
	name = sanitizeName(name)
	if name == "" || len([]rune(name)) > MaxNameLength {
		return Resource{}, newError(CodeInvalidInput, http.StatusBadRequest, "The display name must be 1 to 255 characters.")
	}
	if record.GetString("display_name") != name {
		record.Set("display_name", name)
		if err := app.Save(record); err != nil {
			return Resource{}, err
		}
	}
	return resourceFromRecord(record), nil
}

// Delete removes one of the owner's resources together with its source file
// and, through the cascading relation, every download lease pointing to it.
func Delete(app core.App, ownerID, id string) error {
	record, err := findOwnedResource(app, ownerID, id)
	if err != nil {
		return err
	}
	return app.Delete(record)
}

func (s *service) uploadResource(event *core.RequestEvent) error {
	name, data, err := readUploadedFile(event)
	if err != nil {
		return writeError(event, err)
	}
	resource, err := Upload(event.Request.Context(), s.app, s.converter, event.Auth.Id, name, data)
	if err != nil {
		return writeError(event, err)
	}
	return event.JSON(http.StatusOK, resource)
}

func (s *service) deleteResource(event *core.RequestEvent) error {
	if err := Delete(s.app, event.Auth.Id, event.Request.PathValue("id")); err != nil {
		return writeError(event, err)
	}
	return event.NoContent(http.StatusNoContent)
}

type createLeaseRequest struct {
	Purpose string `json:"purpose"`
}

func (s *service) createLease(event *core.RequestEvent) error {
	var request createLeaseRequest
	if err := event.BindBody(&request); err != nil {
		return writeError(event, newError(CodeInvalidInput, http.StatusBadRequest, "Invalid request body."))
	}
	lease, err := CreateLease(s.app, event.Auth.Id, event.Request.PathValue("id"), request.Purpose, "api")
	if err != nil {
		return writeError(event, err)
	}
	return event.JSON(http.StatusOK, lease)
}

func (s *service) revokeLease(event *core.RequestEvent) error {
	if err := RevokeLease(s.app, event.Auth.Id, event.Request.PathValue("id")); err != nil {
		return writeError(event, err)
	}
	return event.NoContent(http.StatusNoContent)
}

// writeError responds with the stable code and safe message of a resource
// error. Anything else is logged and reported generically, so parser output,
// stacks, and paths never reach the client.
func writeError(event *core.RequestEvent, err error) error {
	var resourceErr *Error
	if !errors.As(err, &resourceErr) {
		event.App.Logger().Error("resource request failed", "error", err)
		resourceErr = newError("internal_error", http.StatusInternalServerError, "The resource request failed.")
	} else if resourceErr.cause != nil {
		event.App.Logger().Warn("resource request rejected", "code", resourceErr.Code, "error", resourceErr.cause)
	}
	return event.JSON(resourceErr.Status, errorBody{Status: resourceErr.Status, Code: resourceErr.Code, Message: resourceErr.Message, Data: map[string]any{}})
}

// errorBody is a PocketBase-shaped error response with its stable code. It is
// a struct so equal errors serialize to identical bytes.
type errorBody struct {
	Status  int            `json:"status"`
	Code    string         `json:"code"`
	Message string         `json:"message"`
	Data    map[string]any `json:"data"`
}

// SourceFile is a resource's original bytes together with its stored
// Markdown, which is empty for binary resources.
type SourceFile struct {
	ID          string
	DisplayName string
	// FileName is the display name, carrying the original file's extension
	// when the display name has lost it, so tools still recognize the file.
	FileName  string
	MediaType string
	Data      []byte
	Markdown  string
}

// Source reads one of the owner's resources for server-side use, such as
// copying it into the owner's sandbox. Only the owner's own resources resolve.
func Source(app core.App, ownerID, id string) (SourceFile, error) {
	record, err := findOwnedResource(app, ownerID, id)
	if err != nil {
		return SourceFile{}, err
	}
	fsys, err := app.NewFilesystem()
	if err != nil {
		return SourceFile{}, err
	}
	defer fsys.Close()
	reader, err := fsys.GetReader(record.BaseFilesPath() + "/" + record.GetString("file"))
	if err != nil {
		return SourceFile{}, err
	}
	defer reader.Close()
	data, err := io.ReadAll(io.LimitReader(reader, MaxFileSize+1))
	if err != nil {
		return SourceFile{}, err
	}

	displayName := record.GetString("display_name")
	fileName := displayName
	if ext := path.Ext(record.GetString("original_name")); !strings.EqualFold(path.Ext(displayName), ext) {
		fileName += ext
	}
	return SourceFile{
		ID:          record.Id,
		DisplayName: displayName,
		FileName:    fileName,
		MediaType:   record.GetString("media_type"),
		Data:        data,
		Markdown:    record.GetString("converted_markdown"),
	}, nil
}
