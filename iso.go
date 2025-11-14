// Package iso provides an isolated Docker environment for running tests and commands.
package iso

import (
	"bytes"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/docker/docker/api/types/container"
)

// Client manages the isolated Docker environment
type Client struct {
	containerManager *containerManager
}

// New creates a new ISO client with the specified session
// If session is empty, it defaults to "default"
func New(session string) (*Client, error) {
	cm, err := newContainerManager(session)
	if err != nil {
		return nil, err
	}

	return &Client{
		containerManager: cm,
	}, nil
}

// Close closes the client and releases resources
func (c *Client) Close() error {
	return c.containerManager.close()
}

// Run executes a command in the isolated environment and returns the exit code
// envVars is a slice of environment variables in KEY=VALUE format
func (c *Client) Run(command []string, envVars []string) (int, error) {
	if len(command) == 0 {
		return 0, fmt.Errorf("no command specified")
	}

	return c.containerManager.runCommand(command, envVars)
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

	slog.Info("started container", "container", c.containerManager.containerName, "id", id)

	return nil
}

// Build ensures the Docker image exists, building it if necessary
func (c *Client) Build() error {
	return c.containerManager.ensureImage()
}

// Rebuild forces a rebuild of the Docker image
func (c *Client) Rebuild() error {
	return c.containerManager.rebuildImage()
}

// Reset stops and removes the container but keeps services and volumes running
func (c *Client) Reset() error {
	return c.containerManager.resetContainer()
}

// Stop stops and removes the container and all services
func (c *Client) Stop() error {
	return c.containerManager.stopContainer()
}

// Prune removes all cache volumes for the project
func (c *Client) Prune() error {
	return c.containerManager.pruneCacheVolumes()
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

// IsoContainer represents an ISO-managed container
type IsoContainer struct {
	ID          string
	Name        string
	ShortName   string
	ProjectName string
	ProjectDir  string
	Session     string
	Status      string
	IsService   bool
	ServiceName string
}

// OrphanedSession represents a session whose project directory no longer exists
type OrphanedSession struct {
	ProjectDir  string
	ProjectName string
	Session     string
	Containers  []IsoContainer
}

// List returns all ISO-managed containers
func (c *Client) List() ([]IsoContainer, error) {
	dockerContainers, err := c.containerManager.docker.listIsoContainers()
	if err != nil {
		return nil, err
	}

	// Convert internal type to public type
	result := make([]IsoContainer, len(dockerContainers))
	for i, dc := range dockerContainers {
		result[i] = IsoContainer{
			ID:          dc.ID,
			Name:        dc.Name,
			ShortName:   dc.ShortName,
			ProjectName: dc.ProjectName,
			ProjectDir:  dc.ProjectDir,
			Session:     dc.Session,
			Status:      dc.Status,
			IsService:   dc.IsService,
			ServiceName: dc.ServiceName,
		}
	}

	return result, nil
}

// ListAll returns all ISO-managed containers across all projects
// This function does not require being in a project directory
func ListAll() ([]IsoContainer, error) {
	docker, err := newDockerClient()
	if err != nil {
		return nil, err
	}
	defer docker.close()

	dockerContainers, err := docker.listIsoContainers()
	if err != nil {
		return nil, err
	}

	// Convert internal type to public type
	result := make([]IsoContainer, len(dockerContainers))
	for i, dc := range dockerContainers {
		result[i] = IsoContainer{
			ID:          dc.ID,
			Name:        dc.Name,
			ShortName:   dc.ShortName,
			ProjectName: dc.ProjectName,
			ProjectDir:  dc.ProjectDir,
			Session:     dc.Session,
			Status:      dc.Status,
			IsService:   dc.IsService,
			ServiceName: dc.ServiceName,
		}
	}

	return result, nil
}

// ListOrphaned returns all ISO sessions whose project directories no longer exist
func ListOrphaned() ([]OrphanedSession, error) {
	containers, err := ListAll()
	if err != nil {
		return nil, err
	}

	// Group by project directory + session
	type sessionKey struct {
		ProjectDir string
		Session    string
	}
	sessions := make(map[sessionKey][]IsoContainer)

	for _, c := range containers {
		key := sessionKey{c.ProjectDir, c.Session}
		sessions[key] = append(sessions[key], c)
	}

	// Check which directories don't exist
	var orphaned []OrphanedSession
	for key, containers := range sessions {
		if _, err := os.Stat(key.ProjectDir); os.IsNotExist(err) {
			orphaned = append(orphaned, OrphanedSession{
				ProjectDir:  key.ProjectDir,
				ProjectName: containers[0].ProjectName,
				Session:     key.Session,
				Containers:  containers,
			})
		}
	}

	return orphaned, nil
}

// CleanupOrphaned stops and removes all orphaned sessions
// Returns the number of containers cleaned up
func CleanupOrphaned(dryRun bool) (int, error) {
	orphaned, err := ListOrphaned()
	if err != nil {
		return 0, err
	}

	if len(orphaned) == 0 {
		return 0, nil
	}

	docker, err := newDockerClient()
	if err != nil {
		return 0, err
	}
	defer docker.close()

	totalContainers := 0
	networksToRemove := make(map[string]bool)

	for _, session := range orphaned {
		slog.Info("cleaning up orphaned session",
			"project", session.ProjectName,
			"session", session.Session,
			"dir", session.ProjectDir,
			"containers", len(session.Containers))

		if dryRun {
			totalContainers += len(session.Containers)
			continue
		}

		// Stop and remove each container
		timeout := 10
		for _, c := range session.Containers {
			// Stop the container
			if err := docker.client.ContainerStop(docker.ctx, c.ID, container.StopOptions{
				Timeout: &timeout,
			}); err != nil {
				errStr := err.Error()
				if !strings.Contains(errStr, "already in progress") && !strings.Contains(errStr, "No such container") {
					slog.Warn("failed to stop container", "name", c.Name, "error", err)
				}
			}

			// Remove the container
			if err := docker.client.ContainerRemove(docker.ctx, c.ID, container.RemoveOptions{}); err != nil {
				errStr := err.Error()
				if !strings.Contains(errStr, "already in progress") && !strings.Contains(errStr, "No such container") {
					slog.Warn("failed to remove container", "name", c.Name, "error", err)
				}
			}

			totalContainers++
		}

		// Track network to remove
		if session.Session == "default" {
			networksToRemove[fmt.Sprintf("%s-network", session.ProjectName)] = true
		} else {
			networksToRemove[fmt.Sprintf("%s-%s-network", session.ProjectName, session.Session)] = true
		}
	}

	if !dryRun && len(networksToRemove) > 0 {
		// Give Docker a moment to clean up container endpoints
		time.Sleep(100 * time.Millisecond)

		// Remove networks
		for networkName := range networksToRemove {
			if err := docker.removeNetwork(networkName); err != nil {
				if !strings.Contains(err.Error(), "not found") {
					slog.Warn("failed to remove network", "network", networkName, "error", err)
				}
			}
		}
	}

	return totalContainers, nil
}

// CleanupOrphanedSessions stops and removes specific orphaned sessions
// Returns the number of containers cleaned up
func CleanupOrphanedSessions(sessions []OrphanedSession, dryRun bool) (int, error) {
	if len(sessions) == 0 {
		return 0, nil
	}

	docker, err := newDockerClient()
	if err != nil {
		return 0, err
	}
	defer docker.close()

	totalContainers := 0
	networksToRemove := make(map[string]bool)

	for _, session := range sessions {
		slog.Info("cleaning up orphaned session",
			"project", session.ProjectName,
			"session", session.Session,
			"dir", session.ProjectDir,
			"containers", len(session.Containers))

		if dryRun {
			totalContainers += len(session.Containers)
			continue
		}

		// Stop and remove each container
		timeout := 10
		for _, c := range session.Containers {
			// Stop the container
			if err := docker.client.ContainerStop(docker.ctx, c.ID, container.StopOptions{
				Timeout: &timeout,
			}); err != nil {
				errStr := err.Error()
				if !strings.Contains(errStr, "already in progress") && !strings.Contains(errStr, "No such container") {
					slog.Warn("failed to stop container", "name", c.Name, "error", err)
				}
			}

			// Remove the container
			if err := docker.client.ContainerRemove(docker.ctx, c.ID, container.RemoveOptions{}); err != nil {
				errStr := err.Error()
				if !strings.Contains(errStr, "already in progress") && !strings.Contains(errStr, "No such container") {
					slog.Warn("failed to remove container", "name", c.Name, "error", err)
				}
			}

			totalContainers++
		}

		// Track network to remove
		if session.Session == "default" {
			networksToRemove[fmt.Sprintf("%s-network", session.ProjectName)] = true
		} else {
			networksToRemove[fmt.Sprintf("%s-%s-network", session.ProjectName, session.Session)] = true
		}
	}

	if !dryRun && len(networksToRemove) > 0 {
		// Give Docker a moment to clean up container endpoints
		time.Sleep(100 * time.Millisecond)

		// Remove networks
		for networkName := range networksToRemove {
			if err := docker.removeNetwork(networkName); err != nil {
				if !strings.Contains(err.Error(), "not found") {
					slog.Warn("failed to remove network", "network", networkName, "error", err)
				}
			}
		}
	}

	return totalContainers, nil
}

// StopAll stops and removes all ISO-managed containers and networks across all projects
// This function does not require being in a project directory
func StopAll() error {
	// Get all ISO containers
	containers, err := ListAll()
	if err != nil {
		return err
	}

	if len(containers) == 0 {
		slog.Info("no ISO containers to stop")
		return nil
	}

	// Create Docker client
	docker, err := newDockerClient()
	if err != nil {
		return err
	}
	defer docker.close()

	// Group containers by project name to handle networks
	projectNetworks := make(map[string]bool)

	// Stop and remove all containers
	for _, c := range containers {
		slog.Info("stopping container", "name", c.Name, "project", c.ProjectName)

		// Get full container ID
		containerID, err := docker.getContainerID(c.Name)
		if err != nil {
			slog.Warn("failed to get container ID", "name", c.Name, "error", err)
			continue
		}

		// Stop the container
		timeout := 10
		if err := docker.client.ContainerStop(docker.ctx, containerID, container.StopOptions{
			Timeout: &timeout,
		}); err != nil {
			// Ignore "already in progress" and "no such container" errors
			errStr := err.Error()
			if !strings.Contains(errStr, "already in progress") && !strings.Contains(errStr, "No such container") {
				slog.Warn("failed to stop container", "name", c.Name, "error", err)
			}
		}

		// Remove the container
		if err := docker.client.ContainerRemove(docker.ctx, containerID, container.RemoveOptions{}); err != nil {
			// Ignore "already in progress" and "no such container" errors - AutoRemove containers remove themselves
			errStr := err.Error()
			if !strings.Contains(errStr, "already in progress") && !strings.Contains(errStr, "No such container") {
				slog.Warn("failed to remove container", "name", c.Name, "error", err)
			}
		}

		// Track project networks to remove later
		projectNetworks[c.ProjectName] = true
	}

	// Give Docker a moment to clean up container endpoints before removing networks
	time.Sleep(100 * time.Millisecond)

	// Remove all project networks
	for projectName := range projectNetworks {
		networkName := fmt.Sprintf("%s-network", projectName)
		if err := docker.removeNetwork(networkName); err != nil {
			// Ignore "not found" errors - network was already removed
			if !strings.Contains(err.Error(), "not found") {
				slog.Warn("failed to remove network", "network", networkName, "error", err)
			}
		}
	}

	slog.Info("stopped all ISO containers", "count", len(containers))
	return nil
}

// StopAllSessions stops and removes all sessions for the current project
// This function requires being in a project directory
func StopAllSessions() error {
	// Find .iso directory to get project name
	_, projectRoot, found := findIsoDir()
	if !found {
		return fmt.Errorf("no .iso directory found - please create one with a Dockerfile and optional services.yml")
	}

	projectName := filepath.Base(projectRoot)

	// Create Docker client
	docker, err := newDockerClient()
	if err != nil {
		return err
	}
	defer docker.close()

	// Get all containers for this project across all sessions
	containers, err := docker.listProjectContainersAllSessions(projectName)
	if err != nil {
		return err
	}

	if len(containers) == 0 {
		slog.Info("no containers to stop", "project", projectName)
		return nil
	}

	// Track sessions and networks to remove
	sessionNetworks := make(map[string]bool)

	// Stop and remove all containers
	timeout := 10
	for _, c := range containers {
		slog.Info("stopping container", "name", c.Name, "session", c.Session)

		// Stop the container
		if err := docker.client.ContainerStop(docker.ctx, c.ID, container.StopOptions{
			Timeout: &timeout,
		}); err != nil {
			// Ignore "already in progress" and "no such container" errors
			errStr := err.Error()
			if !strings.Contains(errStr, "already in progress") && !strings.Contains(errStr, "No such container") {
				slog.Warn("failed to stop container", "name", c.Name, "error", err)
			}
		}

		// Remove the container
		if err := docker.client.ContainerRemove(docker.ctx, c.ID, container.RemoveOptions{}); err != nil {
			// Ignore "already in progress" and "no such container" errors - AutoRemove containers remove themselves
			errStr := err.Error()
			if !strings.Contains(errStr, "already in progress") && !strings.Contains(errStr, "No such container") {
				slog.Warn("failed to remove container", "name", c.Name, "error", err)
			}
		}

		// Track session networks to remove later
		if c.Session == "default" {
			sessionNetworks[fmt.Sprintf("%s-network", projectName)] = true
		} else {
			sessionNetworks[fmt.Sprintf("%s-%s-network", projectName, c.Session)] = true
		}
	}

	// Give Docker a moment to clean up container endpoints before removing networks
	time.Sleep(100 * time.Millisecond)

	// Remove all session networks
	for networkName := range sessionNetworks {
		if err := docker.removeNetwork(networkName); err != nil {
			// Ignore "not found" errors - network was already removed
			if !strings.Contains(err.Error(), "not found") {
				slog.Warn("failed to remove network", "network", networkName, "error", err)
			}
		}
	}

	slog.Info("stopped all sessions for project", "project", projectName, "count", len(containers))
	return nil
}

// InitProject initializes a new .iso directory with AI-generated configuration
func InitProject() error {
	// Get current directory
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("failed to get current directory: %w", err)
	}

	// Check if .iso directory already exists
	isoDir := filepath.Join(cwd, ".iso")
	if _, err := os.Stat(isoDir); err == nil {
		return fmt.Errorf(".iso directory already exists")
	}

	slog.Info("analyzing project to generate ISO configuration")

	// Prepare the prompt for Claude
	prompt := `You are helping initialize an ISO (Isolated Docker Environment) project.

Analyze the current project directory and generate:

1. A Dockerfile for .iso/Dockerfile that:
   - Uses an appropriate base image for this project type
   - Installs necessary dependencies and tools
   - Sets WORKDIR to /workspace
   - Does NOT copy project files (they will be mounted)

2. A services.yml file for .iso/services.yml IF this project needs additional services (databases, caches, etc.):
   - Only include services that are actually needed based on the project
   - Include appropriate environment variables
   - Include port numbers for readiness checks
   - If no services are needed, respond with "NO_SERVICES_NEEDED"

Example services.yml format:
services:
  mysql:
    image: mysql:8.0
    port: 3306
    environment:
      MYSQL_ROOT_PASSWORD: rootpass
      MYSQL_DATABASE: testdb
      MYSQL_USER: testuser
      MYSQL_PASSWORD: testpass
  redis:
    image: redis:7-alpine
    port: 6379

Please respond with EXACTLY this format:
===DOCKERFILE===
<dockerfile content>
===END_DOCKERFILE===

===SERVICES===
<services.yml content OR "NO_SERVICES_NEEDED">
===END_SERVICES===

Be concise and practical. Focus on what this specific project needs.`

	// Call claude CLI with --print mode
	cmd := exec.Command("claude", "--print", prompt)
	cmd.Dir = cwd

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to run claude: %w\nStderr: %s", err, stderr.String())
	}

	response := stdout.String()

	// Parse the response
	dockerfile, services, err := parseInitResponse(response)
	if err != nil {
		return fmt.Errorf("failed to parse claude response: %w", err)
	}

	// Create .iso directory
	if err := os.Mkdir(isoDir, 0755); err != nil {
		return fmt.Errorf("failed to create .iso directory: %w", err)
	}

	// Write Dockerfile
	dockerfilePath := filepath.Join(isoDir, "Dockerfile")
	if err := os.WriteFile(dockerfilePath, []byte(dockerfile), 0644); err != nil {
		return fmt.Errorf("failed to write Dockerfile: %w", err)
	}
	slog.Info("created Dockerfile", "path", dockerfilePath)

	// Write services.yml if needed
	if services != "" {
		servicesPath := filepath.Join(isoDir, "services.yml")
		if err := os.WriteFile(servicesPath, []byte(services), 0644); err != nil {
			return fmt.Errorf("failed to write services.yml: %w", err)
		}
		slog.Info("created services.yml", "path", servicesPath)
	} else {
		slog.Info("no services needed for this project")
	}

	slog.Info("ISO project initialized successfully")
	fmt.Println("\nNext steps:")
	fmt.Println("  1. Review .iso/Dockerfile and adjust if needed")
	if services != "" {
		fmt.Println("  2. Review .iso/services.yml and adjust if needed")
	}
	fmt.Println("  3. Run 'iso build' to build the Docker image")
	fmt.Println("  4. Run 'iso run <command>' to execute commands in the isolated environment")

	return nil
}

// parseInitResponse parses the Claude response to extract Dockerfile and services.yml
func parseInitResponse(response string) (dockerfile, services string, err error) {
	// Extract Dockerfile
	dockerfileStart := strings.Index(response, "===DOCKERFILE===")
	dockerfileEnd := strings.Index(response, "===END_DOCKERFILE===")
	if dockerfileStart == -1 || dockerfileEnd == -1 {
		return "", "", fmt.Errorf("invalid response format: missing Dockerfile markers")
	}
	dockerfile = strings.TrimSpace(response[dockerfileStart+len("===DOCKERFILE===") : dockerfileEnd])

	// Extract services
	servicesStart := strings.Index(response, "===SERVICES===")
	servicesEnd := strings.Index(response, "===END_SERVICES===")
	if servicesStart == -1 || servicesEnd == -1 {
		return "", "", fmt.Errorf("invalid response format: missing services markers")
	}
	servicesContent := strings.TrimSpace(response[servicesStart+len("===SERVICES===") : servicesEnd])

	if servicesContent != "NO_SERVICES_NEEDED" {
		services = servicesContent
	}

	return dockerfile, services, nil
}
