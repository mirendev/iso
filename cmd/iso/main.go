package main

import (
	"crypto/rand"
	_ "embed"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"miren.dev/iso"
	"miren.dev/mflags"
	"miren.dev/trifle"
)

//go:embed agent-help.md
var agentHelpContent string

// ExitError carries an exit code
type ExitError struct {
	Code int
}

func (e *ExitError) Error() string {
	return fmt.Sprintf("exit code %d", e.Code)
}

func main() {

	level := slog.LevelInfo
	if lvlStr, ok := os.LookupEnv("DEBUG"); ok && lvlStr != "0" {
		level = slog.LevelDebug
	}

	// Set up slog with trifle
	slog.SetDefault(slog.New(trifle.New(os.Stderr, &slog.HandlerOptions{
		Level: level,
	})))

	if err := run(); err != nil {
		var exitErr *ExitError
		if errors.As(err, &exitErr) {
			os.Exit(exitErr.Code)
		}
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	dispatcher := mflags.NewDispatcher("iso")

	// Register commands
	registerRunCommand(dispatcher)
	registerBuildCommand(dispatcher)
	registerStartCommand(dispatcher)
	registerStopCommand(dispatcher)
	registerResetCommand(dispatcher)
	registerStatusCommand(dispatcher)
	registerListCommand(dispatcher)
	registerPruneCommand(dispatcher)
	registerInitCommand(dispatcher)
	registerInternalInitCommand(dispatcher)
	registerInEnvCommand(dispatcher)
	registerAgentHelpCommand(dispatcher)

	// Execute the dispatcher
	return dispatcher.Execute(os.Args[1:])
}

// getSession returns the session name and whether it's ephemeral
// If no session is specified, returns an ephemeral session ID
func getSession(flagValue string) (string, bool) {
	if flagValue != "" {
		return flagValue, false
	}
	if envSession := os.Getenv("ISO_SESSION"); envSession != "" {
		return envSession, false
	}

	buf := make([]byte, 8)
	rand.Read(buf)

	suffix := base64.RawURLEncoding.EncodeToString(buf)

	// No session specified - create ephemeral session
	ephemeralID := "eph-" + suffix
	return ephemeralID, true
}

// isValidEnvVarName checks if a string is a valid environment variable name
// Valid names contain only uppercase letters, lowercase letters, digits, and underscores
// and must start with a letter or underscore
func isValidEnvVarName(name string) bool {
	if len(name) == 0 {
		return false
	}

	// First character must be a letter or underscore
	firstChar := name[0]
	if !((firstChar >= 'a' && firstChar <= 'z') || (firstChar >= 'A' && firstChar <= 'Z') || firstChar == '_') {
		return false
	}

	// Remaining characters must be letters, digits, or underscores
	for i := 1; i < len(name); i++ {
		c := name[i]
		if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '_') {
			return false
		}
	}

	return true
}

// registerRunCommand registers the 'run' command
func registerRunCommand(dispatcher *mflags.Dispatcher) {
	fs := mflags.NewFlagSet("run")

	session := fs.String("session", 's', "", "Session name (default: ISO_SESSION env var or ephemeral)")

	// Allow unknown flags to pass through to the command
	fs.AllowUnknownFlags(true)

	handler := func(fs *mflags.FlagSet, args []string) error {
		// Combine positional args and unknown flags to form the command
		// Unknown flags come after positional args
		command := append(args, fs.UnknownFlags()...)

		// Parse environment variables from the command
		// Environment variables are KEY=VALUE at the start of the command
		var envVars []string
		var actualCommand []string

		for i, arg := range command {
			// Check if this looks like an environment variable (KEY=VALUE)
			if strings.Contains(arg, "=") && !strings.HasPrefix(arg, "-") {
				// Additional validation: check if it's at the start or after other env vars
				// and the part before = looks like a valid variable name
				parts := strings.SplitN(arg, "=", 2)
				if len(parts) == 2 && isValidEnvVarName(parts[0]) {
					envVars = append(envVars, arg)
					continue
				}
			}
			// Everything else is part of the actual command
			actualCommand = command[i:]
			break
		}

		sessionName, isEphemeral := getSession(*session)
		client, err := iso.New(sessionName)
		if err != nil {
			return err
		}
		defer client.Close()

		exitCode, err := client.Run(actualCommand, envVars)

		// If ephemeral session, clean up everything after run
		if isEphemeral {
			if stopErr := client.Stop(); stopErr != nil {
				slog.Warn("failed to clean up ephemeral session", "error", stopErr)
			}
		}

		if err != nil {
			return err
		}

		if exitCode != 0 {
			return &ExitError{Code: exitCode}
		}

		return nil
	}

	cmd := mflags.NewCommand(fs, handler,
		mflags.WithUsage("Run a command in the isolated environment"),
	)

	dispatcher.Dispatch("run", cmd)
}

// registerBuildCommand registers the 'build' command
func registerBuildCommand(dispatcher *mflags.Dispatcher) {
	fs := mflags.NewFlagSet("build")

	rebuild := fs.Bool("rebuild", 'r', false, "Force rebuild even if image exists")
	session := fs.String("session", 's', "", "Session name (default: ISO_SESSION env var or ephemeral)")

	handler := func(fs *mflags.FlagSet, args []string) error {
		doRebuild := *rebuild

		sessionName, _ := getSession(*session)
		client, err := iso.New(sessionName)
		if err != nil {
			return err
		}
		defer client.Close()

		if doRebuild {
			return client.Rebuild()
		}
		return client.Build()
	}

	cmd := mflags.NewCommand(fs, handler,
		mflags.WithUsage("Build the Docker image"),
	)

	dispatcher.Dispatch("build", cmd)
}

// registerStartCommand registers the 'start' command
func registerStartCommand(dispatcher *mflags.Dispatcher) {
	fs := mflags.NewFlagSet("start")

	session := fs.String("session", 's', "", "Session name (required, or use ISO_SESSION env var)")

	handler := func(fs *mflags.FlagSet, args []string) error {
		// For start command, session is required
		var sessionName string
		if *session != "" {
			sessionName = *session
		} else if envSession := os.Getenv("ISO_SESSION"); envSession != "" {
			sessionName = envSession
		} else {
			return fmt.Errorf("session is required for 'iso start' - use --session flag or set ISO_SESSION env var")
		}

		client, err := iso.New(sessionName)
		if err != nil {
			return err
		}
		defer client.Close()

		return client.Start()
	}

	cmd := mflags.NewCommand(fs, handler,
		mflags.WithUsage("Start a persistent session (requires --session)"),
	)

	dispatcher.Dispatch("start", cmd)
}

// registerStopCommand registers the 'stop' command
func registerStopCommand(dispatcher *mflags.Dispatcher) {
	fs := mflags.NewFlagSet("stop")

	all := fs.Bool("all", 'a', false, "Stop all ISO-managed containers across all projects")
	allSessions := fs.Bool("all-sessions", 'S', false, "Stop all sessions for the current project")
	session := fs.String("session", 's', "", "Session name (required for stopping specific session, or use ISO_SESSION env var)")

	handler := func(fs *mflags.FlagSet, args []string) error {
		if *all {
			return iso.StopAll()
		}

		if *allSessions {
			return iso.StopAllSessions()
		}

		// For stopping a specific session, require session name
		var sessionName string
		if *session != "" {
			sessionName = *session
		} else if envSession := os.Getenv("ISO_SESSION"); envSession != "" {
			sessionName = envSession
		} else {
			return fmt.Errorf("session is required for 'iso stop' - use --session flag, set ISO_SESSION env var, or use --all/--all-sessions")
		}

		client, err := iso.New(sessionName)
		if err != nil {
			return err
		}
		defer client.Close()

		return client.Stop()
	}

	cmd := mflags.NewCommand(fs, handler,
		mflags.WithUsage("Stop and remove a persistent session (requires --session, or use --all/--all-sessions)"),
	)

	dispatcher.Dispatch("stop", cmd)
}

// registerResetCommand registers the 'reset' command
func registerResetCommand(dispatcher *mflags.Dispatcher) {
	fs := mflags.NewFlagSet("reset")

	session := fs.String("session", 's', "", "Session name (required, or use ISO_SESSION env var)")

	handler := func(fs *mflags.FlagSet, args []string) error {
		// For reset command, session is required
		var sessionName string
		if *session != "" {
			sessionName = *session
		} else if envSession := os.Getenv("ISO_SESSION"); envSession != "" {
			sessionName = envSession
		} else {
			return fmt.Errorf("session is required for 'iso reset' - use --session flag or set ISO_SESSION env var")
		}

		client, err := iso.New(sessionName)
		if err != nil {
			return err
		}
		defer client.Close()

		return client.Reset()
	}

	cmd := mflags.NewCommand(fs, handler,
		mflags.WithUsage("Reset a persistent session's container (requires --session)"),
	)

	dispatcher.Dispatch("reset", cmd)
}

// registerStatusCommand registers the 'status' command
func registerStatusCommand(dispatcher *mflags.Dispatcher) {
	fs := mflags.NewFlagSet("status")

	session := fs.String("session", 's', "", "Session name (required, or use ISO_SESSION env var)")

	handler := func(fs *mflags.FlagSet, args []string) error {
		// For status command, session is required
		var sessionName string
		if *session != "" {
			sessionName = *session
		} else if envSession := os.Getenv("ISO_SESSION"); envSession != "" {
			sessionName = envSession
		} else {
			return fmt.Errorf("session is required for 'iso status' - use --session flag or set ISO_SESSION env var")
		}

		client, err := iso.New(sessionName)
		if err != nil {
			return err
		}
		defer client.Close()

		status, err := client.Status()
		if err != nil {
			return err
		}

		imageStatus := "does not exist"
		if status.ImageExists {
			imageStatus = "exists"
		}

		slog.Info("image status", "image", status.ImageName, "status", imageStatus)
		slog.Info("container status", "container", status.ContainerName, "status", status.ContainerState)

		return nil
	}

	cmd := mflags.NewCommand(fs, handler,
		mflags.WithUsage("Show status of a session (requires --session)"),
	)

	dispatcher.Dispatch("status", cmd)
}

// registerListCommand registers the 'list' command
func registerListCommand(dispatcher *mflags.Dispatcher) {
	fs := mflags.NewFlagSet("list")

	handler := func(fs *mflags.FlagSet, args []string) error {
		containers, err := iso.ListAll()
		if err != nil {
			return err
		}

		if len(containers) == 0 {
			fmt.Println("No ISO containers found")
			return nil
		}

		// Group containers by project
		projectGroups := make(map[string][]iso.IsoContainer)
		projectDirs := make(map[string]string)
		for _, c := range containers {
			projectGroups[c.ProjectName] = append(projectGroups[c.ProjectName], c)
			projectDirs[c.ProjectName] = c.ProjectDir
		}

		// Print each project group
		for projectName, projectContainers := range projectGroups {
			fmt.Printf("\n%s (%s):\n", projectName, projectDirs[projectName])
			fmt.Printf("  %-12s %-15s %-20s %s\n", "CONTAINER ID", "NAME", "SESSION", "STATUS")

			for _, c := range projectContainers {
				status := c.Status

				sessionInfo := c.Session
				if c.IsService {
					status += " (service: " + c.ServiceName + ")"
				}

				fmt.Printf("  %-12s %-15s %-20s %s\n",
					c.ID,
					c.ShortName,
					sessionInfo,
					status,
				)
			}
		}
		fmt.Println()

		return nil
	}

	cmd := mflags.NewCommand(fs, handler,
		mflags.WithUsage("List all ISO-managed containers"),
	)

	dispatcher.Dispatch("list", cmd)
}

// registerPruneCommand registers the 'prune' command
func registerPruneCommand(dispatcher *mflags.Dispatcher) {
	fs := mflags.NewFlagSet("prune")

	handler := func(fs *mflags.FlagSet, args []string) error {
		// Prune doesn't use a specific session since cache volumes are shared
		// We just need a client to access the project configuration
		sessionName, _ := getSession("")
		client, err := iso.New(sessionName)
		if err != nil {
			return err
		}
		defer client.Close()

		return client.Prune()
	}

	cmd := mflags.NewCommand(fs, handler,
		mflags.WithUsage("Remove all cache volumes for the project"),
	)

	dispatcher.Dispatch("prune", cmd)
}

// reapZombies reaps any zombie child processes
func reapZombies() {
	for {
		var wstatus syscall.WaitStatus
		pid, err := syscall.Wait4(-1, &wstatus, syscall.WNOHANG, nil)
		if err != nil || pid <= 0 {
			// No more children to reap
			break
		}
		slog.Debug("reaped child process", "pid", pid, "exit_status", wstatus.ExitStatus())
	}
}

// registerInitCommand registers the 'init' command for project initialization
func registerInitCommand(dispatcher *mflags.Dispatcher) {
	fs := mflags.NewFlagSet("init")

	handler := func(fs *mflags.FlagSet, args []string) error {
		return iso.InitProject()
	}

	cmd := mflags.NewCommand(fs, handler,
		mflags.WithUsage("Initialize .iso directory with AI-generated Dockerfile and services.yml"),
	)

	dispatcher.Dispatch("init", cmd)
}

// registerInternalInitCommand registers the '_internal-init' command for container init process
func registerInternalInitCommand(dispatcher *mflags.Dispatcher) {
	fs := mflags.NewFlagSet("_internal-init")

	handler := func(fs *mflags.FlagSet, args []string) error {
		// Set up signal handling
		sigChan := make(chan os.Signal, 1)
		signal.Notify(sigChan, syscall.SIGTERM, syscall.SIGINT, syscall.SIGCHLD)

		slog.Info("init process started, waiting for signals")

		// Sleep loop with zombie reaping
		ticker := time.NewTicker(1 * time.Second)
		defer ticker.Stop()

		for {
			select {
			case sig := <-sigChan:
				if sig == syscall.SIGCHLD {
					// Reap zombie processes
					reapZombies()
				} else {
					slog.Info("received signal, exiting", "signal", sig)
					return nil
				}
			case <-ticker.C:
				// Periodically reap zombies in case we missed a SIGCHLD
				reapZombies()
			}
		}
	}

	cmd := mflags.NewCommand(fs, handler,
		mflags.WithUsage("Run as init process in container (internal use only)"),
	)

	dispatcher.Dispatch("_internal-init", cmd)
}

// waitForServices waits for all services in ISO_SERVICES to be reachable
func waitForServices(isoServices string) error {
	services := strings.Split(isoServices, ",")

	for _, serviceSpec := range services {
		parts := strings.Split(serviceSpec, ":")
		if len(parts) != 2 {
			return fmt.Errorf("invalid service spec: %s (expected format: service:port)", serviceSpec)
		}

		host := parts[0]
		port := parts[1]
		address := net.JoinHostPort(host, port)

		slog.Debug("waiting for service", "service", host, "address", address)

		// Try to connect with retries
		maxAttempts := 30
		for attempt := 1; attempt <= maxAttempts; attempt++ {
			conn, err := net.DialTimeout("tcp", address, 1*time.Second)
			if err == nil {
				conn.Close()
				slog.Debug("service ready", "service", host, "address", address)
				break
			}

			if attempt == maxAttempts {
				return fmt.Errorf("service %s not ready after %d attempts", host, maxAttempts)
			}

			time.Sleep(1 * time.Second)
		}
	}

	return nil
}

// registerInEnvCommand registers the 'in-env run' command
func registerInEnvCommand(dispatcher *mflags.Dispatcher) {
	fs := mflags.NewFlagSet("in-env run")
	fs.AllowUnknownFlags(true)

	handler := func(fs *mflags.FlagSet, args []string) error {
		// Combine positional args and unknown flags to form the command
		command := append(args, fs.UnknownFlags()...)

		if len(command) == 0 {
			return fmt.Errorf("no command specified")
		}

		// Get workdir from environment (defaults to /workspace)
		workDir := os.Getenv("ISO_WORKDIR")
		if workDir == "" {
			workDir = "/workspace"
		}

		// Wait for services to be ready if ISO_SERVICES is set
		if isoServices := os.Getenv("ISO_SERVICES"); isoServices != "" {
			if err := waitForServices(isoServices); err != nil {
				return err
			}
		}

		// Execute pre-run.sh if it exists
		preRunScript := fmt.Sprintf("%s/.iso/pre-run.sh", workDir)
		if _, err := os.Stat(preRunScript); err == nil {
			// Script exists, execute it
			cmd := exec.Command("bash", preRunScript)
			cmd.Stdout = os.Stdout
			cmd.Stderr = os.Stderr
			cmd.Stdin = os.Stdin

			if err := cmd.Run(); err != nil {
				if exitErr, ok := err.(*exec.ExitError); ok {
					return &ExitError{Code: exitErr.ExitCode()}
				}
				return fmt.Errorf("failed to execute pre-run.sh: %w", err)
			}
		}

		// Execute the main command
		mainCmd := exec.Command(command[0], command[1:]...)
		mainCmd.Stdout = os.Stdout
		mainCmd.Stderr = os.Stderr
		mainCmd.Stdin = os.Stdin

		mainExitCode := 0
		if err := mainCmd.Run(); err != nil {
			if exitErr, ok := err.(*exec.ExitError); ok {
				mainExitCode = exitErr.ExitCode()
			} else {
				return fmt.Errorf("failed to execute command: %w", err)
			}
		}

		// Execute post-run.sh if it exists
		postRunScript := fmt.Sprintf("%s/.iso/post-run.sh", workDir)
		if _, err := os.Stat(postRunScript); err == nil {
			// Script exists, execute it
			cmd := exec.Command("bash", postRunScript)
			cmd.Stdout = os.Stdout
			cmd.Stderr = os.Stderr
			cmd.Stdin = os.Stdin

			if err := cmd.Run(); err != nil {
				if exitErr, ok := err.(*exec.ExitError); ok {
					slog.Warn("post-run.sh exited with non-zero code", "exit_code", exitErr.ExitCode())
				} else {
					slog.Warn("failed to execute post-run.sh", "error", err)
				}
			}
		}

		// Return the main command's exit code
		if mainExitCode != 0 {
			return &ExitError{Code: mainExitCode}
		}

		return nil
	}

	cmd := mflags.NewCommand(fs, handler,
		mflags.WithUsage("Run a command with pre/post hooks (internal use inside container)"),
	)

	dispatcher.Dispatch("in-env run", cmd)
}

// registerAgentHelpCommand registers the 'agent-help' command
func registerAgentHelpCommand(dispatcher *mflags.Dispatcher) {
	fs := mflags.NewFlagSet("agent-help")

	handler := func(fs *mflags.FlagSet, args []string) error {
		fmt.Print(agentHelpContent)
		return nil
	}

	cmd := mflags.NewCommand(fs, handler,
		mflags.WithUsage("Output markdown documentation for AI agents"),
	)

	dispatcher.Dispatch("agent-help", cmd)
}
