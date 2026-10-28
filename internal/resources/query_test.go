package resources

import (
	"context"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func TestListMatchesNamesBeforeContentForTheOwnerOnly(t *testing.T) {
	app := newTestApp(t)
	owner := createTestUser(t, app, "list-owner@example.com")
	other := createTestUser(t, app, "list-other@example.com")
	conv := newTestConverter(app, nil)

	byContent := uploadText(t, app, conv, owner.Id, "Notes.txt", "The ALPHA launch slipped a week.")
	time.Sleep(5 * time.Millisecond)
	byName := uploadText(t, app, conv, owner.Id, "Alpha plan.md", "Milestones only.")
	time.Sleep(5 * time.Millisecond)
	uploadText(t, app, conv, owner.Id, "Unrelated.txt", "Nothing to see. 100% literal_match")
	uploadText(t, app, conv, other.Id, "Alpha secrets.txt", "alpha")

	result, err := List(context.Background(), app, owner.Id, "  alpha ", 0)
	if err != nil {
		t.Fatal(err)
	}
	if result.Query != "alpha" || len(result.Resources) != 2 || result.Truncated {
		t.Fatalf("unexpected result: %#v", result)
	}
	if result.Resources[0].ID != byName.ID || result.Resources[0].MatchedIn != "name" {
		t.Fatalf("name matches must come first: %#v", result.Resources)
	}
	if result.Resources[1].ID != byContent.ID || result.Resources[1].MatchedIn != "content" {
		t.Fatalf("content match missing: %#v", result.Resources)
	}

	all, err := List(context.Background(), app, owner.Id, "", 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(all.Resources) != 2 || !all.Truncated || all.Resources[0].Name != "Unrelated.txt" || all.Resources[0].MatchedIn != "" {
		t.Fatalf("listing must be newest first and report truncation: %#v", all)
	}

	// LIKE wildcards in the query are matched literally.
	literal, err := List(context.Background(), app, owner.Id, "0% lit", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(literal.Resources) != 1 || literal.Resources[0].Name != "Unrelated.txt" {
		t.Fatalf("expected literal wildcard match: %#v", literal)
	}
	none, err := List(context.Background(), app, owner.Id, "a_pha", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(none.Resources) != 0 {
		t.Fatalf("underscore must not act as a wildcard: %#v", none)
	}
}

func TestSearchReturnsMergedCaseInsensitiveExcerptsWithCharacterOffsets(t *testing.T) {
	app := newTestApp(t)
	owner := createTestUser(t, app, "search-owner@example.com")
	other := createTestUser(t, app, "search-other@example.com")
	conv := newTestConverter(app, nil)

	content := "风险提示：" + strings.Repeat("x", 2000) + "Cash Flow risk here, and cash flow again." + strings.Repeat("y", 3000) + "CASH FLOW at the end"
	resource := uploadText(t, app, conv, owner.Id, "report.md", content)

	result, err := Search(context.Background(), app, owner.Id, resource.ID, "cash flow", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Matches) != 2 || result.Truncated || result.Notice == "" {
		t.Fatalf("nearby matches must merge into one excerpt: %#v", result)
	}
	first := strings.Index(content, "Cash Flow")
	if result.Matches[0].Offset != utf8.RuneCountInString(content[:first]) {
		t.Fatalf("offset must count characters, got %d", result.Matches[0].Offset)
	}
	for _, match := range result.Matches {
		if chars := utf8.RuneCountInString(strings.Trim(match.Excerpt, "…")); chars > maxExcerptChars {
			t.Fatalf("excerpt has %d characters", chars)
		}
	}
	if !strings.Contains(result.Matches[0].Excerpt, "cash flow again") || !strings.HasPrefix(result.Matches[0].Excerpt, "…") {
		t.Fatalf("unexpected first excerpt: %q", result.Matches[0].Excerpt)
	}
	if !strings.HasSuffix(result.Matches[1].Excerpt, "CASH FLOW at the end") {
		t.Fatalf("unexpected last excerpt: %q", result.Matches[1].Excerpt)
	}

	limited, err := Search(context.Background(), app, owner.Id, resource.ID, "cash flow", 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(limited.Matches) != 1 || !limited.Truncated {
		t.Fatalf("limit must truncate: %#v", limited)
	}
	chinese, err := Search(context.Background(), app, owner.Id, resource.ID, "风险", 0)
	if err != nil || len(chinese.Matches) != 1 || chinese.Matches[0].Offset != 0 {
		t.Fatalf("unexpected CJK search: %#v %v", chinese, err)
	}
	if _, err := Search(context.Background(), app, other.Id, resource.ID, "cash", 0); !IsNotFound(err) {
		t.Fatalf("another user must get not found, got %v", err)
	}
	if _, err := Search(context.Background(), app, owner.Id, resource.ID, "  ", 0); errorCode(err) != CodeInvalidInput {
		t.Fatalf("blank query must be rejected, got %v", err)
	}
}

func TestReadChunksWithBoundCursorsAndNeverSplitsCharacters(t *testing.T) {
	app := newTestApp(t)
	owner := createTestUser(t, app, "read-owner@example.com")
	other := createTestUser(t, app, "read-other@example.com")
	conv := newTestConverter(app, nil)

	content := strings.Repeat("资源é😀a", 60)
	resource := uploadText(t, app, conv, owner.Id, "long.txt", content)
	second := uploadText(t, app, conv, owner.Id, "second.txt", content+"!")

	whole, err := Read(context.Background(), app, owner.Id, resource.ID, "", 0)
	if err != nil {
		t.Fatal(err)
	}
	if whole.Content != content || whole.Truncated || whole.NextCursor != "" || whole.TotalChars != 300 {
		t.Fatalf("short content must be returned at once: %#v", whole)
	}

	var assembled strings.Builder
	cursor := ""
	calls := 0
	for {
		chunk, err := Read(context.Background(), app, owner.Id, resource.ID, cursor, 77)
		if err != nil {
			t.Fatal(err)
		}
		if !utf8.ValidString(chunk.Content) || chunk.ContentChars > 77 {
			t.Fatalf("invalid chunk: %#v", chunk)
		}
		assembled.WriteString(chunk.Content)
		calls++
		if !chunk.Truncated {
			break
		}
		cursor = chunk.NextCursor
	}
	if assembled.String() != content || calls != 4 {
		t.Fatalf("cursor reads must reassemble the content in 4 calls, got %d", calls)
	}

	first, err := Read(context.Background(), app, owner.Id, resource.ID, "", 10)
	if err != nil {
		t.Fatal(err)
	}
	tampered := []byte(first.NextCursor)
	tampered[0] ^= 1
	for name, bad := range map[string]string{"tampered": string(tampered), "garbage": "not-a-cursor"} {
		if _, err := Read(context.Background(), app, owner.Id, resource.ID, bad, 10); errorCode(err) != CodeInvalidResourceCursor {
			t.Fatalf("%s cursor must be rejected, got %v", name, err)
		}
	}
	if _, err := Read(context.Background(), app, owner.Id, second.ID, first.NextCursor, 10); errorCode(err) != CodeInvalidResourceCursor {
		t.Fatalf("a cursor from another resource must be rejected, got %v", err)
	}
	if _, err := Read(context.Background(), app, other.Id, resource.ID, "", 10); !IsNotFound(err) {
		t.Fatalf("another user must get not found, got %v", err)
	}
	if capped, err := Read(context.Background(), app, owner.Id, resource.ID, "", 1_000_000); err != nil || capped.ContentChars != 300 {
		t.Fatalf("maxChars above the cap must still work: %#v %v", capped, err)
	}
}

func TestSnapshotsKeepRequestedOrderAndRejectForeignIDs(t *testing.T) {
	app := newTestApp(t)
	owner := createTestUser(t, app, "snapshot-owner@example.com")
	other := createTestUser(t, app, "snapshot-other@example.com")
	conv := newTestConverter(app, nil)

	first := uploadText(t, app, conv, owner.Id, "one.txt", "one")
	second := uploadText(t, app, conv, owner.Id, "two.json", `{"two":2}`)
	foreign := uploadText(t, app, conv, other.Id, "three.txt", "three")

	snapshots, err := Snapshots(app, owner.Id, []string{second.ID, first.ID})
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshots) != 2 || snapshots[0].ResourceID != second.ID || snapshots[0].MediaType != "application/json" || snapshots[0].Size != 9 || snapshots[1].Name != "one.txt" {
		t.Fatalf("unexpected snapshots: %#v", snapshots)
	}
	if _, err := Snapshots(app, owner.Id, []string{first.ID, foreign.ID}); !IsNotFound(err) {
		t.Fatalf("a foreign resource must fail the whole call, got %v", err)
	}
}
