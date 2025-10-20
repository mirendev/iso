package iso

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/api/types/network"
	"github.com/moby/term"
)

// containerManager handles container lifecycle operations
type containerManager struct {
	docker         *dockerClient
	imageName      string
	containerName  string
	dockerfilePath string
	projectName    string
	networkName    string
	services       map[string]ServiceConfig
	isoDir         string
}

// newContainerManager creates a new container manager
func newContainerManager() (*containerManager, error) {
	docker, err := newDockerClient()
	if err != nil {
		return nil, err
	}

	// Try to find .iso directory
	isoDir, projectRoot, found := findIsoDir()
	if !found {
		return nil, fmt.Errorf("no .iso directory found - please create one with a Dockerfile and optional services.yml")
	}

	// Load services if they exist
	services, err := loadServicesFile(isoDir)
	if err != nil {
		return nil, err
	}

	projectName := filepath.Base(projectRoot)
	dockerfilePath := filepath.Join(isoDir, "Dockerfile")

	// Check if Dockerfile exists
	if _, err := os.Stat(dockerfilePath); os.IsNotExist(err) {
		return nil, fmt.Errorf("Dockerfile not found at %s", dockerfilePath)
	}

	networkName := fmt.Sprintf("%s-network", projectName)
	containerName := fmt.Sprintf("%s-shell", projectName)
	imageName := fmt.Sprintf("%s-shell", projectName)

	return &containerManager{
		docker:         docker,
		imageName:      imageName,
		containerName:  containerName,
		dockerfilePath: dockerfilePath,
		projectName:    projectName,
		networkName:    networkName,
		services:       services,
		isoDir:         isoDir,
	}, nil
}

// close closes the container manager and Docker client
func (cm *containerManager) close() error {
	return cm.docker.close()
}

// ensureImage ensures the Docker image exists, building it if necessary
func (cm *containerManager) ensureImage() error {
	exists, err := cm.docker.imageExists(cm.imageName)
	if err != nil {
		return err
	}

	if !exists {
		slog.Info("building image", "image", cm.imageName, "dockerfile", cm.dockerfilePath)
		if err := cm.docker.buildImage(cm.dockerfilePath, cm.imageName); err != nil {
			return err
		}
		slog.Info("image built successfully", "image", cm.imageName)
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

	isoPath, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("failed to get executable path: %w", err)
	}

	// Create container
	config := &container.Config{
		Image:      cm.imageName,
		WorkingDir: "/workspace",
		Cmd:        []string{"/iso", "init"},
	}

	hostConfig := &container.HostConfig{
		Binds: []string{
			fmt.Sprintf("%s:/workspace", mountPath),
			fmt.Sprintf("%s:/iso:ro", isoPath),
		},
		AutoRemove: false,
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
		config,
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

// runCommand runs a command in the container and returns the exit code
func (cm *containerManager) runCommand(command []string) (int, error) {
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
			// Start services first
			if err := cm.startAllServices(false); err != nil {
				return 0, err
			}

			// Get container ID and start it
			containerID, err = cm.docker.getContainerID(cm.containerName)
			if err != nil {
				return 0, err
			}
			slog.Info("starting existing container", "container", cm.containerName)
			if err := cm.docker.client.ContainerStart(cm.docker.ctx, containerID, container.StartOptions{}); err != nil {
				return 0, fmt.Errorf("failed to start container: %w", err)
			}
		} else {
			// Ensure image exists
			if err := cm.ensureImage(); err != nil {
				return 0, err
			}

			// Start services first
			if err := cm.startAllServices(false); err != nil {
				return 0, err
			}

			// Start a new container
			slog.Info("starting new container", "container", cm.containerName)
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
	workDir := "/workspace"

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
		workDir = filepath.Join("/workspace", relPath)
	}

	// Check if stdin is a TTY
	isTTY := term.IsTerminal(os.Stdin.Fd())

	// Wrap the command with /iso in-env run to handle pre/post scripts
	wrappedCommand := append([]string{"/iso", "in-env", "run"}, command...)

	// Execute the command in the container
	execConfig := container.ExecOptions{
		Cmd:          wrappedCommand,
		AttachStdout: true,
		AttachStderr: true,
		AttachStdin:  true, // Always attach stdin
		Tty:          isTTY,
		WorkingDir:   workDir,
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
			// Non-TTY mode: use reader for output
			_, err = io.Copy(os.Stdout, attachResp.Reader)
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

// stopContainer stops and removes the container
func (cm *containerManager) stopContainer() error {
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

	slog.Info("container stopped and removed", "container", cm.containerName)

	// Stop all services
	if err := cm.stopAllServices(); err != nil {
		return err
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
	if len(cm.services) == 0 {
		// No services, no need for network
		return nil
	}

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
	containerName := fmt.Sprintf("%s_%s", cm.projectName, serviceName)

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
			slog.Info("starting service", "service", serviceName)
		}
		if err := cm.startService(serviceName, config); err != nil {
			return err
		}
		if verbose {
			slog.Info("service started", "service", serviceName)
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
		// Don't fail if network removal fails - it might still be in use
		slog.Warn("failed to remove network", "network", cm.networkName, "error", err)
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
