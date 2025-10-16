package main

import (
	"fmt"
	"io"
	"os"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/image"
)

// ContainerManager handles container lifecycle operations
type ContainerManager struct {
	docker        *DockerClient
	imageName     string
	containerName string
	dockerfilePath string
}

// NewContainerManager creates a new container manager
func NewContainerManager(dockerfilePath, imageName, containerName string) (*ContainerManager, error) {
	docker, err := NewDockerClient()
	if err != nil {
		return nil, err
	}

	return &ContainerManager{
		docker:        docker,
		imageName:     imageName,
		containerName: containerName,
		dockerfilePath: dockerfilePath,
	}, nil
}

// Close closes the container manager and Docker client
func (cm *ContainerManager) Close() error {
	return cm.docker.Close()
}

// EnsureImage ensures the Docker image exists, building it if necessary
func (cm *ContainerManager) EnsureImage() error {
	exists, err := cm.docker.ImageExists(cm.imageName)
	if err != nil {
		return err
	}

	if !exists {
		fmt.Printf("Building image %s from %s...\n", cm.imageName, cm.dockerfilePath)
		if err := cm.docker.BuildImage(cm.dockerfilePath, cm.imageName); err != nil {
			return err
		}
		fmt.Printf("Image %s built successfully\n", cm.imageName)
	}

	return nil
}

// StartContainer starts a new container
func (cm *ContainerManager) StartContainer() (string, error) {
	// Get current working directory to mount
	cwd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("failed to get current directory: %w", err)
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
			fmt.Sprintf("%s:/workspace", cwd),
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

// RunCommand runs a command in the container
func (cm *ContainerManager) RunCommand(command []string) error {
	// Check if container is already running
	running, err := cm.docker.IsContainerRunning(cm.containerName)
	if err != nil {
		return err
	}

	var containerID string
	if !running {
		// Check if container exists but is stopped
		exists, err := cm.docker.ContainerExists(cm.containerName)
		if err != nil {
			return err
		}

		if exists {
			// Get container ID and start it
			containerID, err = cm.docker.GetContainerID(cm.containerName)
			if err != nil {
				return err
			}
			fmt.Printf("Starting existing container %s...\n", cm.containerName)
			if err := cm.docker.client.ContainerStart(cm.docker.ctx, containerID, container.StartOptions{}); err != nil {
				return fmt.Errorf("failed to start container: %w", err)
			}
		} else {
			// Ensure image exists
			if err := cm.EnsureImage(); err != nil {
				return err
			}

			// Start a new container
			fmt.Printf("Starting new container %s...\n", cm.containerName)
			containerID, err = cm.StartContainer()
			if err != nil {
				return err
			}
		}
	} else {
		containerID, err = cm.docker.GetContainerID(cm.containerName)
		if err != nil {
			return err
		}
	}

	// Execute the command in the container
	execConfig := container.ExecOptions{
		Cmd:          command,
		AttachStdout: true,
		AttachStderr: true,
		WorkingDir:   "/workspace",
	}

	execResp, err := cm.docker.client.ContainerExecCreate(cm.docker.ctx, containerID, execConfig)
	if err != nil {
		return fmt.Errorf("failed to create exec: %w", err)
	}

	// Attach to the exec instance
	attachResp, err := cm.docker.client.ContainerExecAttach(cm.docker.ctx, execResp.ID, container.ExecStartOptions{})
	if err != nil {
		return fmt.Errorf("failed to attach to exec: %w", err)
	}
	defer attachResp.Close()

	// Stream output
	_, err = io.Copy(os.Stdout, attachResp.Reader)
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

// StopContainer stops and removes the container
func (cm *ContainerManager) StopContainer() error {
	exists, err := cm.docker.ContainerExists(cm.containerName)
	if err != nil {
		return err
	}

	if !exists {
		fmt.Printf("Container %s does not exist\n", cm.containerName)
		return nil
	}

	containerID, err := cm.docker.GetContainerID(cm.containerName)
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

// RebuildImage rebuilds the Docker image
func (cm *ContainerManager) RebuildImage() error {
	// Check if image exists and remove it
	exists, err := cm.docker.ImageExists(cm.imageName)
	if err != nil {
		return err
	}

	if exists {
		fmt.Printf("Removing existing image %s...\n", cm.imageName)
		if err := cm.docker.RemoveImage(cm.imageName); err != nil {
			return err
		}
	}

	// Build the image
	fmt.Printf("Building image %s from %s...\n", cm.imageName, cm.dockerfilePath)
	if err := cm.docker.BuildImage(cm.dockerfilePath, cm.imageName); err != nil {
		return err
	}

	fmt.Printf("Image %s built successfully\n", cm.imageName)
	return nil
}

// GetStatus returns the status of the container
func (cm *ContainerManager) GetStatus() (string, error) {
	exists, err := cm.docker.ContainerExists(cm.containerName)
	if err != nil {
		return "", err
	}

	if !exists {
		return "Container does not exist", nil
	}

	running, err := cm.docker.IsContainerRunning(cm.containerName)
	if err != nil {
		return "", err
	}

	if running {
		return "Container is running", nil
	}

	return "Container exists but is stopped", nil
}

// PullImage pulls a Docker image from a registry
func (cm *ContainerManager) PullImage() error {
	out, err := cm.docker.client.ImagePull(cm.docker.ctx, cm.imageName, image.PullOptions{})
	if err != nil {
		return fmt.Errorf("failed to pull image: %w", err)
	}
	defer out.Close()

	_, err = io.Copy(os.Stdout, out)
	return err
}
