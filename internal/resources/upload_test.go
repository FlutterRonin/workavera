package resources

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tests"
	anydoc "github.com/xusenlin/go-anydoc"
	"golang.org/x/text/encoding/simplifiedchinese"

	_ "github.com/xusenlin/workavera/migrations"
)

func newTestApp(t *testing.T) *tests.TestApp {
	t.Helper()
	app, err := tests.NewTestApp()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(app.Cleanup)
	return app
}

func createTestUser(t *testing.T, app core.App, email string) *core.Record {
	t.Helper()
	users, err := app.FindCollectionByNameOrId("users")
	if err != nil {
		t.Fatal(err)
	}
	user := core.NewRecord(users)
	user.SetEmail(email)
	user.SetPassword("password123")
	user.Set("name", email)
	user.SetVerified(true)
	if err := app.Save(user); err != nil {
		t.Fatal(err)
	}
	return user
}

// newTestConverter skips the real warm-up and replaces go-anydoc, so tests
// never compile the wasm module. A nil stub fails every document conversion.
func newTestConverter(app core.App, stub func(ctx context.Context, data []byte, ext string) (string, error)) *converter {
	c := newConverter(app)
	c.warmOnce.Do(func() {})
	close(c.warmed)
	if stub == nil {
		stub = func(context.Context, []byte, string) (string, error) {
			return "", errors.New("unexpected document conversion")
		}
	}
	c.convertDocument = stub
	return c
}

func uploadText(t *testing.T, app core.App, conv *converter, ownerID, name, content string) Resource {
	t.Helper()
	resource, err := Upload(context.Background(), app, conv, ownerID, name, []byte(content))
	if err != nil {
		t.Fatalf("upload %s: %v", name, err)
	}
	return resource
}

func errorCode(err error) string {
	var resourceErr *Error
	if errors.As(err, &resourceErr) {
		return resourceErr.Code
	}
	return ""
}

func countResources(t *testing.T, app core.App) int {
	t.Helper()
	count, err := app.CountRecords(CollectionName)
	if err != nil {
		t.Fatal(err)
	}
	return int(count)
}

func TestUploadStoresTextUnchangedAndReusesIdenticalFiles(t *testing.T) {
	app := newTestApp(t)
	owner := createTestUser(t, app, "resource-owner@example.com")
	conv := newTestConverter(app, nil)

	content := "# Plan\n\nShip the resources library.\n"
	resource := uploadText(t, app, conv, owner.Id, `C:\Users\me\..\Plan.md`, content)
	if resource.OriginalName != "Plan.md" || resource.DisplayName != "Plan.md" || resource.MediaType != "text/markdown" || resource.Size != int64(len(content)) {
		t.Fatalf("unexpected resource: %#v", resource)
	}
	if !strings.HasPrefix(resource.URL, "/api/files/user_resources/"+resource.ID+"/") {
		t.Fatalf("unexpected protected file URL: %s", resource.URL)
	}
	record, err := app.FindRecordById(CollectionName, resource.ID)
	if err != nil {
		t.Fatal(err)
	}
	if record.GetString("converted_markdown") != content || record.GetInt("converted_size") != len(content) || len(record.GetString("sha256")) != 64 || record.GetString("file") == "" {
		t.Fatalf("unexpected stored resource: %#v", record)
	}

	again := uploadText(t, app, conv, owner.Id, "Plan.md", content)
	if again.ID != resource.ID {
		t.Fatalf("same content and name must reuse the resource: %s vs %s", again.ID, resource.ID)
	}
	renamed := uploadText(t, app, conv, owner.Id, "Plan copy.md", content)
	if renamed.ID == resource.ID {
		t.Fatal("same content under a different name must be a separate resource")
	}
	other := createTestUser(t, app, "resource-other@example.com")
	if foreign := uploadText(t, app, conv, other.Id, "Plan.md", content); foreign.ID == resource.ID {
		t.Fatal("another owner's upload must never reuse this resource")
	}
}

func TestUploadRejectsInvalidFilesWithoutSavingAnything(t *testing.T) {
	app := newTestApp(t)
	owner := createTestUser(t, app, "resource-reject@example.com")
	conv := newTestConverter(app, func(context.Context, []byte, string) (string, error) {
		t.Fatal("rejected uploads must not reach the converter")
		return "", nil
	})
	gbk, err := simplifiedchinese.GBK.NewEncoder().Bytes([]byte("中文笔记"))
	if err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name string
		file string
		data []byte
		code string
	}{
		{"empty", "empty.txt", nil, CodeFileEmpty},
		{"too large", "big.txt", bytes.Repeat([]byte("a"), MaxFileSize+1), CodeFileTooLarge},
		{"pdf disguised as text", "report.txt", []byte("%PDF-1.7\n%\xe2\xe3\xcf\xd3\n1 0 obj\n"), CodeUnsupportedMediaType},
		{"text disguised as pdf", "report.pdf", []byte("just some text"), CodeUnsupportedMediaType},
		{"unsupported extension", "tool.exe", []byte("MZ\x90\x00"), CodeUnsupportedMediaType},
		{"no extension", "README", []byte("hello"), CodeUnsupportedMediaType},
		{"svg image", "drawing.svg", []byte(`<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`), CodeUnsupportedMediaType},
		{"zip disguised as png", "chart.png", []byte("PK\x03\x04"), CodeUnsupportedMediaType},
		{"non utf-8 text", "notes.txt", gbk, CodeUnsupportedEncoding},
		{"empty name", "   ", []byte("hello"), CodeInvalidFilename},
		{"path only", "folder/", []byte("hello"), CodeInvalidFilename},
		{"too long name", strings.Repeat("a", 252) + ".txt", []byte("hello"), CodeInvalidFilename},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			_, err := Upload(context.Background(), app, conv, owner.Id, test.file, test.data)
			if code := errorCode(err); code != test.code {
				t.Fatalf("expected %s, got %v", test.code, err)
			}
		})
	}
	if count := countResources(t, app); count != 0 {
		t.Fatalf("rejected uploads saved %d resources", count)
	}
}

func TestConversionFailuresMapToStableCodesAndSaveNothing(t *testing.T) {
	pdf := []byte("%PDF-1.7\n%\xe2\xe3\xcf\xd3\n1 0 obj\n<<>>\nendobj\n")
	cases := []struct {
		name    string
		output  string
		err     error
		code    string
		message string
	}{
		{"needs ocr", "", &anydoc.ConvertError{Kind: anydoc.ErrNeedsOCR, Detail: "page 2 of 3 needs OCR"}, CodeDocumentNeedsOCR, "page 2 of 3 needs OCR"},
		{"encrypted", "", &anydoc.ConvertError{Kind: anydoc.ErrEncrypted, Detail: "/secret/path"}, CodeDocumentEncrypted, "password-protected"},
		{"malformed", "", &anydoc.ConvertError{Kind: anydoc.ErrMalformed}, CodeDocumentMalformed, "damaged"},
		{"timeout", "", context.DeadlineExceeded, CodeConversionTimeout, "too long"},
		{"other", "", errors.New("parser stack trace"), CodeConversionFailed, "could not be converted"},
		{"empty output", "  \n<!-- page 1 -->\n---\n", nil, CodeConvertedContentEmpty, "No text"},
		{"oversized output", strings.Repeat("a", MaxMarkdownSize+1), nil, CodeConvertedContentTooLarge, "8 MB"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			app := newTestApp(t)
			owner := createTestUser(t, app, "resource-convert@example.com")
			conv := newTestConverter(app, func(_ context.Context, _ []byte, ext string) (string, error) {
				if ext != "pdf" {
					t.Fatalf("converter must get the normalized extension, got %q", ext)
				}
				return test.output, test.err
			})
			_, err := Upload(context.Background(), app, conv, owner.Id, "Scan.PDF", pdf)
			var resourceErr *Error
			if !errors.As(err, &resourceErr) || resourceErr.Code != test.code || !strings.Contains(resourceErr.Message, test.message) {
				t.Fatalf("expected %s mentioning %q, got %v", test.code, test.message, err)
			}
			if strings.Contains(resourceErr.Message, "/secret/path") || strings.Contains(resourceErr.Message, "stack") {
				t.Fatalf("converter diagnostics leaked into the message: %s", resourceErr.Message)
			}
			if count := countResources(t, app); count != 0 {
				t.Fatalf("a failed conversion saved %d resources", count)
			}
		})
	}
}

func TestDocumentConversionStoresMetadataAndDecodesLegacyCSV(t *testing.T) {
	app := newTestApp(t)
	owner := createTestUser(t, app, "resource-csv@example.com")
	var received string
	conv := newTestConverter(app, func(_ context.Context, data []byte, ext string) (string, error) {
		if ext != "csv" {
			t.Fatalf("unexpected extension %q", ext)
		}
		received = string(data)
		return "| 名称 | 数量 |\n| --- | --- |\n| 苹果 | 3 |\n", nil
	})
	gbk, err := simplifiedchinese.GBK.NewEncoder().Bytes([]byte("名称,数量\n苹果,3\n"))
	if err != nil {
		t.Fatal(err)
	}
	resource, err := Upload(context.Background(), app, conv, owner.Id, "fruit.csv", gbk)
	if err != nil {
		t.Fatal(err)
	}
	if received != "名称,数量\n苹果,3\n" {
		t.Fatalf("CSV must reach the converter as UTF-8, got %q", received)
	}
	if resource.MediaType != "text/csv" || resource.Size != int64(len(gbk)) {
		t.Fatalf("unexpected resource: %#v", resource)
	}
}

func TestHTMLConversionDropsScriptsAndInlineImages(t *testing.T) {
	app := newTestApp(t)
	owner := createTestUser(t, app, "resource-html@example.com")
	conv := newTestConverter(app, nil)

	page := `<!DOCTYPE html><html><head><title>Saved</title><style>body{color:red}</style>
<script>alert("injected")</script></head><body>
<h1>Quarterly report</h1>
<p>See <a href="/details/q3">the details</a>.</p>
<img src="data:image/png;base64,iVBORw0KGgo=" alt="inline">
<svg><text>vector noise</text></svg>
<noscript>enable scripts</noscript>
<table><tr><th>Region</th><th>Revenue</th></tr><tr><td>North</td><td>42</td></tr></table>
<ul><li>First</li><li>Second</li></ul>
</body></html>`
	resource, err := Upload(context.Background(), app, conv, owner.Id, "report.html", []byte(page))
	if err != nil {
		t.Fatal(err)
	}
	record, err := app.FindRecordById(CollectionName, resource.ID)
	if err != nil {
		t.Fatal(err)
	}
	markdown := record.GetString("converted_markdown")
	for _, expected := range []string{"# Quarterly report", "[the details](/details/q3)", "| Region | Revenue |", "| North", "- First"} {
		if !strings.Contains(markdown, expected) {
			t.Fatalf("markdown is missing %q:\n%s", expected, markdown)
		}
	}
	for _, unexpected := range []string{"alert", "color:red", "data:image", "vector noise", "enable scripts"} {
		if strings.Contains(markdown, unexpected) {
			t.Fatalf("markdown kept %q:\n%s", unexpected, markdown)
		}
	}
	if resource.MediaType != "text/html" {
		t.Fatalf("unexpected media type %q", resource.MediaType)
	}

	gbkPage, err := simplifiedchinese.GBK.NewEncoder().Bytes([]byte(`<html><head><meta charset="gbk"></head><body><h2>季度报告</h2><p>收入增长</p></body></html>`))
	if err != nil {
		t.Fatal(err)
	}
	legacy, err := Upload(context.Background(), app, conv, owner.Id, "legacy.htm", gbkPage)
	if err != nil {
		t.Fatal(err)
	}
	stored, err := app.FindRecordById(CollectionName, legacy.ID)
	if err != nil {
		t.Fatal(err)
	}
	if markdown := stored.GetString("converted_markdown"); !strings.Contains(markdown, "## 季度报告") || !strings.Contains(markdown, "收入增长") {
		t.Fatalf("legacy-encoded HTML was not decoded:\n%s", markdown)
	}
}

func TestSanitizeNameStripsPathsAndDisguises(t *testing.T) {
	cases := map[string]string{
		"../../etc/passwd.txt":      "passwd.txt",
		`C:\fakepath\report.pdf`:    "report.pdf",
		"report\u202Etxt.exe":       "reporttxt.exe",
		"line\r\nbreak.md":          "linebreak.md",
		` "quoted" name.txt `:       "quoted name.txt",
		"年度报告 2026.docx":            "年度报告 2026.docx",
		"tab\there.csv":             "tabhere.csv",
		"zero\u200bwidth.json":      "zerowidth.json",
		"replacement\ufffdchar.txt": "replacementchar.txt",
	}
	for input, expected := range cases {
		if got := sanitizeName(input); got != expected {
			t.Errorf("sanitizeName(%q) = %q, want %q", input, got, expected)
		}
	}
}

func TestBinaryUploadsAreStoredWithoutText(t *testing.T) {
	app := newTestApp(t)
	owner := createTestUser(t, app, "resource-binary@example.com")
	conv := newTestConverter(app, func(context.Context, []byte, string) (string, error) {
		t.Fatal("binary uploads must not reach the converter")
		return "", nil
	})

	cases := []struct {
		file      string
		data      []byte
		mediaType string
	}{
		{"chart.png", []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR"), "image/png"},
		{"photo.jpg", []byte("\xff\xd8\xff\xe0\x00\x10JFIF\x00"), "image/jpeg"},
		{"photo.jpeg", []byte("\xff\xd8\xff\xe1\x00\x10Exif\x00"), "image/jpeg"},
		{"loop.gif", []byte("GIF89a\x01\x00\x01\x00"), "image/gif"},
		{"image.webp", []byte("RIFF\x24\x00\x00\x00WEBPVP8 "), "image/webp"},
		{"bundle.zip", []byte("PK\x03\x04\x14\x00\x00\x00"), "application/zip"},
	}
	for _, test := range cases {
		t.Run(test.file, func(t *testing.T) {
			resource, err := Upload(context.Background(), app, conv, owner.Id, test.file, test.data)
			if err != nil {
				t.Fatalf("upload: %v", err)
			}
			if resource.MediaType != test.mediaType || resource.ConvertedSize != 0 || !IsBinary(resource.MediaType) {
				t.Fatalf("unexpected binary resource: %#v", resource)
			}
			if _, err := Read(context.Background(), app, owner.Id, resource.ID, "", 0); errorCode(err) != CodeResourceHasNoText {
				t.Fatalf("reading a binary resource: expected %s, got %v", CodeResourceHasNoText, err)
			}
			if _, err := Search(context.Background(), app, owner.Id, resource.ID, "PK", 0); errorCode(err) != CodeResourceHasNoText {
				t.Fatalf("searching a binary resource: expected %s, got %v", CodeResourceHasNoText, err)
			}
		})
	}

	list, err := List(context.Background(), app, owner.Id, "chart", 0)
	if err != nil || len(list.Resources) != 1 || list.Resources[0].MatchedIn != "name" {
		t.Fatalf("binary resources must still match by name: %#v %v", list, err)
	}
}

func TestSaveUsesTheRegisteredConverterAndDisplayName(t *testing.T) {
	app := newTestApp(t)
	owner := createTestUser(t, app, "resource-save@example.com")
	if _, err := Save(context.Background(), app, owner.Id, "out.txt", "", []byte("hello")); errorCode(err) != CodeConversionFailed {
		t.Fatalf("without a registered converter Save must fail, got %v", err)
	}
	app.Store().Set(converterStoreKey, newTestConverter(app, nil))

	resource, err := Save(context.Background(), app, owner.Id, "out.txt", "Quarterly numbers", []byte("hello"))
	if err != nil {
		t.Fatal(err)
	}
	if resource.DisplayName != "Quarterly numbers" || resource.OriginalName != "out.txt" || resource.MediaType != "text/plain" {
		t.Fatalf("unexpected saved resource: %#v", resource)
	}
	again, err := Save(context.Background(), app, owner.Id, "out.txt", "Another name", []byte("hello"))
	if err != nil || again.ID != resource.ID || again.DisplayName != "Quarterly numbers" {
		t.Fatalf("saving the same file again must return the existing resource unchanged: %#v %v", again, err)
	}
}
