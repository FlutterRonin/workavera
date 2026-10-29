package migrations

import (
	"encoding/json"
	"strings"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
	m "github.com/pocketbase/pocketbase/migrations"
	"github.com/pocketbase/pocketbase/tools/types"
)

// sandboxPreferenceGuard keeps the sandbox switches out of the Records API:
// turning them on or off creates, removes, or reconnects a container, which
// only the sandbox API can do.
const sandboxPreferenceGuard = ` && @request.body.sandbox_enabled:changed = false && @request.body.sandbox_offline:changed = false`

var sandboxConfigDefaults = []struct {
	key         string
	value       any
	description string
}{
	{"sandbox.enabled", true, "Sandbox master switch. When false, no user can use a sandbox and every sandbox container is stopped."},
	{"sandbox.image", "ghcr.io/xusenlin/workavera-sandbox:2", "Image used when a sandbox container is created or rebuilt. Existing containers keep their image until the user rebuilds them."},
	{"sandbox.network", true, "Sandbox network master switch. When false, every sandbox is offline and users cannot turn their network on."},
	{"sandbox.limits", map[string]any{"memory": "1g", "cpus": 2, "pids": 512}, `Per-sandbox limits: memory with a k, m, or g unit (at least 256m), cpus greater than 0, and pids of at least 64. Applied to existing containers at once.`},
	{"sandbox.idle_timeout_minutes", 60, "Minutes without activity before a sandbox container is stopped; 0 never stops it. Otherwise at least 5."},
}

func init() {
	m.Register(addSandbox, dropSandbox)
}

func addSandbox(app core.App) error {
	configs, err := app.FindCollectionByNameOrId(configsCollection)
	if err != nil {
		return err
	}
	for _, item := range sandboxConfigDefaults {
		if existing, _ := app.FindFirstRecordByFilter(configsCollection, "key = {:key}", dbx.Params{"key": item.key}); existing != nil {
			continue
		}
		value, err := json.Marshal(item.value)
		if err != nil {
			return err
		}
		record := core.NewRecord(configs)
		record.Set("key", item.key)
		record.Set("value", types.JSONRaw(value))
		record.Set("description", item.description)
		if err := app.Save(record); err != nil {
			return err
		}
	}

	preferences, err := app.FindCollectionByNameOrId(userPreferencesCollection)
	if err != nil {
		return err
	}
	preferences.Fields.Add(
		&core.BoolField{Name: "sandbox_enabled"},
		&core.BoolField{Name: "sandbox_offline"},
	)
	if preferences.UpdateRule != nil {
		preferences.UpdateRule = types.Pointer(*preferences.UpdateRule + sandboxPreferenceGuard)
	}
	return app.Save(preferences)
}

func dropSandbox(app core.App) error {
	preferences, err := app.FindCollectionByNameOrId(userPreferencesCollection)
	if err != nil {
		return err
	}
	preferences.Fields.RemoveByName("sandbox_enabled")
	preferences.Fields.RemoveByName("sandbox_offline")
	if preferences.UpdateRule != nil {
		preferences.UpdateRule = types.Pointer(strings.TrimSuffix(*preferences.UpdateRule, sandboxPreferenceGuard))
	}
	if err := app.Save(preferences); err != nil {
		return err
	}

	for _, item := range sandboxConfigDefaults {
		record, err := app.FindFirstRecordByFilter(configsCollection, "key = {:key}", dbx.Params{"key": item.key})
		if err != nil {
			continue
		}
		if err := app.Delete(record); err != nil {
			return err
		}
	}
	return nil
}
