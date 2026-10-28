package resources

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/types"
)

const (
	// LeaseLifetime is how long a download URL works. It bounds the exposure
	// of a credential that is persisted with the chat history, so it must not
	// be relaxed.
	LeaseLifetime     = 5 * time.Minute
	maxPurposeChars   = 200
	minPurposeChars   = 10
	leaseTokenBytes   = 32
	leaseCleanupBatch = 500
)

// vaguePurposes do not tell the user who receives the file or why, which is
// the only basis they have for approving a link.
var vaguePurposes = map[string]bool{
	"user requested":    true,
	"user request":      true,
	"requested by user": true,
	"user asked":        true,
	"download":          true,
	"download link":     true,
	"for download":      true,
	"external use":      true,
	"external service":  true,
}

// LeaseTarget is what a lease will hand out, resolved before asking the user
// to approve it.
type LeaseTarget struct {
	ResourceID string
	Name       string
	Purpose    string
}

// LeaseResult is returned once, when a lease is created. It is the only
// place the plaintext token ever appears.
type LeaseResult struct {
	Resource  ResourceRef `json:"resource"`
	LeaseID   string      `json:"leaseId"`
	URL       string      `json:"url"`
	ExpiresAt string      `json:"expiresAt"`
}

// PrepareLease validates a lease request without creating anything, so an
// approval card is only shown for a link that can actually be created.
func PrepareLease(app core.App, ownerID, resourceID, purpose string) (LeaseTarget, error) {
	purpose = strings.Join(strings.Fields(purpose), " ")
	if utf8.RuneCountInString(purpose) < minPurposeChars || utf8.RuneCountInString(purpose) > maxPurposeChars || vaguePurposes[strings.ToLower(strings.TrimRight(purpose, "."))] {
		return LeaseTarget{}, newError(CodeInvalidInput, http.StatusBadRequest, "The purpose must describe the concrete use and recipient of the file in 10 to 200 characters.")
	}
	if _, err := downloadBaseURL(app); err != nil {
		return LeaseTarget{}, err
	}
	record, err := findOwnedResource(app, ownerID, resourceID)
	if err != nil {
		return LeaseTarget{}, err
	}
	return LeaseTarget{ResourceID: record.Id, Name: record.GetString("display_name"), Purpose: purpose}, nil
}

// CreateLease issues a new download credential for one of the owner's
// resources. Earlier leases for the same resource are left alone, and the
// resource record itself is never written.
func CreateLease(app core.App, ownerID, resourceID, purpose, caller string) (LeaseResult, error) {
	target, err := PrepareLease(app, ownerID, resourceID, purpose)
	if err != nil {
		return LeaseResult{}, err
	}
	record, err := findOwnedResource(app, ownerID, target.ResourceID)
	if err != nil {
		return LeaseResult{}, err
	}
	baseURL, err := downloadBaseURL(app)
	if err != nil {
		return LeaseResult{}, err
	}

	secret := make([]byte, leaseTokenBytes)
	if _, err := rand.Read(secret); err != nil {
		return LeaseResult{}, err
	}
	token := base64.RawURLEncoding.EncodeToString(secret)
	collection, err := app.FindCollectionByNameOrId(LeasesCollectionName)
	if err != nil {
		return LeaseResult{}, err
	}
	expiresAt := time.Now().UTC().Add(LeaseLifetime)
	lease := core.NewRecord(collection)
	lease.Set("owner", ownerID)
	lease.Set("resource", record.Id)
	lease.Set("token_hash", hashToken(token))
	lease.Set("purpose", target.Purpose)
	lease.Set("expires_at", expiresAt)
	if err := app.Save(lease); err != nil {
		return LeaseResult{}, err
	}
	app.Logger().Info("resource download lease created", "resourceId", record.Id, "owner", ownerID, "leaseId", lease.Id, "caller", caller)

	return LeaseResult{
		Resource:  ResourceRef{ID: record.Id, Name: target.Name},
		LeaseID:   lease.Id,
		URL:       baseURL + "/api/resource-downloads/" + token + "/" + url.PathEscape(record.GetString("original_name")),
		ExpiresAt: expiresAt.Format(time.RFC3339),
	}, nil
}

// RevokeLease deletes one of the owner's leases. Revoking an unknown,
// expired, foreign, or already revoked lease also succeeds, so the answer
// never reveals whether the lease existed.
func RevokeLease(app core.App, ownerID, leaseID string) error {
	leaseID = strings.TrimSpace(leaseID)
	if ownerID == "" || leaseID == "" {
		return nil
	}
	lease, err := app.FindFirstRecordByFilter(LeasesCollectionName, "id = {:id} && owner = {:owner}", dbx.Params{"id": leaseID, "owner": ownerID})
	if err != nil {
		return nil
	}
	if err := app.Delete(lease); err != nil {
		return err
	}
	app.Logger().Info("resource download lease revoked", "resourceId", lease.GetString("resource"), "owner", ownerID, "leaseId", lease.Id)
	return nil
}

// downloadBaseURL is the public origin download URLs are built on. A lease is
// a bearer credential, so it is only issued for HTTPS deployments; loopback
// hosts are allowed for local development.
func downloadBaseURL(app core.App) (string, error) {
	unavailable := newError(CodeDownloadLinksUnavailable, http.StatusConflict, "Download links need an HTTPS Application URL in the server settings.")
	parsed, err := url.Parse(strings.TrimSpace(app.Settings().Meta.AppURL))
	if err != nil || parsed.Host == "" {
		return "", unavailable
	}
	if parsed.Scheme != "https" && !(parsed.Scheme == "http" && isLoopbackHost(parsed.Hostname())) {
		return "", unavailable
	}
	return strings.TrimRight(parsed.Scheme+"://"+parsed.Host+parsed.Path, "/"), nil
}

func isLoopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func hashToken(token string) string {
	digest := sha256.Sum256([]byte(token))
	return hex.EncodeToString(digest[:])
}

// resolveLease returns the resource a token currently grants. A lease is
// valid while its record exists and has not expired; there is nothing to
// decrement, so resolving never writes.
func resolveLease(app core.App, token string) (*core.Record, *core.Record, bool) {
	if raw, err := base64.RawURLEncoding.DecodeString(token); err != nil || len(raw) != leaseTokenBytes {
		return nil, nil, false
	}
	lease, err := app.FindFirstRecordByFilter(LeasesCollectionName, "token_hash = {:hash}", dbx.Params{"hash": hashToken(token)})
	if err != nil || !lease.GetDateTime("expires_at").Time().After(time.Now()) {
		return nil, nil, false
	}
	resource, err := app.FindRecordById(CollectionName, lease.GetString("resource"))
	if err != nil || resource.GetString("owner") != lease.GetString("owner") {
		return nil, nil, false
	}
	return lease, resource, true
}

// download serves a source file by lease token without a Workavera session.
// Invalid, expired, and revoked tokens get the same answer, and the response
// never redirects to, or reveals, the protected file URL. It always returns
// nil so the request logger has no failed request to record the token from.
func (s *service) download(event *core.RequestEvent) error {
	header := event.Response.Header()
	header.Set("Cache-Control", "private, no-store")
	header.Set("X-Content-Type-Options", "nosniff")
	header.Set("Referrer-Policy", "no-referrer")

	lease, resource, ok := resolveLease(s.app, event.Request.PathValue("token"))
	if !ok {
		s.app.Logger().Info("resource download rejected", "result", "not_found", "ip", event.RealIP())
		return writeDownloadNotFound(event)
	}
	fsys, err := s.app.NewFilesystem()
	if err != nil {
		s.app.Logger().Error("resource download failed", "resourceId", resource.Id, "owner", resource.GetString("owner"), "result", "storage_error")
		return writeDownloadNotFound(event)
	}
	defer fsys.Close()
	reader, err := fsys.GetReader(resource.BaseFilesPath() + "/" + resource.GetString("file"))
	if err != nil {
		s.app.Logger().Error("resource download failed", "resourceId", resource.Id, "owner", resource.GetString("owner"), "result", "file_missing")
		return writeDownloadNotFound(event)
	}
	defer reader.Close()

	header.Set("Content-Type", resource.GetString("media_type"))
	header.Set("Content-Length", strconv.FormatInt(reader.Size(), 10))
	header.Set("Content-Disposition", contentDisposition(resource.GetString("original_name")))
	event.Response.WriteHeader(http.StatusOK)
	if event.Request.Method != http.MethodHead {
		_, _ = io.Copy(event.Response, reader)
	}
	s.app.Logger().Info("resource downloaded", "resourceId", resource.Id, "owner", resource.GetString("owner"), "leaseId", lease.Id, "result", "ok", "ip", event.RealIP())
	return nil
}

func writeDownloadNotFound(event *core.RequestEvent) error {
	return event.JSON(http.StatusNotFound, errorBody{
		Status:  http.StatusNotFound,
		Code:    CodeDownloadLeaseExpired,
		Message: "This download link is invalid or has expired.",
		Data:    map[string]any{},
	})
}

// contentDisposition builds an attachment header from the stored, sanitized
// original name; non-ASCII names are encoded per RFC 2231.
func contentDisposition(name string) string {
	if value := mime.FormatMediaType("attachment", map[string]string{"filename": name}); value != "" {
		return value
	}
	return "attachment"
}

// deleteExpiredLeases reclaims space only. Expired leases are already
// unusable, because validity is checked against expires_at on every download.
func deleteExpiredLeases(app core.App) error {
	now := types.NowDateTime().String()
	for {
		records, err := app.FindRecordsByFilter(LeasesCollectionName, "expires_at < {:now}", "", leaseCleanupBatch, 0, dbx.Params{"now": now})
		if err != nil {
			return err
		}
		for _, record := range records {
			if err := app.Delete(record); err != nil {
				return err
			}
		}
		if len(records) < leaseCleanupBatch {
			return nil
		}
	}
}
