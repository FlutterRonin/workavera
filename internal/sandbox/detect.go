package sandbox

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/xusenlin/workavera/internal/configs"
)

// Reasons the sandbox is unavailable on this instance.
const (
	ReasonDisabledByAdmin   = "disabled_by_admin"
	ReasonInContainer       = "in_container"
	ReasonDockerMissing     = "docker_missing"
	ReasonDaemonUnreachable = "daemon_unreachable"
	ReasonDaemonNotLinux    = "daemon_not_linux"
)

const (
	environmentTTL     = 30 * time.Second
	daemonCheckTimeout = 5 * time.Second
)

// Availability says whether the sandbox can be used on this instance and,
// when it cannot, why.
type Availability struct {
	Available bool   `json:"available"`
	Reason    string `json:"reason"`
	Message   string `json:"message"`
}

// Availability combines the administrator's switch, read fresh so a change
// applies at once, with the host checks, which are cached for 30 seconds so
// starting Docker later makes the sandbox available without a restart.
func (m *Manager) Availability(ctx context.Context) Availability {
	if !configs.Sandbox(m.app).Enabled {
		return Availability{Reason: ReasonDisabledByAdmin}
	}
	return m.environment(ctx)
}

func (m *Manager) environment(ctx context.Context) Availability {
	m.envMu.Lock()
	defer m.envMu.Unlock()
	if !m.envCheckedAt.IsZero() && time.Since(m.envCheckedAt) < environmentTTL {
		return m.env
	}
	m.env = m.detect(ctx)
	m.envCheckedAt = time.Now()
	return m.env
}

func (m *Manager) detect(ctx context.Context) Availability {
	if m.inContainer() {
		return Availability{Reason: ReasonInContainer}
	}
	if _, err := exec.LookPath(m.docker.binary); err != nil {
		return Availability{Reason: ReasonDockerMissing}
	}
	ctx, cancel := context.WithTimeout(ctx, daemonCheckTimeout)
	defer cancel()
	output, err := m.docker.run(ctx, nil, "version", "--format", "{{.Server.Os}}")
	if err != nil {
		message := err.Error()
		if commandErr, ok := err.(*commandError); ok {
			message = strings.TrimSpace(commandErr.Stderr)
		}
		return Availability{Reason: ReasonDaemonUnreachable, Message: truncate(message, maxErrorLength)}
	}
	if strings.TrimSpace(string(output)) != "linux" {
		return Availability{Reason: ReasonDaemonNotLinux}
	}
	return Availability{Available: true}
}

// runningInContainer is a best-effort check: the files container runtimes
// leave behind, or a container runtime named in the init process's cgroup.
func runningInContainer() bool {
	for _, marker := range []string{"/.dockerenv", "/run/.containerenv"} {
		if _, err := os.Stat(marker); err == nil {
			return true
		}
	}
	cgroup, err := os.ReadFile("/proc/1/cgroup")
	if err != nil {
		return false
	}
	for _, runtime := range []string{"docker", "containerd", "kubepods", "libpod"} {
		if bytes.Contains(cgroup, []byte(runtime)) {
			return true
		}
	}
	return false
}
