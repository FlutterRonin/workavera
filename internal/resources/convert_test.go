package resources

import (
	"archive/zip"
	"bytes"
	"context"
	"strings"
	"testing"
)

// TestRealConverterConvertsDocx runs the embedded go-anydoc module with the
// production options. Compiling it costs about a second and several hundred
// MiB the first time, so it is skipped in short mode.
func TestRealConverterConvertsDocx(t *testing.T) {
	if testing.Short() {
		t.Skip("compiles the go-anydoc wasm module")
	}
	app := newTestApp(t)
	owner := createTestUser(t, app, "real-converter@example.com")
	conv := newConverter(app)
	conv.cacheDir = t.TempDir()
	conv.warmInBackground()

	resource, err := Upload(context.Background(), app, conv, owner.Id, "brief.docx", minimalDocx(t, "Resources ship in the first version."))
	if err != nil {
		t.Fatal(err)
	}
	if conv.warmErr != nil {
		t.Fatalf("warm-up failed: %v", conv.warmErr)
	}
	record, err := app.FindRecordById(CollectionName, resource.ID)
	if err != nil {
		t.Fatal(err)
	}
	if markdown := record.GetString("converted_markdown"); !strings.Contains(markdown, "Resources ship in the first version.") {
		t.Fatalf("unexpected markdown: %q", markdown)
	}
	if !directoryHasEntries(conv.cacheDir) {
		t.Fatal("warm-up must populate the compilation cache")
	}
}

func minimalDocx(t *testing.T, text string) []byte {
	t.Helper()
	var buffer bytes.Buffer
	archive := zip.NewWriter(&buffer)
	files := []struct{ name, body string }{
		{"[Content_Types].xml", `<?xml version="1.0" encoding="UTF-8"?><Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"><Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/><Default Extension="xml" ContentType="application/xml"/><Override PartName="/word/document.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"/></Types>`},
		{"_rels/.rels", `<?xml version="1.0" encoding="UTF-8"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="word/document.xml"/></Relationships>`},
		{"word/document.xml", `<?xml version="1.0" encoding="UTF-8"?><w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body><w:p><w:r><w:t>` + text + `</w:t></w:r></w:p></w:body></w:document>`},
	}
	for _, file := range files {
		writer, err := archive.Create(file.name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := writer.Write([]byte(file.body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}
