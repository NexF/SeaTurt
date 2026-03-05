package container

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/client"
	"github.com/docker/docker/pkg/stdcopy"
)

const labelPrefix = "seaturt"

// Manager wraps the Docker client and provides container lifecycle operations.
type Manager struct {
	cli *client.Client
}

// NewManager creates a new Docker manager using the given host URL.
// Pass empty string to use the default Docker socket.
func NewManager(host string) (*Manager, error) {
	var opts []client.Opt
	opts = append(opts, client.FromEnv, client.WithAPIVersionNegotiation())
	if host != "" {
		opts = append(opts, client.WithHost(host))
	}

	cli, err := client.NewClientWithOpts(opts...)
	if err != nil {
		return nil, fmt.Errorf("create docker client: %w", err)
	}

	// Verify connectivity
	ctx := context.Background()
	ping, err := cli.Ping(ctx)
	if err != nil {
		cli.Close()
		return nil, fmt.Errorf("ping docker: %w", err)
	}
	slog.Info("docker connected", "api_version", ping.APIVersion)

	return &Manager{cli: cli}, nil
}

// Close releases the Docker client resources.
func (m *Manager) Close() error {
	return m.cli.Close()
}

// CreateContainerOpts specifies options for creating a sandbox container.
type CreateContainerOpts struct {
	AgentID       string
	Image         string
	WorkspacePath string // host path to mount as /workspace
	ExtraMounts   []string
	EnvVars       map[string]string
	Desktop       bool   // whether to enable desktop environment
	Resolution    string // desktop resolution, e.g. "1920x1080x24"
	ShmSize       int64  // /dev/shm size in bytes (0 = Docker default)
}

// CreateContainer creates and starts a sandbox container for the given agent.
// Returns the container ID.
func (m *Manager) CreateContainer(ctx context.Context, opts CreateContainerOpts) (string, error) {
	// Build env vars — inject host UID/GID for entrypoint user mapping
	envList := []string{
		fmt.Sprintf("HOST_UID=%d", os.Getuid()),
		fmt.Sprintf("HOST_GID=%d", os.Getgid()),
	}
	for k, v := range opts.EnvVars {
		envList = append(envList, fmt.Sprintf("%s=%s", k, v))
	}

	// Build mounts: workspace + extra
	binds := []string{
		fmt.Sprintf("%s:/workspace", opts.WorkspacePath),
	}
	binds = append(binds, opts.ExtraMounts...)

	containerCfg := &container.Config{
		Image: opts.Image,
		Labels: map[string]string{
			labelPrefix + ".agent_id": opts.AgentID,
			labelPrefix + ".managed": "true",
		},
		Env: envList,
	}

	hostCfg := &container.HostConfig{
		Binds: binds,
		// Security: read-only root filesystem with /workspace writable via bind mount
		// ReadonlyRootfs is disabled for now — MCP servers may need /tmp etc.
	}

	name := fmt.Sprintf("seaturt-%s", opts.AgentID)

	resp, err := m.cli.ContainerCreate(ctx, containerCfg, hostCfg, nil, nil, name)
	if err != nil {
		return "", fmt.Errorf("create container: %w", err)
	}

	slog.Info("container created", "container_id", resp.ID[:12], "agent_id", opts.AgentID)
	return resp.ID, nil
}

// StartContainer starts a stopped container.
func (m *Manager) StartContainer(ctx context.Context, containerID string) error {
	if err := m.cli.ContainerStart(ctx, containerID, container.StartOptions{}); err != nil {
		return fmt.Errorf("start container: %w", err)
	}
	slog.Info("container started", "container_id", containerID[:12])
	return nil
}

// StopContainer stops a running container.
func (m *Manager) StopContainer(ctx context.Context, containerID string) error {
	if err := m.cli.ContainerStop(ctx, containerID, container.StopOptions{}); err != nil {
		return fmt.Errorf("stop container: %w", err)
	}
	slog.Info("container stopped", "container_id", containerID[:12])
	return nil
}

// RemoveContainer force-removes a container.
func (m *Manager) RemoveContainer(ctx context.Context, containerID string) error {
	if err := m.cli.ContainerRemove(ctx, containerID, container.RemoveOptions{Force: true}); err != nil {
		return fmt.Errorf("remove container: %w", err)
	}
	slog.Info("container removed", "container_id", containerID[:12])
	return nil
}

// ContainerStatus represents the state of a container.
type ContainerStatus struct {
	ID      string
	Running bool
	Status  string // e.g. "running", "exited", "created"
}

// InspectContainer returns the current status of a container.
func (m *Manager) InspectContainer(ctx context.Context, containerID string) (*ContainerStatus, error) {
	info, err := m.cli.ContainerInspect(ctx, containerID)
	if err != nil {
		return nil, fmt.Errorf("inspect container: %w", err)
	}
	return &ContainerStatus{
		ID:      info.ID,
		Running: info.State.Running,
		Status:  info.State.Status,
	}, nil
}

// GetMappedPorts returns the actual host port mappings for a container.
// The returned map keys are container port numbers (e.g. "22", "80"),
// and values are the corresponding host ports (e.g. "32768", "32769").
func (m *Manager) GetMappedPorts(ctx context.Context, containerID string) (map[string]string, error) {
	info, err := m.cli.ContainerInspect(ctx, containerID)
	if err != nil {
		return nil, fmt.Errorf("inspect container for ports: %w", err)
	}
	result := make(map[string]string)
	for port, bindings := range info.NetworkSettings.Ports {
		if len(bindings) > 0 && bindings[0].HostPort != "" {
			result[port.Port()] = bindings[0].HostPort
		}
	}
	return result, nil
}

// ListContainers returns all containers managed by SeaTurt.
func (m *Manager) ListContainers(ctx context.Context) ([]ContainerStatus, error) {
	f := filters.NewArgs()
	f.Add("label", labelPrefix+".managed=true")

	containers, err := m.cli.ContainerList(ctx, container.ListOptions{
		All:     true,
		Filters: f,
	})
	if err != nil {
		return nil, fmt.Errorf("list containers: %w", err)
	}

	var result []ContainerStatus
	for _, c := range containers {
		result = append(result, ContainerStatus{
			ID:      c.ID,
			Running: strings.EqualFold(c.State, "running"),
			Status:  c.State,
		})
	}
	return result, nil
}

// ExecResult holds the output from a docker exec command.
type ExecResult struct {
	Stdout   string
	Stderr   string
	ExitCode int
}

// Exec runs a command inside a container and returns the combined output.
// This is used for one-shot commands, NOT for MCP stdio sessions.
func (m *Manager) Exec(ctx context.Context, containerID string, cmd []string) (*ExecResult, error) {
	execCfg := container.ExecOptions{
		Cmd:          cmd,
		AttachStdout: true,
		AttachStderr: true,
	}

	resp, err := m.cli.ContainerExecCreate(ctx, containerID, execCfg)
	if err != nil {
		return nil, fmt.Errorf("exec create: %w", err)
	}

	attach, err := m.cli.ContainerExecAttach(ctx, resp.ID, container.ExecAttachOptions{})
	if err != nil {
		return nil, fmt.Errorf("exec attach: %w", err)
	}
	defer attach.Close()

	var stdout, stderr bytes.Buffer
	if _, err := stdcopy.StdCopy(&stdout, &stderr, attach.Reader); err != nil {
		return nil, fmt.Errorf("exec read: %w", err)
	}

	inspect, err := m.cli.ContainerExecInspect(ctx, resp.ID)
	if err != nil {
		return nil, fmt.Errorf("exec inspect: %w", err)
	}

	return &ExecResult{
		Stdout:   stdout.String(),
		Stderr:   stderr.String(),
		ExitCode: inspect.ExitCode,
	}, nil
}

// ExecAttachOptions specifies options for an interactive exec session (for MCP stdio).
type ExecAttachOptions struct {
	Cmd []string
}

// ExecStdio creates an interactive exec session with stdin/stdout attached.
// This is the core mechanism for MCP communication via docker exec.
// The caller is responsible for closing the returned HijackedResponse.
//
// When Tty is false, Docker multiplexes stdout/stderr with an 8-byte frame header.
// The caller (Transport) must use stdcopy.StdCopy or a demuxer to strip the headers.
// We disable stderr attachment so only stdout frames appear.
func (m *Manager) ExecStdio(ctx context.Context, containerID string, opts ExecAttachOptions) (types.HijackedResponse, error) {
	execCfg := container.ExecOptions{
		Cmd:          opts.Cmd,
		AttachStdin:  true,
		AttachStdout: true,
		AttachStderr: true,
		Tty:          false,
	}

	resp, err := m.cli.ContainerExecCreate(ctx, containerID, execCfg)
	if err != nil {
		return types.HijackedResponse{}, fmt.Errorf("exec create: %w", err)
	}

	hijacked, err := m.cli.ContainerExecAttach(ctx, resp.ID, container.ExecAttachOptions{})
	if err != nil {
		return types.HijackedResponse{}, fmt.Errorf("exec attach: %w", err)
	}

	slog.Debug("exec stdio session started",
		"container_id", containerID[:12],
		"cmd", strings.Join(opts.Cmd, " "),
	)

	return hijacked, nil
}

// ImageExists checks if a Docker image exists locally.
func (m *Manager) ImageExists(ctx context.Context, ref string) (bool, error) {
	_, _, err := m.cli.ImageInspectWithRaw(ctx, ref)
	if err != nil {
		if client.IsErrNotFound(err) {
			return false, nil
		}
		return false, fmt.Errorf("image inspect: %w", err)
	}
	return true, nil
}

// PullImage pulls a Docker image from a registry.
func (m *Manager) PullImage(ctx context.Context, ref string) error {
	slog.Info("pulling image", "image", ref)
	reader, err := m.cli.ImagePull(ctx, ref, image.PullOptions{})
	if err != nil {
		return fmt.Errorf("image pull: %w", err)
	}
	defer reader.Close()
	// Consume the output to wait for pull completion
	if _, err := io.Copy(io.Discard, reader); err != nil {
		return fmt.Errorf("image pull read: %w", err)
	}
	slog.Info("image pulled", "image", ref)
	return nil
}
