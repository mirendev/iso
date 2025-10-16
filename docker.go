package iso

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

// dockerClient wraps the Docker API client
type dockerClient struct {
	client *client.Client
	ctx    context.Context
}

// newDockerClient creates a new Docker client
func newDockerClient() (*dockerClient, error) {
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return nil, fmt.Errorf("failed to create Docker client: %w", err)
	}

	return &dockerClient{
		client: cli,
		ctx:    context.Background(),
	}, nil
}

// close closes the Docker client connection
func (d *dockerClient) close() error {
	return d.client.Close()
}

// buildImage builds a Docker image from a Dockerfile
func (d *dockerClient) buildImage(dockerfilePath, imageName string) error {
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

// imageExists checks if a Docker image exists
func (d *dockerClient) imageExists(imageName string) (bool, error) {
	_, _, err := d.client.ImageInspectWithRaw(d.ctx, imageName)
	if err != nil {
		if client.IsErrNotFound(err) {
			return false, nil
		}
		return false, fmt.Errorf("failed to inspect image: %w", err)
	}
	return true, nil
}

// containerExists checks if a container exists
func (d *dockerClient) containerExists(containerName string) (bool, error) {
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

// isContainerRunning checks if a container is running
func (d *dockerClient) isContainerRunning(containerName string) (bool, error) {
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

// getContainerID gets the container ID by name
func (d *dockerClient) getContainerID(containerName string) (string, error) {
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

// removeImage removes a Docker image
func (d *dockerClient) removeImage(imageName string) error {
	_, err := d.client.ImageRemove(d.ctx, imageName, image.RemoveOptions{
		Force: true,
	})
	if err != nil {
		return fmt.Errorf("failed to remove image: %w", err)
	}
	return nil
}
