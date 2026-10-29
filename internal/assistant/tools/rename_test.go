package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"charm.land/fantasy"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tests"

	workdocs "github.com/xusenlin/workavera/internal/docs"
	"github.com/xusenlin/workavera/internal/resources"
)

func runBatchTool(t *testing.T, tool fantasy.AgentTool, input string) batchResult {
	t.Helper()
	response, err := tool.Run(context.Background(), fantasy.ToolCall{ID: "call-1", Name: tool.Info().Name, Input: input})
	if err != nil {
		t.Fatal(err)
	}
	var result batchResult
	if err := json.Unmarshal([]byte(response.Content), &result); err != nil {
		t.Fatalf("unexpected %s response: %s", tool.Info().Name, response.Content)
	}
	return result
}

func TestRenameToolsReportEachItem(t *testing.T) {
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
	owner := newUser("rename-owner@example.com")
	other := newUser("rename-other@example.com")
	ctx := context.Background()

	first, err := resources.Save(ctx, app, owner.Id, "a.txt", "", []byte("first"))
	if err != nil {
		t.Fatal(err)
	}
	second, err := resources.Save(ctx, app, owner.Id, "b.txt", "", []byte("second"))
	if err != nil {
		t.Fatal(err)
	}
	foreign, err := resources.Save(ctx, app, other.Id, "c.txt", "", []byte("third"))
	if err != nil {
		t.Fatal(err)
	}
	items, _ := json.Marshal(map[string]any{"items": []map[string]string{
		{"resourceId": first.ID, "name": "  2026 预算.txt "},
		{"resourceId": foreign.ID, "name": "stolen.txt"},
		{"resourceId": second.ID, "name": "   "},
	}})
	result := runBatchTool(t, newResourcesRenameTool(app, owner.Id), string(items))
	if result.OK || result.Succeeded != 1 || result.Failed != 2 ||
		!strings.Contains(result.Results[1].Error, "resource_not_found") || !strings.Contains(result.Results[2].Error, "invalid_input") {
		t.Fatalf("unexpected resource rename result: %#v", result)
	}
	stored, err := app.FindRecordById(resources.CollectionName, first.ID)
	if err != nil || stored.GetString("display_name") != "2026 预算.txt" || stored.GetString("original_name") != "a.txt" {
		t.Fatalf("only the display name must change: %v %v", stored, err)
	}
	if stored, _ := app.FindRecordById(resources.CollectionName, foreign.ID); stored.GetString("display_name") != "c.txt" {
		t.Fatal("another user's resource must not be renamed")
	}

	doc, err := workdocs.Create(ctx, app, owner.Id, workdocs.CreateInput{Title: "Draft", Content: "# Body"})
	if err != nil {
		t.Fatal(err)
	}
	stale, err := workdocs.Create(ctx, app, owner.Id, workdocs.CreateInput{Title: "Stale", Content: "kept"})
	if err != nil {
		t.Fatal(err)
	}
	foreignDoc, err := workdocs.Create(ctx, app, other.Id, workdocs.CreateInput{Title: "Private"})
	if err != nil {
		t.Fatal(err)
	}
	items, _ = json.Marshal(map[string]any{"items": []map[string]any{
		{"id": doc.ID, "title": "Final plan", "baseRevision": doc.Revision},
		{"id": stale.ID, "title": "Renamed", "baseRevision": stale.Revision + 1},
		{"id": foreignDoc.ID, "title": "Mine now", "baseRevision": foreignDoc.Revision},
	}})
	result = runBatchTool(t, newDocsRenameTool(app, owner.Id), string(items))
	if result.OK || result.Succeeded != 1 || result.Failed != 2 {
		t.Fatalf("unexpected document rename result: %#v", result)
	}
	renamed, err := workdocs.Get(ctx, app, owner.Id, doc.ID)
	if err != nil || renamed.Title != "Final plan" || renamed.Content != "# Body" || renamed.Revision != doc.Revision+1 {
		t.Fatalf("a rename must change only the title and bump the revision: %#v %v", renamed, err)
	}
	versions, err := workdocs.ListVersions(ctx, app, owner.Id, doc.ID)
	if err != nil || len(versions) != 2 {
		t.Fatalf("a rename must record a version: %d %v", len(versions), err)
	}
	if kept, _ := workdocs.Get(ctx, app, owner.Id, stale.ID); kept.Title != "Stale" {
		t.Fatal("a stale revision must not rename the document")
	}
}
