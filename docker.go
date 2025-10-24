package iso

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/docker/docker/api/types/build"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/api/types/volume"
	"github.com/docker/docker/client"
	"github.com/moby/go-archive"
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
	buildContext := filepath.Dir(filepath.Dir(dockerfilePath))
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
	opts := build.ImageBuildOptions{
		Tags:       []string{imageName},
		Dockerfile: filepath.Join(".iso", filepath.Base(dockerfilePath)),
		Remove:     true,
		Context:    tar,
	}

	resp, err := d.client.ImageBuild(d.ctx, tar, opts)
	if err != nil {
		return fmt.Errorf("failed to build image: %w", err)
	}
	defer resp.Body.Close()

	// Parse and display build output
	type buildMessage struct {
		Stream      string `json:"stream"`
		Error       string `json:"error"`
		ErrorDetail struct {
			Message string `json:"message"`
		} `json:"errorDetail"`
	}

	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		var msg buildMessage
		if err := json.Unmarshal(scanner.Bytes(), &msg); err != nil {
			// If we can't parse JSON, just print the raw line
			fmt.Println(scanner.Text())
			continue
		}

		// Handle errors
		if msg.Error != "" {
			return fmt.Errorf("build failed: %s", msg.Error)
		}

		// Print stream output (build steps, etc.)
		if msg.Stream != "" {
			// Trim trailing newlines since fmt.Print will add one
			output := strings.TrimSuffix(msg.Stream, "\n")
			if output != "" {
				fmt.Println(output)
			}
		}
	}

	if err := scanner.Err(); err != nil {
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

	// Parse and display pull output
	type pullMessage struct {
		Status         string `json:"status"`
		Progress       string `json:"progress"`
		ProgressDetail struct {
			Current int64 `json:"current"`
			Total   int64 `json:"total"`
		} `json:"progressDetail"`
		ID    string `json:"id"`
		Error string `json:"error"`
	}

	scanner := bufio.NewScanner(out)
	lastStatus := ""

	for scanner.Scan() {
		var msg pullMessage
		if err := json.Unmarshal(scanner.Bytes(), &msg); err != nil {
			// If we can't parse JSON, just print the raw line
			fmt.Println(scanner.Text())
			continue
		}

		// Handle errors
		if msg.Error != "" {
			return fmt.Errorf("pull failed: %s", msg.Error)
		}

		// Display status updates (avoid repeating the same status)
		if msg.Status != "" {
			statusLine := msg.Status
			if msg.ID != "" {
				statusLine = msg.ID + ": " + statusLine
			}
			if msg.Progress != "" {
				statusLine += " " + msg.Progress
			}

			// Only print if status changed or has progress info
			if statusLine != lastStatus || msg.Progress != "" {
				fmt.Println(statusLine)
				lastStatus = statusLine
			}
		}
	}

	if err := scanner.Err(); err != nil {
		return fmt.Errorf("failed to read pull output: %w", err)
	}

	return nil
}

// createVolume creates a Docker volume
func (d *dockerClient) createVolume(volumeName string) error {
	_, err := d.client.VolumeCreate(d.ctx, volume.CreateOptions{
		Name: volumeName,
	})
	if err != nil {
		return fmt.Errorf("failed to create volume: %w", err)
	}
	return nil
}

// volumeExists checks if a Docker volume exists
func (d *dockerClient) volumeExists(volumeName string) (bool, error) {
	_, err := d.client.VolumeInspect(d.ctx, volumeName)
	if err != nil {
		if client.IsErrNotFound(err) {
			return false, nil
		}
		return false, fmt.Errorf("failed to inspect volume: %w", err)
	}
	return true, nil
}

// removeVolume removes a Docker volume
func (d *dockerClient) removeVolume(volumeName string) error {
	err := d.client.VolumeRemove(d.ctx, volumeName, true)
	if err != nil {
		return fmt.Errorf("failed to remove volume: %w", err)
	}
	return nil
}
