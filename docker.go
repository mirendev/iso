package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/client"
	"github.com/docker/docker/pkg/archive"
)

// DockerClient wraps the Docker API client
type DockerClient struct {
	client *client.Client
	ctx    context.Context
}

// NewDockerClient creates a new Docker client
func NewDockerClient() (*DockerClient, error) {
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return nil, fmt.Errorf("failed to create Docker client: %w", err)
	}

	return &DockerClient{
		client: cli,
		ctx:    context.Background(),
	}, nil
}

// Close closes the Docker client connection
func (d *DockerClient) Close() error {
	return d.client.Close()
}

// BuildImage builds a Docker image from a Dockerfile
func (d *DockerClient) BuildImage(dockerfilePath, imageName string) error {
	// Get the directory containing the Dockerfile
	buildContext := filepath.Dir(dockerfilePath)
	if buildContext == "" {
		buildContext = "."
	}

	// Create a tar archive of the build context
	tar, err := archive.TarWithOptions(buildContext, &archive.TarOptions{})
	if err != nil {
		return fmt.Errorf("failed to create build context: %w", err)
	}
	defer tar.Close()

	// Build the image
	opts := types.ImageBuildOptions{
		Tags:       []string{imageName},
		Dockerfile: filepath.Base(dockerfilePath),
		Remove:     true,
		Context:    tar,
	}

	resp, err := d.client.ImageBuild(d.ctx, tar, opts)
	if err != nil {
		return fmt.Errorf("failed to build image: %w", err)
	}
	defer resp.Body.Close()

	// Stream the build output
	_, err = io.Copy(os.Stdout, resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read build output: %w", err)
	}

	return nil
}

// ImageExists checks if a Docker image exists
func (d *DockerClient) ImageExists(imageName string) (bool, error) {
	_, _, err := d.client.ImageInspectWithRaw(d.ctx, imageName)
	if err != nil {
		if client.IsErrNotFound(err) {
			return false, nil
		}
		return false, fmt.Errorf("failed to inspect image: %w", err)
	}
	return true, nil
}

// ContainerExists checks if a container exists
func (d *DockerClient) ContainerExists(containerName string) (bool, error) {
	containers, err := d.client.ContainerList(d.ctx, container.ListOptions{
		All: true,
	})
	if err != nil {
		return false, fmt.Errorf("failed to list containers: %w", err)
	}

	for _, c := range containers {
		for _, name := range c.Names {
			// Docker prefixes names with '/'
			if name == "/"+containerName || name == containerName {
				return true, nil
			}
		}
	}
	return false, nil
}

// IsContainerRunning checks if a container is running
func (d *DockerClient) IsContainerRunning(containerName string) (bool, error) {
	containers, err := d.client.ContainerList(d.ctx, container.ListOptions{
		All: false, // Only running containers
	})
	if err != nil {
		return false, fmt.Errorf("failed to list containers: %w", err)
	}

	for _, c := range containers {
		for _, name := range c.Names {
			if name == "/"+containerName || name == containerName {
				return true, nil
			}
		}
	}
	return false, nil
}

// GetContainerID gets the container ID by name
func (d *DockerClient) GetContainerID(containerName string) (string, error) {
	containers, err := d.client.ContainerList(d.ctx, container.ListOptions{
		All: true,
	})
	if err != nil {
		return "", fmt.Errorf("failed to list containers: %w", err)
	}

	for _, c := range containers {
		for _, name := range c.Names {
			if name == "/"+containerName || name == containerName {
				return c.ID, nil
			}
		}
	}
	return "", fmt.Errorf("container not found: %s", containerName)
}

// RemoveImage removes a Docker image
func (d *DockerClient) RemoveImage(imageName string) error {
	_, err := d.client.ImageRemove(d.ctx, imageName, image.RemoveOptions{
		Force: true,
	})
	if err != nil {
		return fmt.Errorf("failed to remove image: %w", err)
	}
	return nil
}
