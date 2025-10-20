package main

import (
	_ "embed"
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
	// Set up slog with trifle
	slog.SetDefault(slog.New(trifle.New(os.Stderr, &slog.HandlerOptions{
		Level: slog.LevelInfo,
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
	registerStatusCommand(dispatcher)
	registerInitCommand(dispatcher)
	registerInEnvCommand(dispatcher)
	registerAgentHelpCommand(dispatcher)

	// Execute the dispatcher
	return dispatcher.Execute(os.Args[1:])
}

// registerRunCommand registers the 'run' command
func registerRunCommand(dispatcher *mflags.Dispatcher) {
	fs := mflags.NewFlagSet("run")

	// Allow unknown flags to pass through to the command
	fs.AllowUnknownFlags(true)

	handler := func(fs *mflags.FlagSet, args []string) error {
		// Combine positional args and unknown flags to form the command
		// Unknown flags come after positional args
		command := append(args, fs.UnknownFlags()...)

		client, err := iso.New()
		if err != nil {
			return err
		}
		defer client.Close()

		exitCode, err := client.Run(command)
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

	handler := func(fs *mflags.FlagSet, args []string) error {
		doRebuild := *rebuild

		client, err := iso.New()
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

	handler := func(fs *mflags.FlagSet, args []string) error {
		client, err := iso.New()
		if err != nil {
			return err
		}
		defer client.Close()

		return client.Start()
	}

	cmd := mflags.NewCommand(fs, handler,
		mflags.WithUsage("Start all services with verbose output"),
	)

	dispatcher.Dispatch("start", cmd)
}

// registerStopCommand registers the 'stop' command
func registerStopCommand(dispatcher *mflags.Dispatcher) {
	fs := mflags.NewFlagSet("stop")

	handler := func(fs *mflags.FlagSet, args []string) error {
		client, err := iso.New()
		if err != nil {
			return err
		}
		defer client.Close()

		return client.Stop()
	}

	cmd := mflags.NewCommand(fs, handler,
		mflags.WithUsage("Stop and remove the container and all services"),
	)

	dispatcher.Dispatch("stop", cmd)
}

// registerStatusCommand registers the 'status' command
func registerStatusCommand(dispatcher *mflags.Dispatcher) {
	fs := mflags.NewFlagSet("status")

	handler := func(fs *mflags.FlagSet, args []string) error {
		client, err := iso.New()
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
		mflags.WithUsage("Show status of image and container"),
	)

	dispatcher.Dispatch("status", cmd)
}

// registerInitCommand registers the 'init' command
func registerInitCommand(dispatcher *mflags.Dispatcher) {
	fs := mflags.NewFlagSet("init")

	handler := func(fs *mflags.FlagSet, args []string) error {
		// Set up signal handling
		sigChan := make(chan os.Signal, 1)
		signal.Notify(sigChan, syscall.SIGTERM, syscall.SIGINT)

		slog.Info("init process started, waiting for signals")

		// Sleep loop
		ticker := time.NewTicker(1 * time.Second)
		defer ticker.Stop()

		for {
			select {
			case sig := <-sigChan:
				slog.Info("received signal, exiting", "signal", sig)
				return nil
			case <-ticker.C:
				// Continue sleeping
			}
		}
	}

	cmd := mflags.NewCommand(fs, handler,
		mflags.WithUsage("Run as init process in container (sleep loop with signal handling)"),
	)

	dispatcher.Dispatch("init", cmd)
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
		address := fmt.Sprintf("%s:%s", host, port)

		slog.Info("waiting for service", "service", host, "address", address)

		// Try to connect with retries
		maxAttempts := 30
		for attempt := 1; attempt <= maxAttempts; attempt++ {
			conn, err := net.DialTimeout("tcp", address, 1*time.Second)
			if err == nil {
				conn.Close()
				slog.Info("service ready", "service", host, "address", address)
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

// registerInEnvCommand registers the 'in-env' command with subcommands
func registerInEnvCommand(dispatcher *mflags.Dispatcher) {
	// Create a sub-dispatcher for in-env subcommands
	inEnvDispatcher := mflags.NewDispatcher("in-env")

	// Register the 'run' subcommand
	runFS := mflags.NewFlagSet("run")
	runFS.AllowUnknownFlags(true)

	runHandler := func(fs *mflags.FlagSet, args []string) error {
		// Combine positional args and unknown flags to form the command
		command := append(args, fs.UnknownFlags()...)

		if len(command) == 0 {
			return fmt.Errorf("no command specified")
		}

		// Wait for services to be ready if ISO_SERVICES is set
		if isoServices := os.Getenv("ISO_SERVICES"); isoServices != "" {
			if err := waitForServices(isoServices); err != nil {
				return err
			}
		}

		// Execute pre-run.sh if it exists
		preRunScript := "/workspace/.iso/pre-run.sh"
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
		postRunScript := "/workspace/.iso/post-run.sh"
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

	runCmd := mflags.NewCommand(runFS, runHandler,
		mflags.WithUsage("Run a command with pre/post hooks (internal use inside container)"),
	)

	inEnvDispatcher.Dispatch("run", runCmd)

	// Register the in-env dispatcher as a command
	inEnvFS := mflags.NewFlagSet("in-env")
	inEnvHandler := func(fs *mflags.FlagSet, args []string) error {
		return inEnvDispatcher.Execute(args)
	}

	inEnvCmd := mflags.NewCommand(inEnvFS, inEnvHandler,
		mflags.WithUsage("Internal commands for use inside container"),
	)

	dispatcher.Dispatch("in-env", inEnvCmd)
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
