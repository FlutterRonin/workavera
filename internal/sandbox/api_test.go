package sandbox

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/pocketbase/pocketbase/apis"
	"github.com/pocketbase/pocketbase/core"
)

func TestAPIInContainer(t *testing.T) {
	app := newTestApp(t)
	user := createTestUser(t, app, "sandbox-api@example.com")
	token, err := user.NewAuthToken()
	if err != nil {
		t.Fatal(err)
	}
	m := newManager(app, newDocker())
	m.inContainer = func() bool { return true }
	m.register()
	router, err := apis.NewRouter(app)
	if err != nil {
		t.Fatal(err)
	}
	if err := app.OnServe().Trigger(&core.ServeEvent{App: app, Router: router}, func(event *core.ServeEvent) error { return event.Next() }); err != nil {
		t.Fatal(err)
	}
	handler, err := router.BuildMux()
	if err != nil {
		t.Fatal(err)
	}
	request := func(method, path, token, body string) (int, map[string]any) {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		if token != "" {
			req.Header.Set("Authorization", token)
		}
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, req)
		var decoded map[string]any
		_ = json.Unmarshal(response.Body.Bytes(), &decoded)
		return response.Code, decoded
	}

	if code, _ := request(http.MethodGet, "/api/sandbox", "", ""); code != http.StatusUnauthorized {
		t.Fatalf("the sandbox API needs authentication, got %d", code)
	}
	code, status := request(http.MethodGet, "/api/sandbox", token, "")
	container, _ := status["container"].(map[string]any)
	if code != http.StatusOK || status["available"] != false || status["reason"] != ReasonInContainer || container["state"] != StateUnavailable || container["name"] != "workavera-"+user.Id {
		t.Fatalf("unexpected status: %d %v", code, status)
	}
	if code, body := request(http.MethodPost, "/api/sandbox/enable", token, ""); code != http.StatusServiceUnavailable || body["code"] != CodeUnavailable {
		t.Fatalf("enabling in a container must fail: %d %v", code, body)
	}
	if code, body := request(http.MethodPost, "/api/sandbox/network", token, `{}`); code != http.StatusBadRequest || body["code"] != CodeInvalidInput {
		t.Fatalf("the network request needs online: %d %v", code, body)
	}
	if code, body := request(http.MethodPost, "/api/sandbox/disable", token, ""); code != http.StatusOK || body["enabled"] != false {
		t.Fatalf("disabling is always allowed: %d %v", code, body)
	}
}
