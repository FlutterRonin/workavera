package sandbox

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/pocketbase/pocketbase/apis"
	"github.com/pocketbase/pocketbase/core"

	"github.com/xusenlin/workavera/internal/configs"
	"github.com/xusenlin/workavera/internal/preferences"
)

const backgroundTimeout = 5 * time.Minute

func (m *Manager) register() {
	m.app.OnServe().BindFunc(func(event *core.ServeEvent) error {
		// Detect once at startup without delaying it; reads re-detect on
		// demand once the cached result is 30 seconds old.
		go m.environment(context.Background())

		group := event.Router.Group("/api/sandbox").Bind(apis.RequireAuth("users"))
		group.GET("", m.getStatus)
		group.POST("/enable", m.handle(m.Enable))
		group.POST("/disable", m.handle(m.Disable))
		group.POST("/network", m.setNetwork)
		group.POST("/rebuild", m.handle(m.Rebuild))
		group.POST("/reset", m.handle(m.Reset))
		return event.Next()
	})

	if err := m.app.Cron().Add("sandboxIdleStop", "*/5 * * * *", m.stopIdle); err != nil {
		m.app.Logger().Error("could not schedule the sandbox idle check", "error", err)
	}

	onConfigChange := func(event *core.RecordEvent) error {
		if key := event.Record.GetString("key"); configs.IsSandboxKey(key) {
			go m.applyConfig(key)
		}
		return event.Next()
	}
	m.app.OnRecordAfterCreateSuccess(configs.CollectionName).BindFunc(onConfigChange)
	m.app.OnRecordAfterUpdateSuccess(configs.CollectionName).BindFunc(onConfigChange)
	m.app.OnRecordAfterDeleteSuccess(configs.CollectionName).BindFunc(onConfigChange)

	m.app.OnRecordAfterUpdateSuccess("users").BindFunc(func(event *core.RecordEvent) error {
		if event.Record.GetBool("deactivated") {
			go m.stopUser(event.Record.Id)
		}
		return event.Next()
	})
	m.app.OnRecordAfterDeleteSuccess("users").BindFunc(func(event *core.RecordEvent) error {
		go m.deleteUser(event.Record.Id)
		return event.Next()
	})
}

func (m *Manager) getStatus(event *core.RequestEvent) error {
	status, err := m.Status(event.Request.Context(), event.Auth.Id)
	if err != nil {
		return writeError(event, err)
	}
	return event.JSON(http.StatusOK, status)
}

// handle runs a lifecycle operation for the authenticated user and responds
// with the resulting status.
func (m *Manager) handle(operation func(context.Context, string) error) func(*core.RequestEvent) error {
	return func(event *core.RequestEvent) error {
		if err := operation(event.Request.Context(), event.Auth.Id); err != nil {
			return writeError(event, err)
		}
		return m.getStatus(event)
	}
}

type networkRequest struct {
	Online *bool `json:"online"`
}

func (m *Manager) setNetwork(event *core.RequestEvent) error {
	var request networkRequest
	if err := event.BindBody(&request); err != nil || request.Online == nil {
		return writeError(event, newError(CodeInvalidInput, http.StatusBadRequest, `The request body must be {"online": true|false}.`))
	}
	if err := m.SetOnline(event.Request.Context(), event.Auth.Id, *request.Online); err != nil {
		return writeError(event, err)
	}
	return m.getStatus(event)
}

// writeError responds with the stable code and safe message of a sandbox
// error. Anything else, which may carry Docker output, is logged and reported
// generically.
func writeError(event *core.RequestEvent, err error) error {
	var sandboxErr *Error
	if !errors.As(err, &sandboxErr) {
		event.App.Logger().Error("sandbox request failed", "userId", event.Auth.Id, "error", err)
		sandboxErr = newError("internal_error", http.StatusInternalServerError, "The sandbox request failed. Check the server log for details.")
	}
	return event.JSON(sandboxErr.Status, map[string]any{
		"status":  sandboxErr.Status,
		"code":    sandboxErr.Code,
		"message": sandboxErr.Message,
		"data":    map[string]any{},
	})
}

// stopIdle stops running sandboxes whose last activity is older than the
// idle timeout, unless a background command still runs in them.
func (m *Manager) stopIdle() {
	config := configs.Sandbox(m.app)
	if config.IdleTimeoutMinutes == 0 {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), backgroundTimeout)
	defer cancel()
	if !m.environment(ctx).Available {
		return
	}
	containers, err := m.docker.listSandboxes(ctx, false)
	if err != nil {
		m.app.Logger().Warn("sandbox idle check failed", "error", err)
		return
	}
	timeout := time.Duration(config.IdleTimeoutMinutes) * time.Minute
	for _, container := range containers {
		if time.Since(m.lastActivity(container.UserID)) < timeout {
			continue
		}
		if running, err := m.hasRunningJobs(ctx, container.Name); err != nil || running {
			continue
		}
		state := m.user(container.UserID)
		state.lock.Lock()
		if time.Since(m.lastActivity(container.UserID)) >= timeout {
			if _, err := m.docker.run(ctx, nil, "stop", container.Name); err != nil {
				m.app.Logger().Warn("sandbox idle stop failed", "userId", container.UserID, "error", err)
			} else {
				m.app.Logger().Info("sandbox stopped after idle timeout", "userId", container.UserID)
			}
		}
		state.lock.Unlock()
	}
}

// applyConfig applies an administrator's change of a sandbox key to the
// existing containers. Failures are only logged; the change itself is saved.
func (m *Manager) applyConfig(key string) {
	ctx, cancel := context.WithTimeout(context.Background(), backgroundTimeout)
	defer cancel()
	if !m.environment(ctx).Available {
		return
	}
	config := configs.Sandbox(m.app)
	switch key {
	case configs.SandboxEnabledKey:
		if config.Enabled {
			return
		}
		m.forEachSandbox(ctx, false, func(container sandboxContainer) error {
			_, err := m.docker.run(ctx, nil, "stop", container.Name)
			return err
		})
	case configs.SandboxNetworkKey:
		m.forEachSandbox(ctx, true, func(container sandboxContainer) error {
			preference, err := preferences.Ensure(m.app, container.UserID)
			if err != nil {
				return err
			}
			info, err := m.docker.inspectContainer(ctx, container.Name)
			if err != nil || !info.Exists {
				return err
			}
			return m.reconcileNetwork(ctx, container.Name, info.Networks, config.Network && !preference.SandboxOffline)
		})
	case configs.SandboxLimitsKey:
		m.forEachSandbox(ctx, true, func(container sandboxContainer) error {
			_, err := m.docker.run(ctx, nil, "update",
				"--memory", config.Limits.Memory,
				"--memory-swap", config.Limits.Memory,
				"--cpus", cpusArg(config.Limits.CPUs),
				"--pids-limit", strconv.Itoa(config.Limits.PIDs),
				container.Name,
			)
			return err
		})
	}
}

func (m *Manager) forEachSandbox(ctx context.Context, all bool, apply func(sandboxContainer) error) {
	containers, err := m.docker.listSandboxes(ctx, all)
	if err != nil {
		m.app.Logger().Warn("sandbox configuration change could not list containers", "error", err)
		return
	}
	for _, container := range containers {
		state := m.user(container.UserID)
		state.lock.Lock()
		err := apply(container)
		state.lock.Unlock()
		if err != nil {
			m.app.Logger().Warn("sandbox configuration change failed", "userId", container.UserID, "error", err)
		}
	}
}

// stopUser stops a deactivated user's container without removing it.
func (m *Manager) stopUser(userID string) {
	if validUserID(userID) != nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), backgroundTimeout)
	defer cancel()
	if !m.environment(ctx).Available {
		return
	}
	state := m.user(userID)
	state.lock.Lock()
	defer state.lock.Unlock()
	if _, err := m.docker.run(ctx, nil, "stop", containerName(userID)); err != nil && !isNoSuch(err) {
		m.app.Logger().Warn("sandbox stop for a deactivated user failed", "userId", userID, "error", err)
	}
}

// deleteUser removes a deleted user's container and workspace volume.
func (m *Manager) deleteUser(userID string) {
	if validUserID(userID) != nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), backgroundTimeout)
	defer cancel()
	if !m.environment(ctx).Available {
		return
	}
	state := m.user(userID)
	state.lock.Lock()
	err := m.removeContainer(ctx, userID)
	if err == nil {
		err = m.removeVolume(ctx, userID)
	}
	state.lock.Unlock()
	if err != nil {
		m.app.Logger().Warn("sandbox removal for a deleted user failed", "userId", userID, "error", err)
	}
	m.mu.Lock()
	delete(m.users, userID)
	m.mu.Unlock()
}
