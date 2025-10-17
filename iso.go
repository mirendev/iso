// Package iso provides an isolated Docker environment for running tests and commands.
package iso

import (
	"fmt"
	"os"
	"path/filepath"
)

// Options configures the ISO client
type Options struct {
	// DockerfilePath is the path to the Dockerfile (used if ComposePath is empty)
	DockerfilePath string
	// ComposePath is the path to the docker-compose.yml file (takes precedence over DockerfilePath)
	ComposePath string
	// ServiceName is the name of the compose service to run commands in (required if ComposePath is set)
	ServiceName string
	// ImageName is the name of the Docker image (used only with Dockerfile)
	ImageName string
	// ContainerName is the name of the container
	ContainerName string
	// ProjectName is the name of the compose project (defaults to directory name)
	ProjectName string
}

// Client manages the isolated Docker environment
type Client struct {
	opts             Options
	containerManager *containerManager
	composeManager   *composeManager
	useCompose       bool
}

// findIsoDir searches upward from the current directory to find .iso directory
// Returns the path to .iso/docker-compose.yml and the project root directory
func findIsoDir() (composePath string, projectRoot string, found bool) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", "", false
	}

	dir := cwd
	for {
		isoDir := filepath.Join(dir, ".iso")
		composeFile := filepath.Join(isoDir, "docker-compose.yml")

		if _, err := os.Stat(composeFile); err == nil {
			return composeFile, dir, true
		}

		// Move up one directory
		parent := filepath.Dir(dir)
		if parent == dir {
			// Reached root directory
			break
		}
		dir = parent
	}

	return "", "", false
}

// New creates a new ISO client with the given options
func New(opts Options) (*Client, error) {
	client := &Client{opts: opts}

	// Auto-detect compose mode by searching upward for .iso/docker-compose.yml
	if opts.ComposePath == "" {
		if composePath, projectRoot, found := findIsoDir(); found {
			opts.ComposePath = composePath
			if opts.ProjectName == "" {
				opts.ProjectName = filepath.Base(projectRoot)
			}
		}
	}

	// Determine if using compose or dockerfile
	if opts.ComposePath != "" {
		// Using docker compose
		client.useCompose = true

		// Set default project name from compose file directory if not set
		if opts.ProjectName == "" {
			absPath, err := filepath.Abs(opts.ComposePath)
			if err != nil {
				return nil, fmt.Errorf("failed to get absolute path: %w", err)
			}
			// Project name is the directory containing .iso
			projectRoot := filepath.Dir(filepath.Dir(absPath))
			opts.ProjectName = filepath.Base(projectRoot)
		}

		cm, err := newComposeManager(opts.ComposePath, opts.ProjectName, opts.ServiceName)
		if err != nil {
			return nil, err
		}
		client.composeManager = cm
	} else {
		// Using Dockerfile
		if opts.DockerfilePath == "" {
			opts.DockerfilePath = "Dockerfile"
		}
		if opts.ImageName == "" {
			opts.ImageName = "iso-test-env"
		}
		if opts.ContainerName == "" {
			opts.ContainerName = "iso-test-container"
		}

		cm, err := newContainerManager(opts.DockerfilePath, opts.ImageName, opts.ContainerName)
		if err != nil {
			return nil, err
		}
		client.containerManager = cm
	}

	client.opts = opts
	return client, nil
}

// Close closes the client and releases resources
func (c *Client) Close() error {
	if c.useCompose {
		return c.composeManager.close()
	}
	return c.containerManager.close()
}

// Run executes a command in the isolated environment and returns the exit code
func (c *Client) Run(command []string) (int, error) {
	if len(command) == 0 {
		return 0, fmt.Errorf("no command specified")
	}

	if c.useCompose {
		return c.composeManager.runCommand(command)
	}

	// Check if Dockerfile exists
	if _, err := os.Stat(c.opts.DockerfilePath); os.IsNotExist(err) {
		return 0, fmt.Errorf("Dockerfile not found: %s", c.opts.DockerfilePath)
	}

	return c.containerManager.runCommand(command)
}

// Build ensures the Docker image exists, building it if necessary
func (c *Client) Build() error {
	if c.useCompose {
		return c.composeManager.buildStack()
	}

	// Check if Dockerfile exists
	if _, err := os.Stat(c.opts.DockerfilePath); os.IsNotExist(err) {
		return fmt.Errorf("Dockerfile not found: %s", c.opts.DockerfilePath)
	}

	return c.containerManager.ensureImage()
}

// Rebuild forces a rebuild of the Docker image
func (c *Client) Rebuild() error {
	if c.useCompose {
		return c.composeManager.rebuildStack()
	}

	// Check if Dockerfile exists
	if _, err := os.Stat(c.opts.DockerfilePath); os.IsNotExist(err) {
		return fmt.Errorf("Dockerfile not found: %s", c.opts.DockerfilePath)
	}

	return c.containerManager.rebuildImage()
}

// Stop stops and removes the container
func (c *Client) Stop() error {
	if c.useCompose {
		return c.composeManager.stopStack()
	}
	return c.containerManager.stopContainer()
}

// Status returns information about the image and container
type Status struct {
	ImageName      string
	ImageExists    bool
	ContainerName  string
	ContainerState string // "does not exist", "running", "stopped"
}

// Status returns the current status of the image and container
func (c *Client) Status() (*Status, error) {
	if c.useCompose {
		status := &Status{
			ImageName:     fmt.Sprintf("compose:%s", c.opts.ProjectName),
			ContainerName: c.composeManager.getContainerName(),
		}

		// For compose, we don't check image existence (compose manages that)
		status.ImageExists = true

		// Check container status
		containerStatus, err := c.composeManager.getStatus()
		if err != nil {
			return nil, err
		}
		status.ContainerState = containerStatus

		return status, nil
	}

	status := &Status{
		ImageName:     c.opts.ImageName,
		ContainerName: c.opts.ContainerName,
	}

	// Check image status
	imageExists, err := c.containerManager.docker.imageExists(c.opts.ImageName)
	if err != nil {
		return nil, err
	}
	status.ImageExists = imageExists

	// Check container status
	containerStatus, err := c.containerManager.getStatus()
	if err != nil {
		return nil, err
	}
	status.ContainerState = containerStatus

	return status, nil
}
