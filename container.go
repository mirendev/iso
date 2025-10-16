package iso

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/image"
	"github.com/moby/term"
)

// containerManager handles container lifecycle operations
type containerManager struct {
	docker        *dockerClient
	imageName     string
	containerName string
	dockerfilePath string
}

// newContainerManager creates a new container manager
func newContainerManager(dockerfilePath, imageName, containerName string) (*containerManager, error) {
	docker, err := newDockerClient()
	if err != nil {
		return nil, err
	}

	return &containerManager{
		docker:        docker,
		imageName:     imageName,
		containerName: containerName,
		dockerfilePath: dockerfilePath,
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
		fmt.Printf("Building image %s from %s...\n", cm.imageName, cm.dockerfilePath)
		if err := cm.docker.buildImage(cm.dockerfilePath, cm.imageName); err != nil {
			return err
		}
		fmt.Printf("Image %s built successfully\n", cm.imageName)
	}

	return nil
}

// startContainer starts a new container
func (cm *containerManager) startContainer() (string, error) {
	// Get the directory containing the Dockerfile
	dockerfileDir := filepath.Dir(cm.dockerfilePath)

	// Get absolute path of the Dockerfile directory
	absPath, err := filepath.Abs(dockerfileDir)
	if err != nil {
		return "", fmt.Errorf("failed to get absolute path of Dockerfile directory: %w", err)
	}

	// Create container
	config := &container.Config{
		Image:      cm.imageName,
		Tty:        true,
		OpenStdin:  true,
		WorkingDir: "/workspace",
	}

	hostConfig := &container.HostConfig{
		Binds: []string{
			fmt.Sprintf("%s:/workspace", absPath),
		},
		AutoRemove: false,
	}

	resp, err := cm.docker.client.ContainerCreate(
		cm.docker.ctx,
		config,
		hostConfig,
		nil,
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

// runCommand runs a command in the container
func (cm *containerManager) runCommand(command []string) error {
	// Check if container is already running
	running, err := cm.docker.isContainerRunning(cm.containerName)
	if err != nil {
		return err
	}

	var containerID string
	if !running {
		// Check if container exists but is stopped
		exists, err := cm.docker.containerExists(cm.containerName)
		if err != nil {
			return err
		}

		if exists {
			// Get container ID and start it
			containerID, err = cm.docker.getContainerID(cm.containerName)
			if err != nil {
				return err
			}
			fmt.Printf("Starting existing container %s...\n", cm.containerName)
			if err := cm.docker.client.ContainerStart(cm.docker.ctx, containerID, container.StartOptions{}); err != nil {
				return fmt.Errorf("failed to start container: %w", err)
			}
		} else {
			// Ensure image exists
			if err := cm.ensureImage(); err != nil {
				return err
			}

			// Start a new container
			fmt.Printf("Starting new container %s...\n", cm.containerName)
			containerID, err = cm.startContainer()
			if err != nil {
				return err
			}
		}
	} else {
		containerID, err = cm.docker.getContainerID(cm.containerName)
		if err != nil {
			return err
		}
	}

	// Calculate the working directory in the container
	workDir := "/workspace"

	// Get current working directory
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("failed to get current directory: %w", err)
	}

	// Get absolute path of Dockerfile directory
	dockerfileDir := filepath.Dir(cm.dockerfilePath)
	absDockerfileDir, err := filepath.Abs(dockerfileDir)
	if err != nil {
		return fmt.Errorf("failed to get absolute path of Dockerfile directory: %w", err)
	}

	// Calculate relative path from Dockerfile dir to current dir
	relPath, err := filepath.Rel(absDockerfileDir, cwd)
	if err != nil {
		return fmt.Errorf("failed to calculate relative path: %w", err)
	}

	// If we're in a subdirectory, use that in the container
	if relPath != "." && !filepath.IsAbs(relPath) && relPath != ".." && !filepath.HasPrefix(relPath, "..") {
		workDir = filepath.Join("/workspace", relPath)
	}

	// Check if stdin is a TTY
	isTTY := term.IsTerminal(os.Stdin.Fd())

	// Execute the command in the container
	execConfig := container.ExecOptions{
		Cmd:          command,
		AttachStdout: true,
		AttachStderr: true,
		AttachStdin:  true, // Always attach stdin
		Tty:          isTTY,
		WorkingDir:   workDir,
	}

	execResp, err := cm.docker.client.ContainerExecCreate(cm.docker.ctx, containerID, execConfig)
	if err != nil {
		return fmt.Errorf("failed to create exec: %w", err)
	}

	// Attach to the exec instance
	attachResp, err := cm.docker.client.ContainerExecAttach(cm.docker.ctx, execResp.ID, container.ExecStartOptions{
		Tty: isTTY,
	})
	if err != nil {
		return fmt.Errorf("failed to attach to exec: %w", err)
	}
	defer attachResp.Close()

	// Handle stdin/stdout based on TTY mode
	if isTTY {
		// TTY mode: use bidirectional connection
		go func() {
			_, _ = io.Copy(attachResp.Conn, os.Stdin)
			// Close write side when stdin closes
			if closer, ok := attachResp.Conn.(interface{ CloseWrite() error }); ok {
				closer.CloseWrite()
			}
		}()

		// Copy output from container
		_, err = io.Copy(os.Stdout, attachResp.Conn)
	} else {
		// Non-TTY mode: forward stdin and read output
		go func() {
			_, _ = io.Copy(attachResp.Conn, os.Stdin)
			// Close write side when stdin closes to propagate EOF
			if closer, ok := attachResp.Conn.(interface{ CloseWrite() error }); ok {
				closer.CloseWrite()
			}
		}()

		// Copy output from container
		_, err = io.Copy(os.Stdout, attachResp.Reader)
	}

	if err != nil && err != io.EOF {
		return fmt.Errorf("failed to read output: %w", err)
	}

	// Check exit code
	inspectResp, err := cm.docker.client.ContainerExecInspect(cm.docker.ctx, execResp.ID)
	if err != nil {
		return fmt.Errorf("failed to inspect exec: %w", err)
	}

	if inspectResp.ExitCode != 0 {
		return fmt.Errorf("command exited with code %d", inspectResp.ExitCode)
	}

	return nil
}

// stopContainer stops and removes the container
func (cm *containerManager) stopContainer() error {
	exists, err := cm.docker.containerExists(cm.containerName)
	if err != nil {
		return err
	}

	if !exists {
		fmt.Printf("Container %s does not exist\n", cm.containerName)
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

	fmt.Printf("Container %s stopped and removed\n", cm.containerName)
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
		fmt.Printf("Removing existing image %s...\n", cm.imageName)
		if err := cm.docker.removeImage(cm.imageName); err != nil {
			return err
		}
	}

	// Build the image
	fmt.Printf("Building image %s from %s...\n", cm.imageName, cm.dockerfilePath)
	if err := cm.docker.buildImage(cm.dockerfilePath, cm.imageName); err != nil {
		return err
	}

	fmt.Printf("Image %s built successfully\n", cm.imageName)
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
