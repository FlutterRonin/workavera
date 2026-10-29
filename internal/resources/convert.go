package resources

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	htmlmd "github.com/JohannesKaufmann/html-to-markdown/v2/converter"
	"github.com/JohannesKaufmann/html-to-markdown/v2/plugin/base"
	"github.com/JohannesKaufmann/html-to-markdown/v2/plugin/commonmark"
	"github.com/JohannesKaufmann/html-to-markdown/v2/plugin/strikethrough"
	"github.com/JohannesKaufmann/html-to-markdown/v2/plugin/table"
	"github.com/pocketbase/pocketbase/core"
	anydoc "github.com/xusenlin/go-anydoc"
	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
	"golang.org/x/net/html/charset"
	"golang.org/x/text/encoding"
	"golang.org/x/text/encoding/japanese"
	"golang.org/x/text/encoding/simplifiedchinese"
	"golang.org/x/text/encoding/traditionalchinese"
	xunicode "golang.org/x/text/encoding/unicode"
)

const (
	// maxConcurrentConversions is the only real concurrency limit: every
	// go-anydoc instance serves exactly one conversion, and HTML conversion
	// takes a slot too, so the two cannot stack beyond the memory budget.
	maxConcurrentConversions = 2
	conversionTimeout        = 60 * time.Second
	// guestMemoryPages caps one wasm guest at 64 MiB of linear memory.
	guestMemoryPages = 1024
)

// converter turns accepted uploads into Markdown. go-anydoc instances are
// created per conversion and closed right after, relying on the on-disk
// compilation cache to make creation cheap; nothing stays resident between
// uploads.
type converter struct {
	app      core.App
	cacheDir string
	slots    chan struct{}

	warmOnce sync.Once
	warmed   chan struct{}
	warmErr  error

	// convertDocument runs one go-anydoc conversion; tests replace it.
	convertDocument func(ctx context.Context, data []byte, ext string) (string, error)
}

func newConverter(app core.App) *converter {
	c := &converter{
		app: app,
		// Next to pb_data rather than inside it: the cache is regenerable
		// machine code, not user data, and must not end up in backups.
		cacheDir: filepath.Join(filepath.Dir(app.DataDir()), "anydoc_cache"),
		slots:    make(chan struct{}, maxConcurrentConversions),
		warmed:   make(chan struct{}),
	}
	c.convertDocument = c.convertWithAnydoc
	return c
}

func (c *converter) anydocOptions() []anydoc.Option {
	return []anydoc.Option{
		anydoc.WithCompiler(),
		anydoc.WithCompilationCache(c.cacheDir),
		anydoc.WithConcurrency(1),
		anydoc.WithMemoryLimitPages(guestMemoryPages),
		anydoc.WithMaxInputBytes(MaxFileSize),
	}
}

// warmInBackground makes sure the compilation cache exists without blocking
// startup, so the expensive first compilation happens while nothing else is
// running rather than inside an upload request. Uploads that arrive earlier
// wait for this same run instead of compiling their own copy.
func (c *converter) warmInBackground() {
	c.warmOnce.Do(func() {
		go func() {
			defer close(c.warmed)
			c.warmErr = c.warm()
		}()
	})
}

func (c *converter) warm() error {
	cacheHit := directoryHasEntries(c.cacheDir)
	mode := "compiler"
	if runtime.GOARCH != "amd64" && runtime.GOARCH != "arm64" {
		mode = "interpreter (no compiler backend for " + runtime.GOARCH + ")"
	}
	started := time.Now()
	instance, err := anydoc.New(c.anydocOptions()...)
	if err != nil {
		c.app.Logger().Error("resource converter warm-up failed; resource uploads are unavailable", "error", err)
		return err
	}
	if err := instance.Close(context.Background()); err != nil {
		c.app.Logger().Warn("resource converter close after warm-up failed", "error", err)
	}
	c.app.Logger().Info(
		"resource converter ready",
		"info", anydoc.Info(),
		"mode", mode,
		"cacheHit", cacheHit,
		"cacheDir", c.cacheDir,
		"duration", time.Since(started).String(),
	)
	return nil
}

func directoryHasEntries(dir string) bool {
	entries, err := os.ReadDir(dir)
	return err == nil && len(entries) > 0
}

// toMarkdown produces the Markdown for one upload and validates it. Every
// path that converts ends in the same size and emptiness checks; binary
// formats have no Markdown at all.
func (c *converter) toMarkdown(ctx context.Context, path conversionPath, ext string, data []byte) (string, error) {
	if path == pathBinary {
		return "", nil
	}
	var markdown string
	var err error
	switch path {
	case pathText:
		if !utf8.Valid(data) {
			return "", newError(CodeUnsupportedEncoding, http.StatusUnprocessableEntity, "Text files must be UTF-8 encoded.")
		}
		markdown = string(data)
	case pathAnydoc:
		markdown, err = c.convertAnydoc(ctx, ext, data)
	case pathHTML:
		markdown, err = c.convertHTML(ctx, data)
	}
	if err != nil {
		return "", err
	}
	if len(markdown) > MaxMarkdownSize {
		return "", newError(CodeConvertedContentTooLarge, http.StatusUnprocessableEntity, "The converted text is larger than 8 MB.")
	}
	if !hasMeaningfulText(markdown) {
		return "", newError(CodeConvertedContentEmpty, http.StatusUnprocessableEntity, "No text could be extracted from this file.")
	}
	return markdown, nil
}

var htmlComment = regexp.MustCompile(`(?s)<!--.*?-->`)

// hasMeaningfulText reports whether the Markdown holds any letter or digit
// outside HTML comments, so output made only of markup, separators, or
// placeholders never counts as a body.
func hasMeaningfulText(markdown string) bool {
	for _, r := range htmlComment.ReplaceAllString(markdown, "") {
		if unicode.IsLetter(r) || unicode.IsNumber(r) {
			return true
		}
	}
	return false
}

func (c *converter) acquire(ctx context.Context) (func(), error) {
	select {
	case c.slots <- struct{}{}:
		return func() { <-c.slots }, nil
	case <-ctx.Done():
		return nil, timeoutError(ctx.Err())
	}
}

func (c *converter) convertAnydoc(ctx context.Context, ext string, data []byte) (string, error) {
	select {
	case <-c.warmed:
	case <-ctx.Done():
		return "", timeoutError(ctx.Err())
	}
	if c.warmErr != nil {
		// Requests never compile on their own after a failed warm-up, so the
		// compilation peak cannot move into request handling.
		return "", wrapError(CodeConversionFailed, http.StatusServiceUnavailable, "Document conversion is unavailable on this server.", c.warmErr)
	}
	if ext == "csv" {
		decoded, err := csvToUTF8(data)
		if err != nil {
			return "", err
		}
		data = decoded
	}

	ctx, cancel := context.WithTimeout(ctx, conversionTimeout)
	defer cancel()
	release, err := c.acquire(ctx)
	if err != nil {
		return "", err
	}
	defer release()

	markdown, err := c.convertDocument(ctx, data, ext)
	if err != nil {
		return "", conversionError(ctx, err)
	}
	return strings.ToValidUTF8(markdown, ""), nil
}

func (c *converter) convertWithAnydoc(ctx context.Context, data []byte, ext string) (string, error) {
	instance, err := anydoc.New(c.anydocOptions()...)
	if err != nil {
		return "", err
	}
	defer instance.Close(context.Background())
	return instance.Convert(ctx, data, ext)
}

var ocrDetail = regexp.MustCompile(`(?i)\bpages?\b[^\n]{0,120}\bneeds? OCR\b`)

// conversionError maps converter failures to stable codes. The converter's own
// message stays in the wrapped cause for the log; only the OCR page range is
// passed on, because the user needs it to know which pages are scans.
func conversionError(ctx context.Context, err error) error {
	switch {
	case ctx.Err() != nil || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled):
		return timeoutError(err)
	case errors.Is(err, anydoc.ErrEncrypted):
		return wrapError(CodeDocumentEncrypted, http.StatusUnprocessableEntity, "The document is password-protected.", err)
	case errors.Is(err, anydoc.ErrMalformed), errors.Is(err, anydoc.ErrMissingPart):
		return wrapError(CodeDocumentMalformed, http.StatusUnprocessableEntity, "The document is damaged or incomplete.", err)
	case errors.Is(err, anydoc.ErrNeedsOCR):
		message := "This PDF contains scanned pages without extractable text."
		if detail := ocrDetail.FindString(err.Error()); detail != "" {
			message = strings.TrimSuffix(message, ".") + ": " + detail + "."
		}
		return wrapError(CodeDocumentNeedsOCR, http.StatusUnprocessableEntity, message, err)
	case errors.Is(err, anydoc.ErrResourceLimit):
		return wrapError(CodeConversionFailed, http.StatusUnprocessableEntity, "The document is too large or complex to convert.", err)
	default:
		return wrapError(CodeConversionFailed, http.StatusUnprocessableEntity, "The document could not be converted.", err)
	}
}

func timeoutError(cause error) error {
	return wrapError(CodeConversionTimeout, http.StatusUnprocessableEntity, "Converting the document took too long.", cause)
}

// csvToUTF8 works around go-anydoc decoding non-UTF-8 CSV as mojibake. It is
// a heuristic: UTF-8 and UTF-16 are recognized by validity or byte order mark,
// and otherwise the first legacy encoding that decodes without errors wins,
// trying Simplified Chinese, Traditional Chinese, and Japanese in that order.
func csvToUTF8(data []byte) ([]byte, error) {
	if utf8.Valid(data) {
		return bytes.TrimPrefix(data, []byte("\xef\xbb\xbf")), nil
	}
	if bytes.HasPrefix(data, []byte{0xff, 0xfe}) || bytes.HasPrefix(data, []byte{0xfe, 0xff}) {
		decoded, err := xunicode.UTF16(xunicode.LittleEndian, xunicode.ExpectBOM).NewDecoder().Bytes(data)
		if err == nil && utf8.Valid(decoded) {
			return decoded, nil
		}
	}
	for _, candidate := range []encoding.Encoding{simplifiedchinese.GB18030, traditionalchinese.Big5, japanese.ShiftJIS} {
		decoded, err := candidate.NewDecoder().Bytes(data)
		if err == nil && utf8.Valid(decoded) && !bytes.ContainsRune(decoded, utf8.RuneError) {
			return decoded, nil
		}
	}
	return nil, newError(CodeUnsupportedEncoding, http.StatusUnprocessableEntity, "The file's text encoding is not supported.")
}

// strippedElements carry no readable text, or only scripts, styles, and
// embedded content that would otherwise dominate a saved web page.
var strippedElements = map[atom.Atom]bool{
	atom.Script:   true,
	atom.Style:    true,
	atom.Noscript: true,
	atom.Template: true,
	atom.Iframe:   true,
	atom.Object:   true,
	atom.Embed:    true,
	atom.Svg:      true,
}

var htmlConverter = htmlmd.NewConverter(htmlmd.WithPlugins(
	base.NewBasePlugin(),
	commonmark.NewCommonmarkPlugin(),
	table.NewTablePlugin(),
	strikethrough.NewStrikethroughPlugin(),
))

// convertHTML converts only the uploaded bytes: nothing the page references
// is fetched, and relative links stay relative. It runs in-process and cannot
// be interrupted midway, so it is bounded by the input size and holds a
// conversion slot like any other conversion.
func (c *converter) convertHTML(ctx context.Context, data []byte) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, conversionTimeout)
	defer cancel()
	release, err := c.acquire(ctx)
	if err != nil {
		return "", err
	}
	defer release()

	decoded, err := decodeHTML(data)
	if err != nil {
		return "", err
	}
	document, err := html.Parse(bytes.NewReader(decoded))
	if err != nil {
		return "", wrapError(CodeDocumentMalformed, http.StatusUnprocessableEntity, "The HTML could not be parsed.", err)
	}
	stripHTML(document)
	markdown, err := htmlConverter.ConvertNode(document, htmlmd.WithContext(ctx))
	if err != nil {
		return "", conversionError(ctx, err)
	}
	return strings.ToValidUTF8(string(markdown), ""), nil
}

// decodeHTML decodes the page to UTF-8 using its byte order mark or declared
// charset, the same detection Reading uses for fetched pages.
func decodeHTML(data []byte) ([]byte, error) {
	enc, name, _ := charset.DetermineEncoding(data, "text/html")
	if enc == nil {
		return nil, newError(CodeUnsupportedEncoding, http.StatusUnprocessableEntity, "The file's text encoding is not supported.")
	}
	if name == "utf-8" {
		if !utf8.Valid(data) {
			return nil, newError(CodeUnsupportedEncoding, http.StatusUnprocessableEntity, "The file's text encoding is not supported.")
		}
		return data, nil
	}
	decoded, err := enc.NewDecoder().Bytes(data)
	if err != nil {
		return nil, wrapError(CodeUnsupportedEncoding, http.StatusUnprocessableEntity, "The file's text encoding is not supported.", err)
	}
	return decoded, nil
}

func stripHTML(node *html.Node) {
	for child := node.FirstChild; child != nil; {
		next := child.NextSibling
		if child.Type == html.ElementNode && (strippedElements[child.DataAtom] || isInlineDataImage(child)) {
			node.RemoveChild(child)
		} else {
			stripHTML(child)
		}
		child = next
	}
}

func isInlineDataImage(node *html.Node) bool {
	if node.DataAtom != atom.Img {
		return false
	}
	for _, attribute := range node.Attr {
		if strings.EqualFold(attribute.Key, "src") && strings.HasPrefix(strings.ToLower(strings.TrimSpace(attribute.Val)), "data:") {
			return true
		}
	}
	return false
}
