package migrations

import (
	"testing"

	"github.com/pocketbase/pocketbase/tests"
)

func TestChatMaxStepsMigration(t *testing.T) {
	app, err := tests.NewTestApp()
	if err != nil {
		t.Fatal(err)
	}
	defer app.Cleanup()

	record, err := app.FindFirstRecordByFilter(configsCollection, `key = "chat.max_steps"`)
	if err != nil {
		t.Fatalf("missing chat.max_steps: %v", err)
	}
	if record.GetString("value") != "80" || record.GetString("description") == "" {
		t.Fatalf("unexpected chat.max_steps record: %q %q", record.GetString("value"), record.GetString("description"))
	}

	if err := dropChatMaxSteps(app); err != nil {
		t.Fatal(err)
	}
	if _, err := app.FindFirstRecordByFilter(configsCollection, `key = "chat.max_steps"`); err == nil {
		t.Fatal("dropping must remove chat.max_steps")
	}
}
