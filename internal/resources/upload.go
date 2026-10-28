package resources

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"path"
	"strings"
	"unicode"

	"github.com/gabriel-vasile/mimetype"
	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/filesystem"
)

// conversionPath is how an accepted format becomes Markdown.
type conversionPath int

const (
	pathText conversionPath = iota
	pathAnydoc
	pathHTML
)

// format describes one accepted extension: the media type stored for it, the
// detected media types that may sit behind it, and its conversion path. The
// extension is only a hint; the detected type must agree with it.
type format struct {
	mediaType string
	detected  []string
	path      conversionPath
}

const (
	mimeDocx = "application/vnd.openxmlformats-officedocument.wordprocessingml.document"
	mimePptx = "application/vnd.openxmlformats-officedocument.presentationml.presentation"
	mimeXlsx = "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"
	mimeOLE  = "application/x-ole-storage"
	mimeText = "text/plain"
)

var formats = map[string]format{
	"txt":   {mediaType: "text/plain", detected: []string{mimeText}, path: pathText},
	"md":    {mediaType: "text/markdown", detected: []string{mimeText}, path: pathText},
	"json":  {mediaType: "application/json", detected: []string{mimeText}, path: pathText},
	"jsonl": {mediaType: "application/x-ndjson", detected: []string{mimeText}, path: pathText},

	"pdf":  {mediaType: "application/pdf", detected: []string{"application/pdf"}, path: pathAnydoc},
	"docx": {mediaType: mimeDocx, detected: []string{mimeDocx}, path: pathAnydoc},
	"docm": {mediaType: "application/vnd.ms-word.document.macroEnabled.12", detected: []string{mimeDocx}, path: pathAnydoc},
	"doc":  {mediaType: "application/msword", detected: []string{"application/msword", mimeOLE}, path: pathAnydoc},
	"pptx": {mediaType: mimePptx, detected: []string{mimePptx}, path: pathAnydoc},
	"pptm": {mediaType: "application/vnd.ms-powerpoint.presentation.macroEnabled.12", detected: []string{mimePptx}, path: pathAnydoc},
	"ppsx": {mediaType: "application/vnd.openxmlformats-officedocument.presentationml.slideshow", detected: []string{mimePptx}, path: pathAnydoc},
	"ppsm": {mediaType: "application/vnd.ms-powerpoint.slideshow.macroEnabled.12", detected: []string{mimePptx}, path: pathAnydoc},
	"ppt":  {mediaType: "application/vnd.ms-powerpoint", detected: []string{"application/vnd.ms-powerpoint", mimeOLE}, path: pathAnydoc},
	"pps":  {mediaType: "application/vnd.ms-powerpoint", detected: []string{"application/vnd.ms-powerpoint", mimeOLE}, path: pathAnydoc},
	"pot":  {mediaType: "application/vnd.ms-powerpoint", detected: []string{"application/vnd.ms-powerpoint", mimeOLE}, path: pathAnydoc},
	"xlsx": {mediaType: mimeXlsx, detected: []string{mimeXlsx}, path: pathAnydoc},
	"xlsm": {mediaType: "application/vnd.ms-excel.sheet.macroEnabled.12", detected: []string{mimeXlsx}, path: pathAnydoc},
	"xlsb": {mediaType: "application/vnd.ms-excel.sheet.binary.macroEnabled.12", detected: []string{mimeXlsx}, path: pathAnydoc},
	"xls":  {mediaType: "application/vnd.ms-excel", detected: []string{"application/vnd.ms-excel", mimeOLE}, path: pathAnydoc},
	"odt":  {mediaType: "application/vnd.oasis.opendocument.text", detected: []string{"application/vnd.oasis.opendocument.text"}, path: pathAnydoc},
	"ods":  {mediaType: "application/vnd.oasis.opendocument.spreadsheet", detected: []string{"application/vnd.oasis.opendocument.spreadsheet"}, path: pathAnydoc},
	"odp":  {mediaType: "application/vnd.oasis.opendocument.presentation", detected: []string{"application/vnd.oasis.opendocument.presentation"}, path: pathAnydoc},
	"rtf":  {mediaType: "application/rtf", detected: []string{"text/rtf"}, path: pathAnydoc},
	"epub": {mediaType: "application/epub+zip", detected: []string{"application/epub+zip"}, path: pathAnydoc},
	// CSV has no signature, so plain text is the honest detection result for
	// most files; non-UTF-8 ones are decoded before conversion.
	"csv": {mediaType: "text/csv", detected: []string{"text/csv", mimeText}, path: pathAnydoc},

	"html": {mediaType: "text/html", detected: []string{"text/html", "application/xhtml+xml", mimeText}, path: pathHTML},
	"htm":  {mediaType: "text/html", detected: []string{"text/html", "application/xhtml+xml", mimeText}, path: pathHTML},
}

// readUploadedFile returns the single multipart "file" of the request and its
// bytes. The declared size is checked before reading, and the bytes actually
// read are checked again, since the multipart header is client input.
func readUploadedFile(event *core.RequestEvent) (string, []byte, error) {
	files, err := event.FindUploadedFiles("file")
	if err != nil || len(files) == 0 {
		return "", nil, newError(CodeFileRequired, http.StatusBadRequest, "Choose a file to upload.")
	}
	if len(files) > 1 {
		return "", nil, newError(CodeInvalidInput, http.StatusBadRequest, "Upload one file at a time.")
	}
	file := files[0]
	if err := checkSize(file.Size); err != nil {
		return "", nil, err
	}
	reader, err := file.Reader.Open()
	if err != nil {
		return "", nil, err
	}
	defer reader.Close()
	data, err := io.ReadAll(io.LimitReader(reader, MaxFileSize+1))
	if err != nil {
		return "", nil, err
	}
	return file.OriginalName, data, nil
}

func checkSize(size int64) error {
	if size <= 0 {
		return newError(CodeFileEmpty, http.StatusBadRequest, "The file is empty.")
	}
	if size > MaxFileSize {
		return newError(CodeFileTooLarge, http.StatusRequestEntityTooLarge, "Files must be 6 MB or smaller.")
	}
	return nil
}

// Upload stores a new resource for the owner, converting it to Markdown in
// the same request. Nothing is written unless conversion succeeds, so a failed
// upload leaves neither a record nor a file behind. Uploading the same content
// under the same original name returns the existing resource.
func Upload(ctx context.Context, app core.App, conv *converter, ownerID, name string, data []byte) (Resource, error) {
	if err := checkSize(int64(len(data))); err != nil {
		return Resource{}, err
	}
	originalName := sanitizeName(name)
	if originalName == "" || len([]rune(originalName)) > MaxNameLength || originalName == "." || originalName == ".." {
		return Resource{}, newError(CodeInvalidFilename, http.StatusBadRequest, "The file name is missing or invalid.")
	}
	ext := strings.ToLower(strings.TrimPrefix(path.Ext(originalName), "."))
	spec, ok := formats[ext]
	if !ok || !detectedMatches(mimetype.Detect(data), spec.detected) {
		return Resource{}, newError(CodeUnsupportedMediaType, http.StatusUnsupportedMediaType, "This file type is not supported.")
	}

	digest := sha256.Sum256(data)
	contentHash := hex.EncodeToString(digest[:])
	if existing, err := findExisting(app, ownerID, contentHash, originalName); err != nil {
		return Resource{}, err
	} else if existing != nil {
		return resourceFromRecord(existing), nil
	}

	markdown, err := conv.toMarkdown(ctx, spec.path, ext, data)
	if err != nil {
		return Resource{}, err
	}

	collection, err := app.FindCollectionByNameOrId(CollectionName)
	if err != nil {
		return Resource{}, err
	}
	file, err := filesystem.NewFileFromBytes(data, originalName)
	if err != nil {
		return Resource{}, err
	}
	record := core.NewRecord(collection)
	record.Set("owner", ownerID)
	record.Set("file", file)
	record.Set("original_name", originalName)
	record.Set("display_name", originalName)
	record.Set("media_type", spec.mediaType)
	record.Set("size", len(data))
	record.Set("sha256", contentHash)
	record.Set("converted_markdown", markdown)
	record.Set("converted_size", len(markdown))
	if err := app.Save(record); err != nil {
		// A concurrent upload of the same file may have won the unique index
		// between the lookup above and this save; reuse its record.
		if existing, findErr := findExisting(app, ownerID, contentHash, originalName); findErr == nil && existing != nil {
			return resourceFromRecord(existing), nil
		}
		return Resource{}, err
	}
	return resourceFromRecord(record), nil
}

func findExisting(app core.App, ownerID, contentHash, originalName string) (*core.Record, error) {
	records, err := app.FindRecordsByFilter(
		CollectionName,
		"owner = {:owner} && sha256 = {:sha256} && original_name = {:name}",
		"",
		1,
		0,
		dbx.Params{"owner": ownerID, "sha256": contentHash, "name": originalName},
	)
	if err != nil || len(records) == 0 {
		return nil, err
	}
	return records[0], nil
}

// detectedMatches reports whether the detected media type, or one of its
// ancestors, is among the accepted ones. Ancestors matter for text: a Markdown
// file that opens with an HTML tag is detected as HTML, whose parent is plain
// text.
func detectedMatches(detected *mimetype.MIME, accepted []string) bool {
	for current := detected; current != nil; current = current.Parent() {
		for _, mediaType := range accepted {
			if current.Is(mediaType) {
				return true
			}
		}
	}
	return false
}

// sanitizeName keeps only the last path component of a client-supplied name
// and drops control and formatting characters (including bidirectional
// overrides that can disguise an extension) and quotes, which would otherwise
// reach response headers.
func sanitizeName(name string) string {
	if index := strings.LastIndexAny(name, `/\`); index >= 0 {
		name = name[index+1:]
	}
	name = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) || r == '"' || r == unicode.ReplacementChar {
			return -1
		}
		return r
	}, name)
	return strings.TrimSpace(name)
}
