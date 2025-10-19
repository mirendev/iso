package main

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"miren.dev/iso"
	"miren.dev/mflags"
	"miren.dev/trifle"
)

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
