package sandbox

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// docker runs the docker CLI with an argument list. It uses the CLI's
// default context and honors DOCKER_HOST and the other standard variables.
type docker struct {
	binary string
}

func newDocker() *docker {
	return &docker{binary: "docker"}
}

// commandError is a docker command that ran and exited non-zero. For
// docker exec the exit code is usually the one of the command in the
// container.
type commandError struct {
	Args     []string
	ExitCode int
	Stderr   string
}

func (e *commandError) Error() string {
	name := "docker"
	if len(e.Args) > 0 {
		name += " " + e.Args[0]
	}
	return fmt.Sprintf("%s exited with %d: %s", name, e.ExitCode, truncate(strings.TrimSpace(e.Stderr), maxErrorLength))
}

func exitCode(err error) int {
	var commandErr *commandError
	if errors.As(err, &commandErr) {
		return commandErr.ExitCode
	}
	return -1
}

func isNoSuch(err error) bool {
	var commandErr *commandError
	return errors.As(err, &commandErr) && strings.Contains(strings.ToLower(commandErr.Stderr), "no such")
}

func (d *docker) run(ctx context.Context, stdin io.Reader, args ...string) ([]byte, error) {
	var stdout, stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, d.binary, args...)
	cmd.Stdin = stdin
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	// A killed docker client can leave its pipes open through children;
	// never wait on them for long after the context ends.
	cmd.WaitDelay = 5 * time.Second
	err := cmd.Run()
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return stdout.Bytes(), ctxErr
		}
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return stdout.Bytes(), &commandError{Args: args, ExitCode: exitErr.ExitCode(), Stderr: stderr.String()}
		}
		return stdout.Bytes(), err
	}
	return stdout.Bytes(), nil
}

// script runs a bash script in the container as its default user, passing
// args as positional parameters so they are never parsed as shell code.
func (d *docker) script(ctx context.Context, container string, stdin io.Reader, script string, args ...string) ([]byte, error) {
	command := []string{"exec"}
	if stdin != nil {
		command = append(command, "-i")
	}
	command = append(command, container, "bash", "-c", script, "bash")
	return d.run(ctx, stdin, append(command, args...)...)
}

type containerInfo struct {
	Exists   bool
	Running  bool
	Image    string
	ImageID  string
	Created  time.Time
	Networks []string
}

func (d *docker) inspectContainer(ctx context.Context, name string) (containerInfo, error) {
	output, err := d.run(ctx, nil, "container", "inspect", "--format", "{{json .}}", name)
	if err != nil {
		if isNoSuch(err) {
			return containerInfo{}, nil
		}
		return containerInfo{}, err
	}
	var raw struct {
		Image   string    `json:"Image"`
		Created time.Time `json:"Created"`
		State   struct {
			Running bool `json:"Running"`
		} `json:"State"`
		Config struct {
			Image string `json:"Image"`
		} `json:"Config"`
		NetworkSettings struct {
			Networks map[string]json.RawMessage `json:"Networks"`
		} `json:"NetworkSettings"`
	}
	if err := json.Unmarshal(output, &raw); err != nil {
		return containerInfo{}, err
	}
	info := containerInfo{
		Exists:  true,
		Running: raw.State.Running,
		Image:   raw.Config.Image,
		ImageID: raw.Image,
		Created: raw.Created,
	}
	for network := range raw.NetworkSettings.Networks {
		info.Networks = append(info.Networks, network)
	}
	return info, nil
}

// imageLabel returns the image's ID and one of its labels. ok is false when
// the image is not on this host.
func (d *docker) imageLabel(ctx context.Context, image, label string) (id, value string, ok bool, err error) {
	output, err := d.run(ctx, nil, "image", "inspect", "--format", "{{json .Id}} {{json .Config.Labels}}", image)
	if err != nil {
		if isNoSuch(err) {
			return "", "", false, nil
		}
		return "", "", false, err
	}
	idPart, labelsPart, _ := strings.Cut(strings.TrimSpace(string(output)), " ")
	if err := json.Unmarshal([]byte(idPart), &id); err != nil {
		return "", "", false, err
	}
	var labels map[string]string
	if err := json.Unmarshal([]byte(labelsPart), &labels); err != nil {
		return "", "", false, err
	}
	return id, labels[label], true, nil
}

// sandboxContainer is one container carrying the sandbox label.
type sandboxContainer struct {
	Name    string
	UserID  string
	Running bool
}

func (d *docker) listSandboxes(ctx context.Context, all bool) ([]sandboxContainer, error) {
	args := []string{"ps", "--filter", "label=" + labelSandbox + "=1", "--format", `{{.Names}}	{{.Label "` + labelUser + `"}}	{{.State}}`}
	if all {
		args = append(args, "--all")
	}
	output, err := d.run(ctx, nil, args...)
	if err != nil {
		return nil, err
	}
	var containers []sandboxContainer
	for _, line := range strings.Split(strings.TrimSpace(string(output)), "\n") {
		fields := strings.Split(line, "\t")
		if len(fields) != 3 || validUserID(fields[1]) != nil || fields[0] != containerName(fields[1]) {
			continue
		}
		containers = append(containers, sandboxContainer{Name: fields[0], UserID: fields[1], Running: fields[2] == "running"})
	}
	return containers, nil
}

func cpusArg(cpus float64) string {
	return strconv.FormatFloat(cpus, 'f', -1, 64)
}
