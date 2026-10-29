package resources

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path"
	"strings"
	"unicode"

	"github.com/gabriel-vasile/mimetype"
	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/filesystem"
	anydoc "github.com/xusenlin/go-anydoc"
)

// conversionPath is how an accepted format becomes Markdown.
type conversionPath int

const (
	pathText conversionPath = iota
	pathAnydoc
	pathHTML
	// pathBinary stores the file as it is, without any Markdown.
	pathBinary
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

	// Binary resources are stored and downloaded, never converted. SVG is
	// left out on purpose: it is XML that can run scripts when opened.
	"png":  {mediaType: "image/png", detected: []string{"image/png"}, path: pathBinary},
	"jpg":  {mediaType: "image/jpeg", detected: []string{"image/jpeg"}, path: pathBinary},
	"jpeg": {mediaType: "image/jpeg", detected: []string{"image/jpeg"}, path: pathBinary},
	"gif":  {mediaType: "image/gif", detected: []string{"image/gif"}, path: pathBinary},
	"webp": {mediaType: "image/webp", detected: []string{"image/webp"}, path: pathBinary},
	// The ZIP signature also opens OOXML and EPUB files, which the detector
	// names first; those are still ZIP archives and pass as one through the
	// detected type's ancestors.
	"zip": {mediaType: "application/zip", detected: []string{"application/zip"}, path: pathBinary},
	// TIFF is what most scanners write, and HEIC what iPhones photograph in;
	// both can be OCRed in the sandbox.
	"tif":  {mediaType: "image/tiff", detected: []string{"image/tiff"}, path: pathBinary},
	"tiff": {mediaType: "image/tiff", detected: []string{"image/tiff"}, path: pathBinary},
	"bmp":  {mediaType: "image/bmp", detected: []string{"image/bmp"}, path: pathBinary},
	"heic": {mediaType: "image/heic", detected: []string{"image/heic", "image/heic-sequence", "image/heif"}, path: pathBinary},
	"tar":  {mediaType: "application/x-tar", detected: []string{"application/x-tar"}, path: pathBinary},
	"gz":   {mediaType: "application/gzip", detected: []string{"application/gzip"}, path: pathBinary},
	"tgz":  {mediaType: "application/gzip", detected: []string{"application/gzip"}, path: pathBinary},
	"7z":   {mediaType: "application/x-7z-compressed", detected: []string{"application/x-7z-compressed"}, path: pathBinary},
}

// textFormats are code, configuration, and data files kept as text: their
// Markdown is the file itself, like .txt. Each maps to the media type stored
// for it.
var textFormats = map[string]string{
	"tsv": "text/tab-separated-values", "log": "text/plain", "ipynb": "application/x-ipynb+json",
	"xml": "application/xml", "yaml": "application/yaml", "yml": "application/yaml", "toml": "application/toml",
	"ini": "text/plain", "cfg": "text/plain", "conf": "text/plain", "env": "text/plain",
	"sql": "application/sql", "sh": "application/x-sh", "bash": "application/x-sh", "zsh": "application/x-sh",
	"py": "text/x-python", "js": "text/javascript", "mjs": "text/javascript", "cjs": "text/javascript",
	"ts": "text/x-typescript", "tsx": "text/x-typescript", "jsx": "text/javascript",
	"go": "text/x-go", "rs": "text/x-rust", "java": "text/x-java", "kt": "text/x-kotlin", "swift": "text/x-swift",
	"c": "text/x-c", "h": "text/x-c", "cpp": "text/x-c++", "cc": "text/x-c++", "hpp": "text/x-c++",
	"cs": "text/x-csharp", "rb": "text/x-ruby", "php": "text/x-php", "lua": "text/x-lua", "r": "text/x-r",
	"css": "text/css", "scss": "text/x-scss", "vue": "text/x-vue", "svelte": "text/x-svelte",
}

func init() {
	for ext, mediaType := range textFormats {
		formats[ext] = format{mediaType: mediaType, detected: []string{mimeText}, path: pathText}
	}
}

// IsBinary reports whether resources of the stored media type are binary:
// they have no Markdown and can only be downloaded or copied.
func IsBinary(mediaType string) bool {
	for _, spec := range formats {
		if spec.mediaType == mediaType {
			return spec.path == pathBinary
		}
	}
	return false
}

// Reasons a resource is stored without Markdown. A file whose text cannot be
// extracted is still kept, so it can be downloaded or processed in the
// sandbox, for example to OCR a scanned PDF.
const (
	NoTextBinary    = "binary"
	NoTextNeedsOCR  = "needs_ocr"
	NoTextEncrypted = "encrypted"
	NoTextEmpty     = "empty"
	NoTextTooLarge  = "too_complex"
)

// noTextReason maps a conversion failure to the reason a file is kept
// without text, or "" when the upload must fail: a damaged file, which may
// be a broken upload, or a transient failure worth retrying.
func noTextReason(err error) string {
	var resourceErr *Error
	if !errors.As(err, &resourceErr) {
		return ""
	}
	if errors.Is(err, anydoc.ErrResourceLimit) {
		return NoTextTooLarge
	}
	switch resourceErr.Code {
	case CodeDocumentNeedsOCR:
		return NoTextNeedsOCR
	case CodeDocumentEncrypted:
		return NoTextEncrypted
	case CodeConvertedContentEmpty:
		return NoTextEmpty
	case CodeConvertedContentTooLarge:
		return NoTextTooLarge
	}
	return ""
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
	if err := checkSize(event.App, file.Size); err != nil {
		return "", nil, err
	}
	reader, err := file.Reader.Open()
	if err != nil {
		return "", nil, err
	}
	defer reader.Close()
	data, err := io.ReadAll(io.LimitReader(reader, MaxFileSize(event.App)+1))
	if err != nil {
		return "", nil, err
	}
	return file.OriginalName, data, nil
}

func checkSize(app core.App, size int64) error {
	if size <= 0 {
		return newError(CodeFileEmpty, http.StatusBadRequest, "The file is empty.")
	}
	if limit := MaxFileSize(app); size > limit {
		return newError(CodeFileTooLarge, http.StatusRequestEntityTooLarge, fmt.Sprintf("Files must be %d MB or smaller.", limit/(1024*1024)))
	}
	return nil
}

// Upload stores a new resource for the owner, converting it to Markdown in
// the same request. A damaged file or a failed conversion leaves neither a
// record nor a file behind; binary formats and documents whose text cannot be
// extracted, such as scanned PDFs, are stored without Markdown and with the
// reason. Uploading the same content under the same original name returns
// the existing resource.
func Upload(ctx context.Context, app core.App, conv *converter, ownerID, name string, data []byte) (Resource, error) {
	return upload(ctx, app, conv, ownerID, name, "", data)
}

// Save stores bytes produced on the server, such as a file exported from the
// sandbox, under exactly the rules of a user upload. The name supplies the
// extension; displayName, when not empty, names a newly created resource
// instead of it.
func Save(ctx context.Context, app core.App, ownerID, name, displayName string, data []byte) (Resource, error) {
	conv, _ := app.Store().Get(converterStoreKey).(*converter)
	if conv == nil {
		return Resource{}, newError(CodeConversionFailed, http.StatusServiceUnavailable, "Document conversion is unavailable on this server.")
	}
	return upload(ctx, app, conv, ownerID, name, displayName, data)
}

func upload(ctx context.Context, app core.App, conv *converter, ownerID, name, displayName string, data []byte) (Resource, error) {
	if err := checkSize(app, int64(len(data))); err != nil {
		return Resource{}, err
	}
	originalName := sanitizeName(name)
	if originalName == "" || len([]rune(originalName)) > MaxNameLength || originalName == "." || originalName == ".." {
		return Resource{}, newError(CodeInvalidFilename, http.StatusBadRequest, "The file name is missing or invalid.")
	}
	displayName = sanitizeName(displayName)
	if displayName == "" {
		displayName = originalName
	}
	if len([]rune(displayName)) > MaxNameLength {
		return Resource{}, newError(CodeInvalidFilename, http.StatusBadRequest, "The display name must be 1 to 255 characters.")
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

	reason := ""
	markdown, err := conv.toMarkdown(ctx, spec.path, ext, data)
	switch {
	case spec.path == pathBinary:
		reason = NoTextBinary
	case err != nil:
		if reason = noTextReason(err); reason == "" {
			return Resource{}, err
		}
		markdown = ""
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
	record.Set("display_name", displayName)
	record.Set("media_type", spec.mediaType)
	record.Set("size", len(data))
	record.Set("sha256", contentHash)
	record.Set("converted_markdown", markdown)
	record.Set("converted_size", len(markdown))
	record.Set("no_text_reason", reason)
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
