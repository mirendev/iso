package iso

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/pkg/stdcopy"
	"github.com/moby/term"
)

// containerManager handles container lifecycle operations
type containerManager struct {
	docker              *dockerClient
	imageName           string
	containerName       string
	dockerfilePath      string
	projectName         string // Deprecated: use worktreeProjectName
	baseProjectName     string // Base project name (shared across worktrees, used for cache volumes)
	worktreeProjectName string // Worktree-specific project name (used for containers, networks, session volumes)
	projectRoot         string // Absolute path to project root directory
	session             string // Session name (default is "default")
	networkName         string
	services            map[string]ServiceConfig
	isoDir              string
	tempIsoPath         string // Path to extracted Linux iso binary
	config              *Config
}

// newContainerManager creates a new container manager
func newContainerManager(session string) (*containerManager, error) {
	// Default to "default" session if not specified
	if session == "" {
		session = "default"
	}

	docker, err := newDockerClient()
	if err != nil {
		return nil, err
	}

	// Try to find .iso directory
	isoDir, projectRoot, found := findIsoDir()
	if !found {
		return nil, fmt.Errorf("no .iso directory found - please create one with a Dockerfile and optional services.yml")
	}

	// Load config if it exists
	config, err := loadConfigFile(isoDir)
	if err != nil {
		return nil, err
	}

	// Load services if they exist
	services, err := loadServicesFile(isoDir)
	if err != nil {
		return nil, err
	}

	// Detect git worktree to determine project names
	baseProjectName, worktreeProjectName := detectGitWorktree(projectRoot)

	dockerfilePath := filepath.Join(isoDir, "Dockerfile")

	// Check if Dockerfile exists
	if _, err := os.Stat(dockerfilePath); os.IsNotExist(err) {
		return nil, fmt.Errorf("Dockerfile not found at %s", dockerfilePath)
	}

	// Generate names with session support
	// Image name uses worktreeProjectName (worktrees can have different Dockerfiles)
	// Container and network names use worktreeProjectName (isolated per worktree)
	// Cache volumes will use baseProjectName (shared across worktrees)
	imageName := fmt.Sprintf("%s-shell", worktreeProjectName)

	var networkName, containerName string
	if session == "default" {
		networkName = fmt.Sprintf("%s-network", worktreeProjectName)
		containerName = fmt.Sprintf("%s-shell", worktreeProjectName)
	} else {
		networkName = fmt.Sprintf("%s-%s-network", worktreeProjectName, session)
		containerName = fmt.Sprintf("%s-%s-shell", worktreeProjectName, session)
	}

	// Get Docker architecture to determine which binary to use
	arch, err := docker.getArchitecture()
	if err != nil {
		return nil, err
	}

	// Extract the embedded Linux iso binary to .iso directory (reuses if exists)
	isoPath, err := extractLinuxBinary(isoDir, arch)
	if err != nil {
		return nil, fmt.Errorf("failed to extract Linux binary: %w", err)
	}

	return &containerManager{
		docker:              docker,
		imageName:           imageName,
		containerName:       containerName,
		dockerfilePath:      dockerfilePath,
		projectName:         worktreeProjectName, // Maintain backward compatibility
		baseProjectName:     baseProjectName,
		worktreeProjectName: worktreeProjectName,
		projectRoot:         projectRoot,
		session:             session,
		networkName:         networkName,
		services:            services,
		isoDir:              isoDir,
		tempIsoPath:         isoPath,
		config:              config,
	}, nil
}

// close closes the container manager and Docker client
func (cm *containerManager) close() error {
	// Note: We don't clean up tempIsoPath as it's in .iso directory and reused
	return cm.docker.close()
}

// getVolumeNameForPath generates a Docker volume name for a container path
// Session-specific volumes are removed when the session is stopped
// Uses worktreeProjectName to isolate volumes per worktree
func (cm *containerManager) getVolumeNameForPath(path string) string {
	// Sanitize the path to create a valid volume name
	// Replace / with - and remove leading/trailing dashes
	sanitized := strings.ReplaceAll(strings.Trim(path, "/"), "/", "-")
	if cm.session == "default" {
		return fmt.Sprintf("%s-%s", cm.worktreeProjectName, sanitized)
	}
	return fmt.Sprintf("%s-%s-%s", cm.worktreeProjectName, cm.session, sanitized)
}

// getCacheVolumeNameForPath generates a Docker volume name for a cache path
// Cache volumes are shared across all sessions and worktrees, persist until pruned
// Uses baseProjectName to share caches across all worktrees of the same base repository
func (cm *containerManager) getCacheVolumeNameForPath(path string) string {
	// Sanitize the path to create a valid volume name
	// Replace / with - and remove leading/trailing dashes
	sanitized := strings.ReplaceAll(strings.Trim(path, "/"), "/", "-")
	return fmt.Sprintf("%s-cache-%s", cm.baseProjectName, sanitized)
}

// ensureVolumes creates Docker volumes for configured volume and cache paths
func (cm *containerManager) ensureVolumes() error {
	// Create session-specific volumes
	for _, volumePath := range cm.config.Volumes {
		volumeName := cm.getVolumeNameForPath(volumePath)

		// Check if volume exists
		exists, err := cm.docker.volumeExists(volumeName)
		if err != nil {
			return err
		}

		if !exists {
			slog.Debug("creating volume", "volume", volumeName, "path", volumePath)
			if err := cm.docker.createVolume(volumeName); err != nil {
				return err
			}
		}
	}

	// Create shared cache volumes
	for _, cachePath := range cm.config.Cache {
		volumeName := cm.getCacheVolumeNameForPath(cachePath)

		// Check if volume exists
		exists, err := cm.docker.volumeExists(volumeName)
		if err != nil {
			return err
		}

		if !exists {
			slog.Debug("creating cache volume", "volume", volumeName, "path", cachePath)
			if err := cm.docker.createVolume(volumeName); err != nil {
				return err
			}
		}
	}

	return nil
}

// ensureImage ensures the Docker image exists, building it if necessary
func (cm *containerManager) ensureImage() error {
	exists, err := cm.docker.imageExists(cm.imageName)
	if err != nil {
		return err
	}

	if !exists {
		slog.Debug("building image", "image", cm.imageName, "dockerfile", cm.dockerfilePath)
		if err := cm.docker.buildImage(cm.dockerfilePath, cm.imageName); err != nil {
			return err
		}
		slog.Debug("image built successfully", "image", cm.imageName)
	}

	return nil
}

// startContainer starts a new container
func (cm *containerManager) startContainer() (string, error) {
	// Determine the mount path
	var mountPath string
	if cm.isoDir != "" {
		// If using .iso directory, mount the project root (parent of .iso)
		mountPath = filepath.Dir(cm.isoDir)
	} else {
		// Otherwise mount the directory containing the Dockerfile
		dockerfileDir := filepath.Dir(cm.dockerfilePath)
		var err error
		mountPath, err = filepath.Abs(dockerfileDir)
		if err != nil {
			return "", fmt.Errorf("failed to get absolute path of Dockerfile directory: %w", err)
		}
	}

	// Build ISO_SERVICES environment variable from services with ports
	var isoServices []string
	for serviceName, serviceConfig := range cm.services {
		if serviceConfig.Port > 0 {
			isoServices = append(isoServices, fmt.Sprintf("%s:%d", serviceName, serviceConfig.Port))
		}
	}

	// Create container environment
	env := []string{
		fmt.Sprintf("ISO_WORKDIR=%s", cm.config.WorkDir),
	}
	if len(isoServices) > 0 {
		env = append(env, fmt.Sprintf("ISO_SERVICES=%s", strings.Join(isoServices, ",")))
	}

	// Ensure volumes exist
	if err := cm.ensureVolumes(); err != nil {
		return "", err
	}

	// Build bind mounts list
	binds := []string{
		fmt.Sprintf("%s:%s", mountPath, cm.config.WorkDir),
		fmt.Sprintf("%s:/iso:ro", cm.tempIsoPath),
	}

	// Add session-specific volume mounts
	for _, volumePath := range cm.config.Volumes {
		volumeName := cm.getVolumeNameForPath(volumePath)
		binds = append(binds, fmt.Sprintf("%s:%s", volumeName, volumePath))
	}

	// Add shared cache volume mounts
	for _, cachePath := range cm.config.Cache {
		volumeName := cm.getCacheVolumeNameForPath(cachePath)
		binds = append(binds, fmt.Sprintf("%s:%s", volumeName, cachePath))
	}

	// Create container
	containerConfig := &container.Config{
		Image:      cm.imageName,
		WorkingDir: cm.config.WorkDir,
		Cmd:        []string{"/iso", "_internal-init"},
		Env:        env,
		Labels: map[string]string{
			"iso.managed":      "true",
			"iso.project.name": cm.projectName,
			"iso.project.dir":  cm.projectRoot,
			"iso.session":      cm.session,
			"iso.name":         "shell",
		},
	}

	hostConfig := &container.HostConfig{
		Binds:      binds,
		AutoRemove: false,
		Privileged: cm.config.Privileged,
	}

	// Set up network configuration if we have services
	var networkConfig *network.NetworkingConfig
	if len(cm.services) > 0 {
		networkConfig = &network.NetworkingConfig{
			EndpointsConfig: map[string]*network.EndpointSettings{
				cm.networkName: {},
			},
		}
	}

	resp, err := cm.docker.client.ContainerCreate(
		cm.docker.ctx,
		containerConfig,
		hostConfig,
		networkConfig,
		nil,
		cm.containerName,
	)
	if err != nil {
		return "", fmt.Errorf("failed to create container: %w", err)
	}

	// Start the container
	if err := cm.docker.client.ContainerStart(cm.docker.ctx, resp.ID, container.StartOptions{}); err != nil {
		return "", fmt.Errorf("failed to start container: %w", err)
	}

	return resp.ID, nil
}

// startFreshServices starts fresh service containers for a single run
// Returns a map of service container IDs that should be stopped after the run
func (cm *containerManager) startFreshServices(runID string) (map[string]string, error) {
	if len(cm.services) == 0 {
		return nil, nil
	}

	// Ensure network exists
	if err := cm.ensureNetwork(); err != nil {
		return nil, err
	}

	serviceContainerIDs := make(map[string]string)

	// Start each service with unique name
	for serviceName, config := range cm.services {
		// Generate unique service container name
		var containerName string
		if cm.session == "default" {
			containerName = fmt.Sprintf("%s_%s-fresh-%s", cm.projectName, serviceName, runID)
		} else {
			containerName = fmt.Sprintf("%s-%s_%s-fresh-%s", cm.projectName, cm.session, serviceName, runID)
		}

		// Pull the image if it doesn't exist
		imageExists, err := cm.docker.imageExists(config.Image)
		if err != nil {
			return nil, err
		}

		if !imageExists {
			slog.Debug("pulling image", "image", config.Image)
			if err := cm.docker.pullImage(config.Image); err != nil {
				return nil, err
			}
		}

		// Convert environment map to slice
		var env []string
		for key, value := range config.Environment {
			env = append(env, fmt.Sprintf("%s=%s", key, value))
		}

		// Create container config
		containerConfig := &container.Config{
			Image: config.Image,
			Env:   env,
			Labels: map[string]string{
				"iso.managed":      "true",
				"iso.project.name": cm.projectName,
				"iso.project.dir":  cm.projectRoot,
				"iso.session":      cm.session,
				"iso.service":      "true",
				"iso.service.name": serviceName,
				"iso.name":         serviceName,
				"iso.fresh":        "true",
			},
		}

		// Set command if specified
		if len(config.Command) > 0 {
			containerConfig.Cmd = config.Command
		}

		hostConfig := &container.HostConfig{
			AutoRemove: true, // Auto-remove when stopped
		}

		networkConfig := &network.NetworkingConfig{
			EndpointsConfig: map[string]*network.EndpointSettings{
				cm.networkName: {
					Aliases: []string{serviceName}, // Use service name as DNS alias
				},
			},
		}

		// Create the service container
		resp, err := cm.docker.client.ContainerCreate(
			cm.docker.ctx,
			containerConfig,
			hostConfig,
			networkConfig,
			nil,
			containerName,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to create fresh service container %s: %w", serviceName, err)
		}

		// Start the service container
		if err := cm.docker.client.ContainerStart(cm.docker.ctx, resp.ID, container.StartOptions{}); err != nil {
			return nil, fmt.Errorf("failed to start fresh service container %s: %w", serviceName, err)
		}

		serviceContainerIDs[serviceName] = resp.ID
		slog.Debug("fresh service started", "service", serviceName, "container", containerName)
	}

	return serviceContainerIDs, nil
}

// stopFreshServices stops and removes fresh service containers
func (cm *containerManager) stopFreshServices(serviceContainerIDs map[string]string) {
	if len(serviceContainerIDs) == 0 {
		return
	}

	timeout := 2
	for serviceName, containerID := range serviceContainerIDs {
		if err := cm.docker.client.ContainerStop(cm.docker.ctx, containerID, container.StopOptions{
			Timeout: &timeout,
		}); err != nil {
			// Ignore "already in progress" errors - containers have AutoRemove so Docker is cleaning them up
			errStr := err.Error()
			if !strings.Contains(errStr, "already in progress") && !strings.Contains(errStr, "No such container") {
				slog.Warn("failed to stop fresh service", "service", serviceName, "error", err)
			}
		}
	}
}

// runCommand runs a command in the container and returns the exit code
// envVars is a slice of environment variables in KEY=VALUE format
func (cm *containerManager) runCommand(command []string, envVars []string) (int, error) {
	// Generate unique run ID for fresh services
	runID := fmt.Sprintf("%d", time.Now().UnixNano())

	// Start fresh services for this run
	serviceContainerIDs, err := cm.startFreshServices(runID)
	if err != nil {
		return 0, err
	}
	// Ensure services are stopped after run completes
	defer cm.stopFreshServices(serviceContainerIDs)

	// Check if container is already running
	running, err := cm.docker.isContainerRunning(cm.containerName)
	if err != nil {
		return 0, err
	}

	var containerID string
	if !running {
		// Check if container exists but is stopped
		exists, err := cm.docker.containerExists(cm.containerName)
		if err != nil {
			return 0, err
		}

		if exists {
			// Get container ID and start it
			containerID, err = cm.docker.getContainerID(cm.containerName)
			if err != nil {
				return 0, err
			}
			if err := cm.docker.client.ContainerStart(cm.docker.ctx, containerID, container.StartOptions{}); err != nil {
				return 0, fmt.Errorf("failed to start container: %w", err)
			}
		} else {
			// Ensure image exists
			if err := cm.ensureImage(); err != nil {
				return 0, err
			}

			// Start a new container
			containerID, err = cm.startContainer()
			if err != nil {
				return 0, err
			}
		}
	} else {
		containerID, err = cm.docker.getContainerID(cm.containerName)
		if err != nil {
			return 0, err
		}
	}

	// Calculate the working directory in the container
	workDir := cm.config.WorkDir

	// Get current working directory
	cwd, err := os.Getwd()
	if err != nil {
		return 0, fmt.Errorf("failed to get current directory: %w", err)
	}

	// Determine the mount root (same logic as startContainer)
	var mountRoot string
	if cm.isoDir != "" {
		// If using .iso directory, mount root is the project root (parent of .iso)
		mountRoot = filepath.Dir(cm.isoDir)
	} else {
		// Otherwise mount root is the directory containing the Dockerfile
		dockerfileDir := filepath.Dir(cm.dockerfilePath)
		mountRoot, err = filepath.Abs(dockerfileDir)
		if err != nil {
			return 0, fmt.Errorf("failed to get absolute path of Dockerfile directory: %w", err)
		}
	}

	// Calculate relative path from mount root to current dir
	relPath, err := filepath.Rel(mountRoot, cwd)
	if err != nil {
		return 0, fmt.Errorf("failed to calculate relative path: %w", err)
	}

	// If we're in a subdirectory, use that in the container
	if relPath != "." && !filepath.IsAbs(relPath) && relPath != ".." && !filepath.HasPrefix(relPath, "..") {
		workDir = filepath.Join(cm.config.WorkDir, relPath)
	}

	// Check if stdin is a TTY
	isTTY := term.IsTerminal(os.Stdin.Fd())

	// If TTY mode, set terminal to raw mode and handle resize
	var oldState *term.State
	if isTTY {
		// Save current terminal state
		oldState, err = term.SaveState(os.Stdin.Fd())
		if err != nil {
			return 0, fmt.Errorf("failed to save terminal state: %w", err)
		}

		// Ensure terminal is restored on exit
		defer func() {
			if oldState != nil {
				_ = term.RestoreTerminal(os.Stdin.Fd(), oldState)
			}
		}()

		// Put terminal into raw mode
		if _, err := term.MakeRaw(os.Stdin.Fd()); err != nil {
			return 0, fmt.Errorf("failed to set terminal to raw mode: %w", err)
		}
	}

	// Wrap the command with /iso in-env run to handle pre/post scripts
	wrappedCommand := append([]string{"/iso", "in-env", "run", "--"}, command...)

	// Build exec environment (include ISO_WORKDIR for the in-env command)
	// Start with ISO internal variables
	execEnv := []string{
		fmt.Sprintf("ISO_WORKDIR=%s", cm.config.WorkDir),
		fmt.Sprintf("ISO_SESSION=%s", cm.session),
	}

	// If TTY mode, pass through TERM environment variable
	if isTTY {
		if termValue := os.Getenv("TERM"); termValue != "" {
			// Special case: xterm-ghostty -> xterm-256color
			if termValue == "xterm-ghostty" {
				termValue = "xterm-256color"
			}
			execEnv = append(execEnv, fmt.Sprintf("TERM=%s", termValue))
		}
	}

	// Add environment variables from config.yml
	for key, value := range cm.config.Environment {
		execEnv = append(execEnv, fmt.Sprintf("%s=%s", key, value))
	}

	// Add command-line environment variables (these override config.yml)
	execEnv = append(execEnv, envVars...)

	// Execute the command in the container
	execConfig := container.ExecOptions{
		Cmd:          wrappedCommand,
		AttachStdout: true,
		AttachStderr: true,
		AttachStdin:  true, // Always attach stdin
		Tty:          isTTY,
		WorkingDir:   workDir,
		Env:          execEnv,
	}

	execResp, err := cm.docker.client.ContainerExecCreate(cm.docker.ctx, containerID, execConfig)
	if err != nil {
		return 0, fmt.Errorf("failed to create exec: %w", err)
	}

	// Attach to the exec instance
	attachResp, err := cm.docker.client.ContainerExecAttach(cm.docker.ctx, execResp.ID, container.ExecStartOptions{
		Tty: isTTY,
	})
	if err != nil {
		return 0, fmt.Errorf("failed to attach to exec: %w", err)
	}
	defer attachResp.Close()

	// If TTY mode, set terminal size and monitor for resize events
	if isTTY {
		// Get current terminal size
		winsize, err := term.GetWinsize(os.Stdin.Fd())
		if err == nil {
			// Resize the exec session to match local terminal
			if err := cm.docker.client.ContainerExecResize(cm.docker.ctx, execResp.ID, container.ResizeOptions{
				Height: uint(winsize.Height),
				Width:  uint(winsize.Width),
			}); err != nil {
				slog.Warn("failed to set initial terminal size", "error", err)
			}
		}

		// Monitor for terminal resize events using SIGWINCH
		go func() {
			sigChan := make(chan os.Signal, 1)
			signal.Notify(sigChan, syscall.SIGWINCH)
			defer signal.Stop(sigChan)

			for {
				select {
				case <-sigChan:
					// Terminal was resized, update container
					if ws, err := term.GetWinsize(os.Stdin.Fd()); err == nil {
						_ = cm.docker.client.ContainerExecResize(cm.docker.ctx, execResp.ID, container.ResizeOptions{
							Height: uint(ws.Height),
							Width:  uint(ws.Width),
						})
					}
				case <-cm.docker.ctx.Done():
					return
				}
			}
		}()
	}

	// Copy stdin in background
	go func() {
		_, _ = io.Copy(attachResp.Conn, os.Stdin)
		// Close write side when stdin closes to propagate EOF
		if closer, ok := attachResp.Conn.(interface{ CloseWrite() error }); ok {
			closer.CloseWrite()
		}
	}()

	// Copy stdout/stderr in background based on TTY mode
	outputDone := make(chan error, 1)
	go func() {
		var err error
		if isTTY {
			// TTY mode: use bidirectional connection
			_, err = io.Copy(os.Stdout, attachResp.Conn)
		} else {
			// Non-TTY mode: demultiplex stdout and stderr
			_, err = stdcopy.StdCopy(os.Stdout, os.Stderr, attachResp.Reader)
		}
		outputDone <- err
	}()

	// Wait for either output to finish or context to be done
	select {
	case err := <-outputDone:
		if err != nil && err != io.EOF {
			return 0, fmt.Errorf("failed to read output: %w", err)
		}
	case <-cm.docker.ctx.Done():
		return 0, cm.docker.ctx.Err()
	}

	// Check exit code
	inspectResp, err := cm.docker.client.ContainerExecInspect(cm.docker.ctx, execResp.ID)
	if err != nil {
		return 0, fmt.Errorf("failed to inspect exec: %w", err)
	}

	return inspectResp.ExitCode, nil
}

// resetContainer stops and removes the container but keeps services and volumes
func (cm *containerManager) resetContainer() error {
	exists, err := cm.docker.containerExists(cm.containerName)
	if err != nil {
		return err
	}

	if !exists {
		slog.Info("container does not exist", "container", cm.containerName)
		return nil
	}

	containerID, err := cm.docker.getContainerID(cm.containerName)
	if err != nil {
		return err
	}

	// Stop the container
	timeout := 10
	if err := cm.docker.client.ContainerStop(cm.docker.ctx, containerID, container.StopOptions{
		Timeout: &timeout,
	}); err != nil {
		return fmt.Errorf("failed to stop container: %w", err)
	}

	// Remove the container
	if err := cm.docker.client.ContainerRemove(cm.docker.ctx, containerID, container.RemoveOptions{}); err != nil {
		return fmt.Errorf("failed to remove container: %w", err)
	}

	slog.Info("container reset - will be recreated on next run", "container", cm.containerName)

	return nil
}

// stopContainer stops and removes the container
func (cm *containerManager) stopContainer() error {
	// Use labels to find all containers for this project (main + services)
	containers, err := cm.docker.listProjectContainers(cm.projectName, cm.session)
	if err != nil {
		return err
	}

	if len(containers) == 0 {
		slog.Info("no containers to stop", "project", cm.projectName)
		return nil
	}

	// Stop and remove all containers
	timeout := 10
	for _, c := range containers {
		slog.Debug("stopping container", "name", c.Name, "service", c.IsService)

		// Stop the container (may already be stopped/removed if it had AutoRemove)
		if err := cm.docker.client.ContainerStop(cm.docker.ctx, c.ID, container.StopOptions{
			Timeout: &timeout,
		}); err != nil {
			// If container doesn't exist or removal already in progress, it was auto-removed - that's fine
			errStr := err.Error()
			if !strings.Contains(errStr, "No such container") && !strings.Contains(errStr, "already in progress") {
				slog.Warn("failed to stop container, continuing cleanup", "name", c.Name, "error", err)
				continue
			}
			slog.Debug("container already removed or being removed", "container", c.Name)
			continue
		}

		// Remove the container
		if err := cm.docker.client.ContainerRemove(cm.docker.ctx, c.ID, container.RemoveOptions{}); err != nil {
			// If container doesn't exist or removal already in progress, it was auto-removed - that's fine
			errStr := err.Error()
			if !strings.Contains(errStr, "No such container") && !strings.Contains(errStr, "already in progress") {
				slog.Warn("failed to remove container, continuing cleanup", "name", c.Name, "error", err)
				continue
			}
			slog.Debug("container already removed or being removed", "container", c.Name)
			continue
		}

		slog.Debug("container stopped and removed", "container", c.Name)
	}

	// Remove the network
	if err := cm.docker.removeNetwork(cm.networkName); err != nil {
		// Don't fail if network removal fails - it might still be in use or already removed
		if !strings.Contains(err.Error(), "not found") {
			slog.Warn("failed to remove network", "network", cm.networkName, "error", err)
		}
	}

	// Remove volumes
	for _, volumePath := range cm.config.Volumes {
		volumeName := cm.getVolumeNameForPath(volumePath)

		exists, err := cm.docker.volumeExists(volumeName)
		if err != nil {
			slog.Warn("failed to check volume existence", "volume", volumeName, "error", err)
			continue
		}

		if exists {
			slog.Debug("removing volume", "volume", volumeName, "path", volumePath)
			if err := cm.docker.removeVolume(volumeName); err != nil {
				slog.Warn("failed to remove volume", "volume", volumeName, "error", err)
			}
		}
	}

	return nil
}

// rebuildImage rebuilds the Docker image
func (cm *containerManager) rebuildImage() error {
	// Check if image exists and remove it
	exists, err := cm.docker.imageExists(cm.imageName)
	if err != nil {
		return err
	}

	if exists {
		slog.Info("removing existing image", "image", cm.imageName)
		if err := cm.docker.removeImage(cm.imageName); err != nil {
			return err
		}
	}

	// Build the image
	slog.Info("building image", "image", cm.imageName, "dockerfile", cm.dockerfilePath)
	if err := cm.docker.buildImage(cm.dockerfilePath, cm.imageName); err != nil {
		return err
	}

	slog.Info("image built successfully", "image", cm.imageName)
	return nil
}

// getStatus returns the status of the container
func (cm *containerManager) getStatus() (string, error) {
	exists, err := cm.docker.containerExists(cm.containerName)
	if err != nil {
		return "", err
	}

	if !exists {
		return "Container does not exist", nil
	}

	running, err := cm.docker.isContainerRunning(cm.containerName)
	if err != nil {
		return "", err
	}

	if running {
		return "Container is running", nil
	}

	return "Container exists but is stopped", nil
}

// ensureNetwork creates the Docker network if it doesn't exist
func (cm *containerManager) ensureNetwork() error {
	exists, err := cm.docker.networkExists(cm.networkName)
	if err != nil {
		return err
	}

	if !exists {
		_, err = cm.docker.createNetwork(cm.networkName)
		if err != nil {
			return err
		}
	}

	return nil
}

// startService starts a single service container
func (cm *containerManager) startService(serviceName string, config ServiceConfig) error {
	var containerName string
	if cm.session == "default" {
		containerName = fmt.Sprintf("%s_%s", cm.projectName, serviceName)
	} else {
		containerName = fmt.Sprintf("%s-%s_%s", cm.projectName, cm.session, serviceName)
	}

	// Check if service container already exists and is running
	running, err := cm.docker.isContainerRunning(containerName)
	if err != nil {
		return err
	}

	if running {
		// Service already running
		return nil
	}

	// Check if container exists but is stopped
	exists, err := cm.docker.containerExists(containerName)
	if err != nil {
		return err
	}

	if exists {
		// Start existing container
		containerID, err := cm.docker.getContainerID(containerName)
		if err != nil {
			return err
		}
		return cm.docker.client.ContainerStart(cm.docker.ctx, containerID, container.StartOptions{})
	}

	// Pull the image if it doesn't exist
	imageExists, err := cm.docker.imageExists(config.Image)
	if err != nil {
		return err
	}

	if !imageExists {
		slog.Info("pulling image", "image", config.Image)
		if err := cm.docker.pullImage(config.Image); err != nil {
			return err
		}
	}

	// Convert environment map to slice
	var env []string
	for key, value := range config.Environment {
		env = append(env, fmt.Sprintf("%s=%s", key, value))
	}

	// Create container config
	containerConfig := &container.Config{
		Image: config.Image,
		Env:   env,
		Labels: map[string]string{
			"iso.managed":      "true",
			"iso.project.name": cm.projectName,
			"iso.project.dir":  cm.projectRoot,
			"iso.session":      cm.session,
			"iso.service":      "true",
			"iso.service.name": serviceName,
			"iso.name":         serviceName,
		},
	}

	// Set command if specified
	if len(config.Command) > 0 {
		containerConfig.Cmd = config.Command
	}

	hostConfig := &container.HostConfig{}

	networkConfig := &network.NetworkingConfig{
		EndpointsConfig: map[string]*network.EndpointSettings{
			cm.networkName: {
				Aliases: []string{serviceName},
			},
		},
	}

	// Create the service container
	resp, err := cm.docker.client.ContainerCreate(
		cm.docker.ctx,
		containerConfig,
		hostConfig,
		networkConfig,
		nil,
		containerName,
	)
	if err != nil {
		return fmt.Errorf("failed to create service container %s: %w", serviceName, err)
	}

	// Start the service container
	if err := cm.docker.client.ContainerStart(cm.docker.ctx, resp.ID, container.StartOptions{}); err != nil {
		return fmt.Errorf("failed to start service container %s: %w", serviceName, err)
	}

	return nil
}

// startAllServices starts all service containers
func (cm *containerManager) startAllServices(verbose bool) error {
	if len(cm.services) == 0 {
		return nil
	}

	// Ensure network exists
	if err := cm.ensureNetwork(); err != nil {
		return err
	}

	// Start each service
	for serviceName, config := range cm.services {
		if verbose {
			slog.Debug("starting service", "service", serviceName)
		}
		if err := cm.startService(serviceName, config); err != nil {
			return err
		}
		if verbose {
			slog.Debug("service started", "service", serviceName)
		}
	}

	return nil
}

// stopAllServices stops and removes all service containers
func (cm *containerManager) stopAllServices() error {
	if len(cm.services) == 0 {
		return nil
	}

	for serviceName := range cm.services {
		containerName := fmt.Sprintf("%s_%s", cm.projectName, serviceName)

		exists, err := cm.docker.containerExists(containerName)
		if err != nil {
			return err
		}

		if !exists {
			continue
		}

		containerID, err := cm.docker.getContainerID(containerName)
		if err != nil {
			return err
		}

		// Stop the container
		timeout := 10
		if err := cm.docker.client.ContainerStop(cm.docker.ctx, containerID, container.StopOptions{
			Timeout: &timeout,
		}); err != nil {
			return fmt.Errorf("failed to stop service %s: %w", serviceName, err)
		}

		// Remove the container
		if err := cm.docker.client.ContainerRemove(cm.docker.ctx, containerID, container.RemoveOptions{}); err != nil {
			return fmt.Errorf("failed to remove service %s: %w", serviceName, err)
		}
	}

	// Remove the network
	if err := cm.docker.removeNetwork(cm.networkName); err != nil {
		// Don't fail if network removal fails - it might still be in use or already removed
		if !strings.Contains(err.Error(), "not found") {
			slog.Warn("failed to remove network", "network", cm.networkName, "error", err)
		}
	}

	return nil
}

// pruneCacheVolumes removes all cache volumes for this project
func (cm *containerManager) pruneCacheVolumes() error {
	if len(cm.config.Cache) == 0 {
		slog.Info("no cache volumes configured")
		return nil
	}

	for _, cachePath := range cm.config.Cache {
		volumeName := cm.getCacheVolumeNameForPath(cachePath)

		exists, err := cm.docker.volumeExists(volumeName)
		if err != nil {
			slog.Warn("failed to check cache volume existence", "volume", volumeName, "error", err)
			continue
		}

		if exists {
			slog.Info("removing cache volume", "volume", volumeName, "path", cachePath)
			if err := cm.docker.removeVolume(volumeName); err != nil {
				slog.Warn("failed to remove cache volume", "volume", volumeName, "error", err)
			}
		} else {
			slog.Debug("cache volume does not exist", "volume", volumeName)
		}
	}

	return nil
}

// pullImage pulls a Docker image from a registry
func (cm *containerManager) pullImage() error {
	out, err := cm.docker.client.ImagePull(cm.docker.ctx, cm.imageName, image.PullOptions{})
	if err != nil {
		return fmt.Errorf("failed to pull image: %w", err)
	}
	defer out.Close()

	_, err = io.Copy(os.Stdout, out)
	return err
}
