package tools

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tests"

	workdocs "github.com/xusenlin/workavera/internal/docs"
	"github.com/xusenlin/workavera/internal/resources"
)

func TestSendFilesUsesStoredRecordsOfTheUser(t *testing.T) {
	app, err := tests.NewTestApp()
	if err != nil {
		t.Fatal(err)
	}
	defer app.Cleanup()
	resources.Register(app)
	users, err := app.FindCollectionByNameOrId("users")
	if err != nil {
		t.Fatal(err)
	}
	newUser := func(email string) *core.Record {
		user := core.NewRecord(users)
		user.SetEmail(email)
		user.SetPassword("password123")
		user.Set("name", email)
		if err := app.Save(user); err != nil {
			t.Fatal(err)
		}
		return user
	}
	owner := newUser("send-files-owner@example.com")
	other := newUser("send-files-other@example.com")
	ctx := context.Background()

	resource, err := resources.Save(ctx, app, owner.Id, "notes.txt", "Meeting notes", []byte("hello"))
	if err != nil {
		t.Fatal(err)
	}
	doc, err := workdocs.Create(ctx, app, owner.Id, workdocs.CreateInput{Title: "Plan", Kind: workdocs.KindHTML, Content: "<p>plan</p>"})
	if err != nil {
		t.Fatal(err)
	}
	foreignResource, err := resources.Save(ctx, app, other.Id, "secret.txt", "", []byte("secret"))
	if err != nil {
		t.Fatal(err)
	}
	foreignDoc, err := workdocs.Create(ctx, app, other.Id, workdocs.CreateInput{Title: "Private"})
	if err != nil {
		t.Fatal(err)
	}

	files, err := sendFiles(ctx, app, owner.Id, []chatSendFileItem{{Kind: "doc", ID: doc.ID}, {Kind: "resource", ID: " " + resource.ID + " "}})
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 2 ||
		files[0] != (SentFile{Kind: "doc", DocID: doc.ID, Name: "Plan", DocKind: workdocs.KindHTML}) ||
		files[1] != (SentFile{Kind: "resource", ResourceID: resource.ID, Name: "Meeting notes", MediaType: "text/plain", Size: 5}) {
		t.Fatalf("unexpected files: %#v", files)
	}

	var resourceErr *resources.Error
	if _, err := sendFiles(ctx, app, owner.Id, []chatSendFileItem{{Kind: "resource", ID: foreignResource.ID}}); !errors.As(err, &resourceErr) || resourceErr.Code != resources.CodeResourceNotFound {
		t.Fatalf("another user's resource must not be shown, got %v", err)
	}
	if _, err := sendFiles(ctx, app, owner.Id, []chatSendFileItem{{Kind: "doc", ID: foreignDoc.ID}}); err == nil || !strings.HasPrefix(err.Error(), "document_not_found") {
		t.Fatalf("another user's private document must not be shown, got %v", err)
	}
	for _, items := range [][]chatSendFileItem{
		nil,
		{{Kind: "resource", ID: resource.ID}, {Kind: "resource", ID: resource.ID}},
		{{Kind: "link", ID: resource.ID}},
		{{Kind: "doc", ID: " "}},
		make([]chatSendFileItem, maxSentFiles+1),
	} {
		if _, err := sendFiles(ctx, app, owner.Id, items); !errors.Is(err, errInvalidSentFiles) {
			t.Fatalf("invalid items %v must be rejected, got %v", items, err)
		}
	}
}
