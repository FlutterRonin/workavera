package sandbox

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tests"
	"github.com/pocketbase/pocketbase/tools/types"

	"github.com/xusenlin/workavera/internal/configs"
	"github.com/xusenlin/workavera/internal/preferences"
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
	if err := app.Save(user); err != nil {
		t.Fatal(err)
	}
	if _, err := preferences.Ensure(app, user.Id); err != nil {
		t.Fatal(err)
	}
	return user
}

func errorCode(err error) string {
	var sandboxErr *Error
	if errors.As(err, &sandboxErr) {
		return sandboxErr.Code
	}
	return ""
}

func TestUnavailableInstanceRefusesTheSandbox(t *testing.T) {
	app := newTestApp(t)
	user := createTestUser(t, app, "sandbox-unavailable@example.com")
	if _, err := preferences.Set(app, user.Id, "sandbox_enabled", true); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	inContainer := newManager(app, newDocker())
	inContainer.inContainer = func() bool { return true }
	if availability := inContainer.Availability(ctx); availability.Available || availability.Reason != ReasonInContainer {
		t.Fatalf("unexpected availability: %#v", availability)
	}
	if err := inContainer.Enable(ctx, user.Id); errorCode(err) != CodeUnavailable {
		t.Fatalf("enabling in a container: expected %s, got %v", CodeUnavailable, err)
	}
	if inContainer.ToolsEnabled(ctx, user.Id) || inContainer.SystemPrompt(ctx, user.Id, "") != "" {
		t.Fatal("an unavailable instance must give no sandbox tools or prompt")
	}
	status, err := inContainer.Status(ctx, user.Id)
	if err != nil || status.Container.State != StateUnavailable || status.Reason != ReasonInContainer || status.Container.Name != "workavera-"+user.Id {
		t.Fatalf("unexpected status: %#v %v", status, err)
	}

	missing := newManager(app, &docker{binary: "workavera-no-such-docker"})
	missing.inContainer = func() bool { return false }
	if availability := missing.Availability(ctx); availability.Reason != ReasonDockerMissing {
		t.Fatalf("expected %s, got %#v", ReasonDockerMissing, availability)
	}

	record, err := app.FindFirstRecordByFilter(configs.CollectionName, "key = {:key}", dbx.Params{"key": configs.SandboxEnabledKey})
	if err != nil {
		t.Fatal(err)
	}
	record.Set("value", types.JSONRaw("false"))
	if err := app.Save(record); err != nil {
		t.Fatal(err)
	}
	if availability := missing.Availability(ctx); availability.Reason != ReasonDisabledByAdmin {
		t.Fatalf("the administrator's switch comes first: %#v", availability)
	}
}

func TestNamesOnlyComeFromValidUserIDs(t *testing.T) {
	app := newTestApp(t)
	m := newManager(app, newDocker())
	m.inContainer = func() bool { return true }
	for _, id := range []string{"", "../etc", "abc def", "ABC", "a;rm -rf /", strings.Repeat("a", 65)} {
		if _, err := m.Status(context.Background(), id); err == nil {
			t.Fatalf("user ID %q must be rejected", id)
		}
	}
	if containerName("abc123") != "workavera-abc123" || volumeName("abc123") != "workavera-abc123-workspace" {
		t.Fatal("unexpected sandbox names")
	}
}

func TestResolvePath(t *testing.T) {
	dir := ConversationDir("abc123")
	if dir != "/workspace/chats/abc123" || ConversationDir("") != "/workspace" || ConversationDir("../x") != "/workspace" {
		t.Fatalf("unexpected conversation folders: %q", dir)
	}
	cases := map[string]string{
		"report.csv":              dir + "/report.csv",
		"  out/chart.png ":        dir + "/out/chart.png",
		"../../shared/a.txt":      "/workspace/shared/a.txt",
		"/workspace/notes.md":     "/workspace/notes.md",
		"/tmp/.workavera/jobs/x/": "/tmp/.workavera/jobs/x",
	}
	for input, want := range cases {
		if got, err := resolvePath(dir, input); err != nil || got != want {
			t.Errorf("resolvePath(%q) = %q, %v; want %q", input, got, err, want)
		}
	}
	if _, err := resolvePath(dir, "  "); errorCode(err) != CodeInvalidInput {
		t.Fatalf("an empty path must be rejected, got %v", err)
	}
}

func TestSanitizeFileName(t *testing.T) {
	cases := map[string]string{
		"Q3/Q4 report.pdf": "Q3_Q4 report.pdf",
		"a\\b":             "a_b",
		"line\nbreak.md":   "linebreak.md",
		"  ..  ":           "untitled",
		"":                 "untitled",
		"‮gnp.exe":         "gnp.exe",
		"计划.md":            "计划.md",
	}
	for input, want := range cases {
		if got := sanitizeFileName(input); got != want {
			t.Errorf("sanitizeFileName(%q) = %q, want %q", input, got, want)
		}
	}
	long := sanitizeFileName(strings.Repeat("文", 100) + ".xlsx")
	if len(long) > maxFileNameBytes || !strings.HasSuffix(long, ".xlsx") || !strings.HasPrefix(long, "文") {
		t.Fatalf("a long name must be cut on a character boundary and keep its extension: %q", long)
	}
}

func TestTailText(t *testing.T) {
	if text, omitted := tailText([]byte("hello\nworld\n"), 12); text != "hello\nworld\n" || omitted != 0 {
		t.Fatalf("short output must be kept: %q %d", text, omitted)
	}

	var lines []string
	for index := 1; index <= 2500; index++ {
		lines = append(lines, fmt.Sprintf("line %d", index))
	}
	full := strings.Join(lines, "\n")
	text, omitted := tailText([]byte(full), int64(len(full)))
	kept := strings.Split(text, "\n")
	if len(kept) != maxOutputLines || kept[0] != "line 501" || kept[len(kept)-1] != "line 2500" {
		t.Fatalf("only the last 2000 lines must be kept: %q ... %q", kept[0], kept[len(kept)-1])
	}
	if omitted != int64(len(full)-len(text)) {
		t.Fatalf("omitted bytes: got %d, want %d", omitted, len(full)-len(text))
	}

	longLine := strings.Repeat("x", 3000)
	text, omitted = tailText([]byte(longLine), int64(len(longLine)))
	if !strings.HasSuffix(text, truncatedLineMark) || omitted != 1000 {
		t.Fatalf("a long line must be cut at 2000 characters: %d chars, omitted %d", len(text), omitted)
	}

	// A tail that starts inside a multi-byte character drops the fragment.
	chinese := []byte("中文输出")
	text, omitted = tailText(chinese[1:], int64(len(chinese)))
	if text != "文输出" || omitted != 3 {
		t.Fatalf("unexpected tail of a cut character: %q %d", text, omitted)
	}
}

func TestParseBashOutput(t *testing.T) {
	result, err := parseBashOutput([]byte("0 42 6 4\nhello\noops"))
	if err != nil || result.Status != "completed" || result.ExitCode == nil || *result.ExitCode != 0 || result.Stdout != "hello\n" || result.Stderr != "oops" {
		t.Fatalf("unexpected completed result: %#v %v", result, err)
	}
	result, err = parseBashOutput([]byte("- 77 3 0\nabc"))
	if err != nil || result.Status != "running" || result.ExitCode != nil || result.PID != 77 {
		t.Fatalf("unexpected running result: %#v %v", result, err)
	}
	big := fmt.Sprintf("1 9 %d 0\n%s", maxOutputBytes+10, strings.Repeat("y", maxOutputBytes))
	result, err = parseBashOutput([]byte(big))
	if err != nil || result.StdoutOmittedBytes != int64(10+maxOutputBytes-maxLineChars) || *result.ExitCode != 1 {
		t.Fatalf("unexpected truncated result: %d %v", result.StdoutOmittedBytes, err)
	}
	if _, err := parseBashOutput([]byte("garbage")); err == nil {
		t.Fatal("malformed output must fail")
	}
}

func TestLooksLikeText(t *testing.T) {
	if !looksLikeText([]byte("plain text\n"), false) || looksLikeText([]byte("bin\x00ary"), false) || looksLikeText([]byte{0xff, 0xfe, 0x41}, false) {
		t.Fatal("unexpected text detection")
	}
	cut := []byte("中文")[:4]
	if looksLikeText(cut, false) || !looksLikeText(cut, true) {
		t.Fatal("a sample cut inside a character is text only when it was cut")
	}
}
