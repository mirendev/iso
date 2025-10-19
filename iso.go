// Package iso provides an isolated Docker environment for running tests and commands.
package iso

import (
	"fmt"
	"os"
	"path/filepath"
)

// Options configures the ISO client
type Options struct {
	// DockerfilePath is the path to the Dockerfile
	DockerfilePath string
	// ImageName is the name of the Docker image
	ImageName string
	// ContainerName is the name of the container
	ContainerName string
}

// Client manages the isolated Docker environment
type Client struct {
	opts             Options
	containerManager *containerManager
}

// New creates a new ISO client with the given options
func New(opts Options) (*Client, error) {
	// Auto-detect .iso directory if using default Dockerfile
	// If .iso directory exists, prefer .iso/Dockerfile over ./Dockerfile
	if opts.DockerfilePath == "" || opts.DockerfilePath == "Dockerfile" {
		isoDir, _, found := findIsoDir()
		if found {
			// Use Dockerfile from .iso directory
			opts.DockerfilePath = filepath.Join(isoDir, "Dockerfile")
		} else if opts.DockerfilePath == "" {
			// No .iso directory and no explicit path - use current directory default
			opts.DockerfilePath = "Dockerfile"
		}
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

	return &Client{
		opts:             opts,
		containerManager: cm,
	}, nil
}

// Close closes the client and releases resources
func (c *Client) Close() error {
	return c.containerManager.close()
}

// Run executes a command in the isolated environment and returns the exit code
func (c *Client) Run(command []string) (int, error) {
	if len(command) == 0 {
		return 0, fmt.Errorf("no command specified")
	}

	// Check if Dockerfile exists
	if _, err := os.Stat(c.opts.DockerfilePath); os.IsNotExist(err) {
		return 0, fmt.Errorf("Dockerfile not found: %s", c.opts.DockerfilePath)
	}

	return c.containerManager.runCommand(command)
}

// Start starts all services with verbose output
func (c *Client) Start() error {
	// Ensure image exists
	if err := c.containerManager.ensureImage(); err != nil {
		return err
	}

	// Ensure network exists
	if err := c.containerManager.ensureNetwork(); err != nil {
		return err
	}

	// Start all services first
	if err := c.containerManager.startAllServices(true); err != nil {
		return err
	}

	// Then start the main container
	id, err := c.containerManager.startContainer()
	if err != nil {
		return err
	}

	fmt.Printf("Started container %s with ID %s\n", c.containerManager.containerName, id)

	return nil
}

// Build ensures the Docker image exists, building it if necessary
func (c *Client) Build() error {
	// Check if Dockerfile exists
	if _, err := os.Stat(c.opts.DockerfilePath); os.IsNotExist(err) {
		return fmt.Errorf("Dockerfile not found: %s", c.opts.DockerfilePath)
	}

	return c.containerManager.ensureImage()
}

// Rebuild forces a rebuild of the Docker image
func (c *Client) Rebuild() error {
	// Check if Dockerfile exists
	if _, err := os.Stat(c.opts.DockerfilePath); os.IsNotExist(err) {
		return fmt.Errorf("Dockerfile not found: %s", c.opts.DockerfilePath)
	}

	return c.containerManager.rebuildImage()
}

// Stop stops and removes the container and all services
func (c *Client) Stop() error {
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
	status := &Status{
		ImageName:     c.containerManager.imageName,
		ContainerName: c.containerManager.containerName,
	}

	// Check image status
	imageExists, err := c.containerManager.docker.imageExists(c.containerManager.imageName)
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
