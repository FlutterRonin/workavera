package sandbox

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/pocketbase/pocketbase/core"

	"github.com/xusenlin/workavera/internal/configs"
	"github.com/xusenlin/workavera/internal/preferences"
)

// Preparation phases reported while a container is being made ready.
const (
	PhasePullingImage = "pulling_image"
	PhaseCreating     = "creating"
	PhaseStarting     = "starting"
)

// Container states reported by Status.
const (
	StateUnavailable = "unavailable"
	StateDisabled    = "disabled"
	StatePreparing   = "preparing"
	StateRunning     = "running"
	StateStopped     = "stopped"
	StateError       = "error"
)

const (
	prepareTimeout = 30 * time.Minute
	readyWait      = 2 * time.Minute
	dockerTimeout  = 30 * time.Second
)

// Manager owns every sandbox container on the host. The containers
// themselves are the source of truth and are inspected on every read; only
// preparations in flight, the last preparation error, and the last activity
// live in memory.
type Manager struct {
	app         core.App
	docker      *docker
	startedAt   time.Time
	inContainer func() bool

	envMu        sync.Mutex
	env          Availability
	envCheckedAt time.Time

	networkMu sync.Mutex

	mu     sync.Mutex
	users  map[string]*userState
	pulls  map[string]*call
	labels map[string]string
}

type userState struct {
	// lock serializes the lifecycle operations of one user.
	lock sync.Mutex

	// Guarded by Manager.mu.
	prepare   *call
	phase     string
	lastError string
	activity  time.Time
}

// call is one operation in flight that later callers wait for.
type call struct {
	done chan struct{}
	err  error
}

func newManager(app core.App, d *docker) *Manager {
	return &Manager{
		app:         app,
		docker:      d,
		startedAt:   time.Now(),
		inContainer: runningInContainer,
		users:       map[string]*userState{},
		pulls:       map[string]*call{},
		labels:      map[string]string{},
	}
}

func (m *Manager) user(userID string) *userState {
	m.mu.Lock()
	defer m.mu.Unlock()
	state := m.users[userID]
	if state == nil {
		state = &userState{}
		m.users[userID] = state
	}
	return state
}

func (m *Manager) touch(userID string) {
	state := m.user(userID)
	m.mu.Lock()
	state.activity = time.Now()
	m.mu.Unlock()
}

// lastActivity falls back to the server start, so containers left running
// across a restart get a full idle period before they are stopped.
func (m *Manager) lastActivity(userID string) time.Time {
	state := m.user(userID)
	m.mu.Lock()
	defer m.mu.Unlock()
	if state.activity.IsZero() {
		return m.startedAt
	}
	return state.activity
}

func (m *Manager) setPhase(userID, phase string) {
	state := m.user(userID)
	m.mu.Lock()
	state.phase = phase
	m.mu.Unlock()
}

func (m *Manager) clearError(userID string) {
	state := m.user(userID)
	m.mu.Lock()
	state.lastError = ""
	m.mu.Unlock()
}

// requireReady checks what every operation on a user's sandbox needs: an
// available instance and the user's own switch turned on.
func (m *Manager) requireReady(ctx context.Context, userID string) (preferences.Preferences, error) {
	if err := validUserID(userID); err != nil {
		return preferences.Preferences{}, err
	}
	if availability := m.Availability(ctx); !availability.Available {
		return preferences.Preferences{}, unavailableError(availability)
	}
	preference, err := preferences.Ensure(m.app, userID)
	if err != nil {
		return preferences.Preferences{}, err
	}
	if !preference.SandboxEnabled {
		return preference, errDisabled
	}
	return preference, nil
}

func unavailableError(availability Availability) *Error {
	message := "The sandbox is not available on this server."
	switch availability.Reason {
	case ReasonDisabledByAdmin:
		message = "An administrator has turned the sandbox off."
	case ReasonInContainer:
		message = "Workavera runs in a container, where the sandbox is not supported."
	case ReasonDockerMissing:
		message = "Docker is not installed on the server."
	case ReasonDaemonUnreachable:
		message = "The server cannot reach Docker."
	case ReasonDaemonNotLinux:
		message = "Docker on the server does not run Linux containers."
	}
	return newError(CodeUnavailable, http.StatusServiceUnavailable, message+" Do not retry.")
}

// Ready makes sure the user's container runs, creating or starting it when
// needed, and returns its name. It waits at most two minutes for a
// preparation, which keeps going in the background afterwards.
func (m *Manager) Ready(ctx context.Context, userID string) (string, error) {
	if _, err := m.requireReady(ctx, userID); err != nil {
		return "", err
	}
	m.touch(userID)
	name := containerName(userID)
	inspectCtx, cancel := context.WithTimeout(ctx, dockerTimeout)
	info, err := m.docker.inspectContainer(inspectCtx, name)
	cancel()
	if err == nil && info.Running {
		return name, nil
	}

	prepare := m.startPrepare(userID)
	timer := time.NewTimer(readyWait)
	defer timer.Stop()
	select {
	case <-prepare.done:
	case <-timer.C:
		return "", newError(CodePreparing, http.StatusAccepted, "The sandbox is still being prepared. Tell the user to try again in a moment.")
	case <-ctx.Done():
		return "", ctx.Err()
	}
	if prepare.err != nil {
		var sandboxErr *Error
		if errors.As(prepare.err, &sandboxErr) {
			return "", sandboxErr
		}
		return "", wrapError(CodeStartFailed, http.StatusBadGateway, "The sandbox could not be started. The user can retry or rebuild it in Settings.", prepare.err)
	}
	return name, nil
}

// startPrepare joins the preparation in flight for the user or starts one in
// the background.
func (m *Manager) startPrepare(userID string) *call {
	state := m.user(userID)
	m.mu.Lock()
	if state.prepare != nil {
		prepare := state.prepare
		m.mu.Unlock()
		return prepare
	}
	prepare := &call{done: make(chan struct{})}
	state.prepare = prepare
	state.phase = PhaseCreating
	state.lastError = ""
	m.mu.Unlock()

	go func() {
		err := m.prepare(userID)
		m.mu.Lock()
		state.prepare = nil
		state.phase = ""
		if err != nil && !errors.Is(err, errDisabled) {
			state.lastError = truncate(err.Error(), maxErrorLength)
		}
		m.mu.Unlock()
		if err != nil && !errors.Is(err, errDisabled) {
			m.app.Logger().Warn("sandbox preparation failed", "userId", userID, "error", err)
		}
		prepare.err = err
		close(prepare.done)
	}()
	return prepare
}

func (m *Manager) prepare(userID string) error {
	ctx, cancel := context.WithTimeout(context.Background(), prepareTimeout)
	defer cancel()
	if _, err := m.requireReady(ctx, userID); err != nil {
		return err
	}
	config := configs.Sandbox(m.app)
	name := containerName(userID)

	// The image is pulled before taking the user's lock, so turning the
	// sandbox off never waits for a download.
	info, err := m.docker.inspectContainer(ctx, name)
	if err != nil {
		return err
	}
	if !info.Exists {
		if err := m.ensureImage(ctx, config.Image, func() { m.setPhase(userID, PhasePullingImage) }); err != nil {
			return err
		}
	}

	state := m.user(userID)
	state.lock.Lock()
	defer state.lock.Unlock()
	// The user may have turned the sandbox off while the image was pulled.
	preference, err := m.requireReady(ctx, userID)
	if err != nil {
		return err
	}
	online := config.Network && !preference.SandboxOffline
	info, err = m.docker.inspectContainer(ctx, name)
	if err != nil {
		return err
	}
	if !info.Exists {
		m.setPhase(userID, PhaseCreating)
		err = m.create(ctx, userID, config, online)
	} else {
		m.setPhase(userID, PhaseStarting)
		if err = m.reconcileNetwork(ctx, name, info.Networks, online); err == nil && !info.Running {
			_, err = m.docker.run(ctx, nil, "start", name)
		}
	}
	if err != nil {
		return err
	}
	m.touch(userID)
	return nil
}

func (m *Manager) create(ctx context.Context, userID string, config configs.SandboxConfig, online bool) error {
	network := "none"
	if online {
		if err := m.ensureNetwork(ctx); err != nil {
			return err
		}
		network = networkName
	}
	_, err := m.docker.run(ctx, nil,
		"run", "-d",
		"--name", containerName(userID),
		"--hostname", "workavera-sandbox",
		"--label", labelSandbox+"=1",
		"--label", labelUser+"="+userID,
		"--mount", "type=volume,src="+volumeName(userID)+",dst="+workspaceDir,
		"--network", network,
		"--memory", config.Limits.Memory,
		"--memory-swap", config.Limits.Memory,
		"--cpus", cpusArg(config.Limits.CPUs),
		"--pids-limit", strconv.Itoa(config.Limits.PIDs),
		"--init",
		"--user", "workavera",
		"--workdir", workspaceDir,
		"--entrypoint", "/bin/sh",
		config.Image, "-c", "sleep 2147483647",
	)
	return err
}

// ensureImage pulls the image when it is not on the host. Concurrent pulls
// of the same reference share one download.
func (m *Manager) ensureImage(ctx context.Context, image string, onPull func()) error {
	if _, _, ok, err := m.docker.imageLabel(ctx, image, toolsLabel); err != nil || ok {
		return err
	}
	onPull()
	m.mu.Lock()
	pull := m.pulls[image]
	if pull == nil {
		pull = &call{done: make(chan struct{})}
		m.pulls[image] = pull
		go func() {
			pullCtx, cancel := context.WithTimeout(context.Background(), prepareTimeout)
			defer cancel()
			_, pull.err = m.docker.run(pullCtx, nil, "pull", "--quiet", image)
			m.mu.Lock()
			delete(m.pulls, image)
			m.mu.Unlock()
			close(pull.done)
		}()
	}
	m.mu.Unlock()
	select {
	case <-pull.done:
		return pull.err
	case <-ctx.Done():
		return ctx.Err()
	}
}

// ensureNetwork creates the sandbox network on first use. Inter-container
// communication is off, so sandboxes on it cannot reach each other.
func (m *Manager) ensureNetwork(ctx context.Context) error {
	m.networkMu.Lock()
	defer m.networkMu.Unlock()
	if _, err := m.docker.run(ctx, nil, "network", "inspect", "--format", "{{.Id}}", networkName); err == nil {
		return nil
	}
	_, err := m.docker.run(ctx, nil,
		"network", "create",
		"--driver", "bridge",
		"--opt", "com.docker.network.bridge.enable_icc=false",
		"--label", labelSandbox+"=1",
		networkName,
	)
	if err != nil && strings.Contains(err.Error(), "already exists") {
		return nil
	}
	return err
}

// reconcileNetwork connects the container to the sandbox network or takes
// it offline. A container is either on "none", on the sandbox network, or on
// no network at all, which is offline too.
func (m *Manager) reconcileNetwork(ctx context.Context, name string, networks []string, online bool) error {
	connected := slices.Contains(networks, networkName)
	if online == connected {
		return nil
	}
	if !online {
		_, err := m.docker.run(ctx, nil, "network", "disconnect", networkName, name)
		return err
	}
	if err := m.ensureNetwork(ctx); err != nil {
		return err
	}
	if slices.Contains(networks, "none") {
		if _, err := m.docker.run(ctx, nil, "network", "disconnect", "none", name); err != nil {
			return err
		}
	}
	_, err := m.docker.run(ctx, nil, "network", "connect", networkName, name)
	return err
}

// removeContainer deletes the user's container, ending whatever runs in it.
// The workspace volume stays.
func (m *Manager) removeContainer(ctx context.Context, userID string) error {
	if _, err := m.docker.run(ctx, nil, "rm", "--force", containerName(userID)); err != nil && !isNoSuch(err) {
		return err
	}
	return nil
}

func (m *Manager) removeVolume(ctx context.Context, userID string) error {
	if _, err := m.docker.run(ctx, nil, "volume", "rm", "--force", volumeName(userID)); err != nil && !isNoSuch(err) {
		return err
	}
	return nil
}

// Enable turns the user's sandbox on and starts preparing it in the
// background.
func (m *Manager) Enable(ctx context.Context, userID string) error {
	if err := validUserID(userID); err != nil {
		return err
	}
	if availability := m.Availability(ctx); !availability.Available {
		return unavailableError(availability)
	}
	if _, err := preferences.Set(m.app, userID, "sandbox_enabled", true); err != nil {
		return err
	}
	m.touch(userID)
	m.startPrepare(userID)
	return nil
}

// Disable turns the user's sandbox off and removes the container. The
// workspace volume stays, so turning it on again finds the files.
func (m *Manager) Disable(ctx context.Context, userID string) error {
	if err := validUserID(userID); err != nil {
		return err
	}
	state := m.user(userID)
	state.lock.Lock()
	defer state.lock.Unlock()
	if _, err := preferences.Set(m.app, userID, "sandbox_enabled", false); err != nil {
		return err
	}
	m.clearError(userID)
	if !m.environment(ctx).Available {
		return nil
	}
	return m.removeContainer(ctx, userID)
}

// SetOnline saves the user's network choice and applies it to an existing
// container right away, without recreating it.
func (m *Manager) SetOnline(ctx context.Context, userID string, online bool) error {
	if err := validUserID(userID); err != nil {
		return err
	}
	config := configs.Sandbox(m.app)
	if !config.Network {
		return newError(CodeNetworkDisabled, http.StatusForbidden, "An administrator has turned off sandbox network access.")
	}
	state := m.user(userID)
	state.lock.Lock()
	defer state.lock.Unlock()
	preference, err := preferences.Set(m.app, userID, "sandbox_offline", !online)
	if err != nil {
		return err
	}
	if !preference.SandboxEnabled || !m.Availability(ctx).Available {
		return nil
	}
	m.touch(userID)
	name := containerName(userID)
	info, err := m.docker.inspectContainer(ctx, name)
	if err != nil || !info.Exists {
		return err
	}
	return m.reconcileNetwork(ctx, name, info.Networks, online)
}

// Rebuild recreates the container from the current image and limits,
// keeping the workspace volume.
func (m *Manager) Rebuild(ctx context.Context, userID string) error {
	if _, err := m.requireReady(ctx, userID); err != nil {
		return err
	}
	state := m.user(userID)
	state.lock.Lock()
	err := m.removeContainer(ctx, userID)
	state.lock.Unlock()
	if err != nil {
		return err
	}
	m.clearError(userID)
	m.touch(userID)
	m.startPrepare(userID)
	return nil
}

// Reset deletes the container and the workspace volume, then recreates the
// container when the sandbox is on. The files cannot be recovered.
func (m *Manager) Reset(ctx context.Context, userID string) error {
	if err := validUserID(userID); err != nil {
		return err
	}
	if availability := m.Availability(ctx); !availability.Available {
		return unavailableError(availability)
	}
	state := m.user(userID)
	state.lock.Lock()
	err := m.removeContainer(ctx, userID)
	if err == nil {
		err = m.removeVolume(ctx, userID)
	}
	state.lock.Unlock()
	if err != nil {
		return err
	}
	m.clearError(userID)
	preference, err := preferences.Ensure(m.app, userID)
	if err != nil {
		return err
	}
	if preference.SandboxEnabled {
		m.touch(userID)
		m.startPrepare(userID)
	}
	return nil
}

// ContainerStatus describes the user's container.
type ContainerStatus struct {
	Name      string `json:"name"`
	State     string `json:"state"`
	Phase     string `json:"phase"`
	Image     string `json:"image"`
	CreatedAt string `json:"createdAt"`
	Error     string `json:"error"`
}

// Status is everything Settings shows about the user's sandbox.
type Status struct {
	Availability
	Enabled            bool                  `json:"enabled"`
	Online             bool                  `json:"online"`
	NetworkAllowed     bool                  `json:"networkAllowed"`
	ConfiguredImage    string                `json:"configuredImage"`
	Limits             configs.SandboxLimits `json:"limits"`
	IdleTimeoutMinutes int                   `json:"idleTimeoutMinutes"`
	Container          ContainerStatus       `json:"container"`
}

func (m *Manager) Status(ctx context.Context, userID string) (Status, error) {
	if err := validUserID(userID); err != nil {
		return Status{}, err
	}
	preference, err := preferences.Ensure(m.app, userID)
	if err != nil {
		return Status{}, err
	}
	config := configs.Sandbox(m.app)
	status := Status{
		Availability:       m.Availability(ctx),
		Enabled:            preference.SandboxEnabled,
		Online:             !preference.SandboxOffline,
		NetworkAllowed:     config.Network,
		ConfiguredImage:    config.Image,
		Limits:             config.Limits,
		IdleTimeoutMinutes: config.IdleTimeoutMinutes,
		Container:          ContainerStatus{Name: containerName(userID)},
	}
	switch {
	case !status.Available:
		status.Container.State = StateUnavailable
		return status, nil
	case !status.Enabled:
		status.Container.State = StateDisabled
		return status, nil
	}

	state := m.user(userID)
	m.mu.Lock()
	preparing, phase, lastError := state.prepare != nil, state.phase, state.lastError
	m.mu.Unlock()

	info, err := m.docker.inspectContainer(ctx, containerName(userID))
	if err != nil {
		return Status{}, err
	}
	if info.Exists {
		status.Container.Image = info.Image
		status.Container.CreatedAt = info.Created.UTC().Format(time.RFC3339)
	}
	switch {
	case preparing:
		status.Container.State = StatePreparing
		status.Container.Phase = phase
	case lastError != "":
		status.Container.State = StateError
		status.Container.Error = lastError
	case info.Running:
		status.Container.State = StateRunning
	default:
		status.Container.State = StateStopped
	}
	return status, nil
}
