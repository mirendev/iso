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
	"github.com/docker/docker/api/types/network"
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

// getArchitecture returns the architecture Docker is using for containers
func (d *dockerClient) getArchitecture() (string, error) {
	info, err := d.client.Info(d.ctx)
	if err != nil {
		return "", fmt.Errorf("failed to get Docker info: %w", err)
	}

	// Map Docker architecture names to our binary names
	switch info.Architecture {
	case "x86_64", "amd64":
		return "amd64", nil
	case "aarch64", "arm64":
		return "arm64", nil
	default:
		return "", fmt.Errorf("unsupported Docker architecture: %s", info.Architecture)
	}
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

// createNetwork creates a Docker network
func (d *dockerClient) createNetwork(networkName string) (string, error) {
	resp, err := d.client.NetworkCreate(d.ctx, networkName, network.CreateOptions{
		Driver: "bridge",
	})
	if err != nil {
		return "", fmt.Errorf("failed to create network: %w", err)
	}
	return resp.ID, nil
}

// networkExists checks if a Docker network exists
func (d *dockerClient) networkExists(networkName string) (bool, error) {
	networks, err := d.client.NetworkList(d.ctx, network.ListOptions{})
	if err != nil {
		return false, fmt.Errorf("failed to list networks: %w", err)
	}

	for _, net := range networks {
		if net.Name == networkName {
			return true, nil
		}
	}
	return false, nil
}

// removeNetwork removes a Docker network
func (d *dockerClient) removeNetwork(networkName string) error {
	err := d.client.NetworkRemove(d.ctx, networkName)
	if err != nil {
		return fmt.Errorf("failed to remove network: %w", err)
	}
	return nil
}

// pullImage pulls a Docker image from a registry
func (d *dockerClient) pullImage(imageName string) error {
	out, err := d.client.ImagePull(d.ctx, imageName, image.PullOptions{})
	if err != nil {
		return fmt.Errorf("failed to pull image: %w", err)
	}
	defer out.Close()

	// Stream the pull output
	_, err = io.Copy(os.Stdout, out)
	if err != nil {
		return fmt.Errorf("failed to read pull output: %w", err)
	}

	return nil
}
