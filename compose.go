package iso

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/compose-spec/compose-go/v2/cli"
	"github.com/compose-spec/compose-go/v2/types"
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

// getIsoBinaryPath returns the absolute path to the currently running iso binary
func getIsoBinaryPath() (string, error) {
	execPath, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("failed to get executable path: %w", err)
	}

	absPath, err := filepath.Abs(execPath)
	if err != nil {
		return "", fmt.Errorf("failed to get absolute path: %w", err)
	}

	// Resolve symlinks
	realPath, err := filepath.EvalSymlinks(absPath)
	if err != nil {
		return "", fmt.Errorf("failed to resolve symlinks: %w", err)
	}

	return realPath, nil
}

// setupComposeEnv sets up environment variables for docker compose commands
func setupComposeEnv(cmd *exec.Cmd) error {
	isoPath, err := getIsoBinaryPath()
	if err != nil {
		return err
	}

	// Copy current environment and add ISO variable
	cmd.Env = append(os.Environ(), fmt.Sprintf("ISO=%s", isoPath))
	return nil
}

// newComposeManager creates a new compose manager
func newComposeManager(composePath, projectName, serviceName string) (*composeManager, error) {
	docker, err := newDockerClient()
	if err != nil {
		return nil, err
	}

	// Set ISO environment variable for compose file parsing
	isoPath, err := getIsoBinaryPath()
	if err != nil {
		return nil, err
	}
	os.Setenv("ISO", isoPath)

	// Load and parse the compose file
	absPath, err := filepath.Abs(composePath)
	if err != nil {
		return nil, fmt.Errorf("failed to get absolute path of compose file: %w", err)
	}

	// Set up project name
	if projectName == "" {
		workingDir := filepath.Dir(absPath)
		projectName = filepath.Base(workingDir)
	}

	// Create project options
	ctx := context.Background()
	options, err := cli.NewProjectOptions(
		[]string{absPath},
		cli.WithOsEnv,
		cli.WithDotEnv,
		cli.WithName(projectName),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create project options: %w", err)
	}

	// Load the project
	project, err := options.LoadProject(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to load compose file: %w", err)
	}

	// Validate that the service exists or select default
	if serviceName != "" {
		// Check if specified service exists
		if _, found := project.Services[serviceName]; !found {
			return nil, fmt.Errorf("service %q not found in compose file", serviceName)
		}
	} else {
		// No service specified - look for "shell" service first
		if _, found := project.Services["shell"]; found {
			serviceName = "shell"
		} else if len(project.Services) > 0 {
			// If no "shell" service, pick the first one
			for name := range project.Services {
				serviceName = name
				break
			}
		} else {
			return nil, fmt.Errorf("no services found in compose file")
		}
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
		// Start the compose stack (silently)
		if err := cm.startStack(false); err != nil {
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

	// Get the project root (parent of .iso directory)
	composeDir := filepath.Dir(cm.composePath)       // .iso directory
	projectRoot := filepath.Dir(composeDir)          // parent of .iso
	absProjectRoot, err := filepath.Abs(projectRoot)
	if err != nil {
		return 0, fmt.Errorf("failed to get absolute path of project root: %w", err)
	}

	relPath, err := filepath.Rel(absProjectRoot, cwd)
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
// If verbose is true, output is shown to stdout/stderr in addition to being logged
func (cm *composeManager) startStack(verbose bool) error {
	// Create log file for startup output
	composeDir := filepath.Dir(cm.composePath)
	logPath := filepath.Join(composeDir, "startup.log")
	logFile, err := os.Create(logPath)
	if err != nil {
		return fmt.Errorf("failed to create startup log: %w", err)
	}
	defer logFile.Close()

	ctx := context.Background()
	cmd := exec.CommandContext(ctx, "docker", "compose", "-f", cm.composePath, "-p", cm.projectName, "up", "-d")

	if verbose {
		// Write to both log file and stdout/stderr
		cmd.Stdout = io.MultiWriter(logFile, os.Stdout)
		cmd.Stderr = io.MultiWriter(logFile, os.Stderr)
	} else {
		// Write only to log file
		cmd.Stdout = logFile
		cmd.Stderr = logFile
	}

	if err := setupComposeEnv(cmd); err != nil {
		return err
	}

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to start compose stack: %w", err)
	}

	return nil
}

// stopStack stops the compose stack
func (cm *composeManager) stopStack() error {
	fmt.Printf("Stopping compose stack %s...\n", cm.projectName)

	ctx := context.Background()
	cmd := exec.CommandContext(ctx, "docker", "compose", "-f", cm.composePath, "-p", cm.projectName, "down", "-t", "3")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := setupComposeEnv(cmd); err != nil {
		return err
	}

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to stop compose stack: %w", err)
	}

	fmt.Printf("Compose stack %s stopped\n", cm.projectName)
	return nil
}

// buildStack builds the compose stack images
func (cm *composeManager) buildStack() error {
	fmt.Printf("Building compose stack %s...\n", cm.projectName)

	ctx := context.Background()
	cmd := exec.CommandContext(ctx, "docker", "compose", "-f", cm.composePath, "-p", cm.projectName, "build")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := setupComposeEnv(cmd); err != nil {
		return err
	}

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to build compose stack: %w", err)
	}

	fmt.Printf("Compose stack %s built\n", cm.projectName)
	return nil
}

// rebuildStack rebuilds the compose stack images without cache
func (cm *composeManager) rebuildStack() error {
	fmt.Printf("Rebuilding compose stack %s (no cache)...\n", cm.projectName)

	ctx := context.Background()
	cmd := exec.CommandContext(ctx, "docker", "compose", "-f", cm.composePath, "-p", cm.projectName, "build", "--no-cache")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := setupComposeEnv(cmd); err != nil {
		return err
	}

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to rebuild compose stack: %w", err)
	}

	fmt.Printf("Compose stack %s rebuilt\n", cm.projectName)
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
