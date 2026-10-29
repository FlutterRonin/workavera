package configs

import (
	"testing"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/tests"
	"github.com/pocketbase/pocketbase/tools/types"

	_ "github.com/xusenlin/workavera/migrations"
)

func TestValidateSandboxValue(t *testing.T) {
	cases := []struct {
		key   string
		value string
		valid bool
	}{
		{SandboxEnabledKey, `false`, true},
		{SandboxEnabledKey, `"false"`, false},
		{SandboxNetworkKey, `true`, true},
		{SandboxNetworkKey, `1`, false},
		{SandboxImageKey, `"registry.example.com/sandbox:3"`, true},
		{SandboxImageKey, `""`, false},
		{SandboxImageKey, `"--privileged"`, false},
		{SandboxImageKey, `"two words"`, false},
		{SandboxLimitsKey, `{"memory": "512m", "cpus": 0.5, "pids": 64}`, true},
		{SandboxLimitsKey, `{"memory": "2G", "cpus": 4, "pids": 1024}`, true},
		{SandboxLimitsKey, `{"memory": "1g", "cpus": 0, "pids": 512}`, false},
		{SandboxLimitsKey, `{"memory": "255m", "cpus": 2, "pids": 512}`, false},
		{SandboxLimitsKey, `{"memory": "1024", "cpus": 2, "pids": 512}`, false},
		{SandboxLimitsKey, `{"memory": "1g", "cpus": 2, "pids": 63}`, false},
		{SandboxLimitsKey, `{"memory": "1g", "cpus": 2, "pids": 64.5}`, false},
		{SandboxLimitsKey, `{"memory": "1g", "cpus": 2}`, false},
		{SandboxIdleTimeoutKey, `0`, true},
		{SandboxIdleTimeoutKey, `5`, true},
		{SandboxIdleTimeoutKey, `4`, false},
		{SandboxIdleTimeoutKey, `-1`, false},
		{SandboxIdleTimeoutKey, `7.5`, false},
	}
	for _, test := range cases {
		err := ValidateSandboxValue(test.key, []byte(test.value))
		if (err == nil) != test.valid {
			t.Errorf("%s = %s: valid %v, got %v", test.key, test.value, test.valid, err)
		}
	}
}

func TestSandboxConfigValidationAndDefaults(t *testing.T) {
	app, err := tests.NewTestApp()
	if err != nil {
		t.Fatal(err)
	}
	defer app.Cleanup()
	Register(app)

	if config := Sandbox(app); config != DefaultSandboxConfig() {
		t.Fatalf("the migrated defaults must match DefaultSandboxConfig: %#v", config)
	}

	limits, err := app.FindFirstRecordByFilter(CollectionName, "key = {:key}", dbx.Params{"key": SandboxLimitsKey})
	if err != nil {
		t.Fatal(err)
	}
	limits.Set("value", types.JSONRaw(`{"memory": "1g", "cpus": 0, "pids": 512}`))
	if err := app.Save(limits); err == nil {
		t.Fatal("an invalid sandbox.limits must be rejected")
	}
	limits.Set("value", types.JSONRaw(`{"memory": "2g", "cpus": 1, "pids": 256}`))
	if err := app.Save(limits); err != nil {
		t.Fatalf("a valid sandbox.limits must be saved: %v", err)
	}
	if config := Sandbox(app); config.Limits != (SandboxLimits{Memory: "2g", CPUs: 1, PIDs: 256}) {
		t.Fatalf("unexpected limits: %#v", config.Limits)
	}

	image, err := app.FindFirstRecordByFilter(CollectionName, "key = {:key}", dbx.Params{"key": SandboxImageKey})
	if err != nil {
		t.Fatal(err)
	}
	if err := app.Delete(image); err != nil {
		t.Fatal(err)
	}
	if config := Sandbox(app); config.Image != DefaultSandboxImage {
		t.Fatalf("a deleted key must fall back to its default: %q", config.Image)
	}
}

func TestChatMaxSteps(t *testing.T) {
	app, err := tests.NewTestApp()
	if err != nil {
		t.Fatal(err)
	}
	defer app.Cleanup()
	Register(app)

	if steps := ChatMaxSteps(app); steps != DefaultChatMaxSteps {
		t.Fatalf("the migrated default must be %d, got %d", DefaultChatMaxSteps, steps)
	}
	record, err := app.FindFirstRecordByFilter(CollectionName, "key = {:key}", dbx.Params{"key": ChatMaxStepsKey})
	if err != nil {
		t.Fatal(err)
	}
	for _, invalid := range []string{`1`, `501`, `12.5`, `"80"`} {
		record.Set("value", types.JSONRaw(invalid))
		if err := app.Save(record); err == nil {
			t.Fatalf("chat.max_steps = %s must be rejected", invalid)
		}
	}
	record.Set("value", types.JSONRaw(`30`))
	if err := app.Save(record); err != nil {
		t.Fatal(err)
	}
	if steps := ChatMaxSteps(app); steps != 30 {
		t.Fatalf("expected 30, got %d", steps)
	}
	if err := app.Delete(record); err != nil {
		t.Fatal(err)
	}
	if steps := ChatMaxSteps(app); steps != DefaultChatMaxSteps {
		t.Fatalf("a deleted record must fall back to the default, got %d", steps)
	}
}
