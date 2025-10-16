package iso

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/compose-spec/compose-go/loader"
	"github.com/compose-spec/compose-go/types"
	"github.com/docker/docker/api/types/container"
	"github.com/moby/term"
)

// composeManager handles docker compose operations
type composeManager struct {
	docker      *dockerClient
	composePath string
	projectName string
	serviceName string
	project     *types.Project
}

// newComposeManager creates a new compose manager
func newComposeManager(composePath, projectName, serviceName string) (*composeManager, error) {
	docker, err := newDockerClient()
	if err != nil {
		return nil, err
	}

	// Load and parse the compose file
	absPath, err := filepath.Abs(composePath)
	if err != nil {
		return nil, fmt.Errorf("failed to get absolute path of compose file: %w", err)
	}

	// Read the compose file
	composeData, err := os.ReadFile(absPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read compose file: %w", err)
	}

	// Set up loader config
	workingDir := filepath.Dir(absPath)
	if projectName == "" {
		projectName = filepath.Base(workingDir)
	}

	configFiles := []types.ConfigFile{
		{
			Filename: absPath,
			Content:  composeData,
		},
	}

	// Parse the compose file
	project, err := loader.Load(types.ConfigDetails{
		WorkingDir:  workingDir,
		ConfigFiles: configFiles,
		Environment: nil,
	}, func(options *loader.Options) {
		options.SetProjectName(projectName, true)
	})
	if err != nil {
		return nil, fmt.Errorf("failed to load compose file: %w", err)
	}

	// Validate that the service exists
	if serviceName != "" {
		found := false
		for _, service := range project.Services {
			if service.Name == serviceName {
				found = true
				break
			}
		}
		if !found {
			return nil, fmt.Errorf("service %q not found in compose file", serviceName)
		}
	} else if len(project.Services) > 0 {
		// Default to first service
		serviceName = project.Services[0].Name
	}

	return &composeManager{
		docker:      docker,
		composePath: absPath,
		projectName: projectName,
		serviceName: serviceName,
		project:     project,
	}, nil
}

// close closes the compose manager
func (cm *composeManager) close() error {
	return cm.docker.close()
}

// getContainerName returns the container name for the service
func (cm *composeManager) getContainerName() string {
	return fmt.Sprintf("%s-%s-1", cm.projectName, cm.serviceName)
}

// runCommand runs a command in the compose service
func (cm *composeManager) runCommand(command []string) (int, error) {
	// Check if container is running
	containerName := cm.getContainerName()
	running, err := cm.docker.isContainerRunning(containerName)
	if err != nil {
		return 0, err
	}

	var containerID string
	if !running {
		// Start the compose stack
		if err := cm.startStack(); err != nil {
			return 0, err
		}

		// Get the container ID
		containerID, err = cm.docker.getContainerID(containerName)
		if err != nil {
			return 0, err
		}
	} else {
		containerID, err = cm.docker.getContainerID(containerName)
		if err != nil {
			return 0, err
		}
	}

	// Calculate working directory
	workDir := "/workspace"
	cwd, err := os.Getwd()
	if err != nil {
		return 0, fmt.Errorf("failed to get current directory: %w", err)
	}

	composeDir := filepath.Dir(cm.composePath)
	absComposeDir, err := filepath.Abs(composeDir)
	if err != nil {
		return 0, fmt.Errorf("failed to get absolute path of compose directory: %w", err)
	}

	relPath, err := filepath.Rel(absComposeDir, cwd)
	if err != nil {
		return 0, fmt.Errorf("failed to calculate relative path: %w", err)
	}

	if relPath != "." && !filepath.IsAbs(relPath) && relPath != ".." && !filepath.HasPrefix(relPath, "..") {
		workDir = filepath.Join("/workspace", relPath)
	}

	// Check if stdin is a TTY
	isTTY := term.IsTerminal(os.Stdin.Fd())

	// Execute command in container
	execConfig := container.ExecOptions{
		Cmd:          command,
		AttachStdout: true,
		AttachStderr: true,
		AttachStdin:  true,
		Tty:          isTTY,
		WorkingDir:   workDir,
	}

	execResp, err := cm.docker.client.ContainerExecCreate(cm.docker.ctx, containerID, execConfig)
	if err != nil {
		return 0, fmt.Errorf("failed to create exec: %w", err)
	}

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
		if closer, ok := attachResp.Conn.(interface{ CloseWrite() error }); ok {
			closer.CloseWrite()
		}
	}()

	// Copy stdout/stderr in background
	outputDone := make(chan error, 1)
	go func() {
		var err error
		if isTTY {
			_, err = io.Copy(os.Stdout, attachResp.Conn)
		} else {
			_, err = io.Copy(os.Stdout, attachResp.Reader)
		}
		outputDone <- err
	}()

	// Wait for output to finish
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

// startStack starts the compose stack
func (cm *composeManager) startStack() error {
	fmt.Printf("Starting compose stack %s...\n", cm.projectName)

	// Start services using docker compose up
	// We'll use docker compose CLI via exec for now
	ctx := context.Background()
	composeCmd := []string{"docker", "compose", "-f", cm.composePath, "-p", cm.projectName, "up", "-d"}

	// Execute docker compose up
	cmd := execCommand(ctx, composeCmd[0], composeCmd[1:]...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to start compose stack: %w", err)
	}

	fmt.Printf("Compose stack %s started\n", cm.projectName)
	return nil
}

// stopStack stops the compose stack
func (cm *composeManager) stopStack() error {
	fmt.Printf("Stopping compose stack %s...\n", cm.projectName)

	ctx := context.Background()
	composeCmd := []string{"docker", "compose", "-f", cm.composePath, "-p", cm.projectName, "down"}

	cmd := execCommand(ctx, composeCmd[0], composeCmd[1:]...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to stop compose stack: %w", err)
	}

	fmt.Printf("Compose stack %s stopped\n", cm.projectName)
	return nil
}

// getStatus returns the status of the compose service container
func (cm *composeManager) getStatus() (string, error) {
	containerName := cm.getContainerName()
	exists, err := cm.docker.containerExists(containerName)
	if err != nil {
		return "", err
	}

	if !exists {
		return "does not exist", nil
	}

	running, err := cm.docker.isContainerRunning(containerName)
	if err != nil {
		return "", err
	}

	if running {
		return "running", nil
	}
	return "stopped", nil
}

// execCommand is a helper to create exec.CommandContext
func execCommand(ctx context.Context, name string, args ...string) *exec.Cmd {
	return exec.CommandContext(ctx, name, args...)
}
