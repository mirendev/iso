package main

import (
	"errors"
	"fmt"
	"os"

	"miren.dev/iso"
	"miren.dev/mflags"
)

const (
	defaultDockerfile    = "Dockerfile"
	defaultImageName     = "iso-test-env"
	defaultContainerName = "iso-test-container"
)

// ExitError carries an exit code
type ExitError struct {
	Code int
}

func (e *ExitError) Error() string {
	return fmt.Sprintf("exit code %d", e.Code)
}

func main() {
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
	registerStopCommand(dispatcher)
	registerStatusCommand(dispatcher)

	// Execute the dispatcher
	return dispatcher.Execute(os.Args[1:])
}

// registerRunCommand registers the 'run' command
func registerRunCommand(dispatcher *mflags.Dispatcher) {
	fs := mflags.NewFlagSet("run")

	// Dockerfile-based options
	fs.String("dockerfile", 'f', defaultDockerfile, "Path to Dockerfile")
	fs.String("image", 'i', defaultImageName, "Name of the Docker image")
	fs.String("container", 'c', defaultContainerName, "Name of the container")

	// Compose-based options
	fs.String("compose", 'C', "", "Path to docker-compose.yml file")
	fs.String("service", 's', "", "Service name in compose file")
	fs.String("project", 'p', "", "Compose project name (defaults to directory name)")

	// Allow unknown flags to pass through to the command
	fs.AllowUnknownFlags(true)

	handler := func(fs *mflags.FlagSet, args []string) error {
		composePath := fs.Lookup("compose").Value.String()
		serviceName := fs.Lookup("service").Value.String()
		projectName := fs.Lookup("project").Value.String()
		dockerfile := fs.Lookup("dockerfile").Value.String()
		imageName := fs.Lookup("image").Value.String()
		containerName := fs.Lookup("container").Value.String()

		// Combine positional args and unknown flags to form the command
		// Unknown flags come after positional args
		command := append(args, fs.UnknownFlags()...)

		client, err := iso.New(iso.Options{
			DockerfilePath: dockerfile,
			ImageName:      imageName,
			ContainerName:  containerName,
			ComposePath:    composePath,
			ServiceName:    serviceName,
			ProjectName:    projectName,
		})
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

	// Dockerfile-based options
	fs.String("dockerfile", 'f', defaultDockerfile, "Path to Dockerfile")
	fs.String("image", 'i', defaultImageName, "Name of the Docker image")
	fs.String("container", 'c', defaultContainerName, "Name of the container")
	rebuild := fs.Bool("rebuild", 'r', false, "Force rebuild even if image exists")

	// Compose-based options
	fs.String("compose", 'C', "", "Path to docker-compose.yml file")
	fs.String("service", 's', "", "Service name in compose file")
	fs.String("project", 'p', "", "Compose project name (defaults to directory name)")

	handler := func(fs *mflags.FlagSet, args []string) error {
		composePath := fs.Lookup("compose").Value.String()
		serviceName := fs.Lookup("service").Value.String()
		projectName := fs.Lookup("project").Value.String()
		dockerfile := fs.Lookup("dockerfile").Value.String()
		imageName := fs.Lookup("image").Value.String()
		containerName := fs.Lookup("container").Value.String()
		doRebuild := *rebuild

		client, err := iso.New(iso.Options{
			DockerfilePath: dockerfile,
			ImageName:      imageName,
			ContainerName:  containerName,
			ComposePath:    composePath,
			ServiceName:    serviceName,
			ProjectName:    projectName,
		})
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

// registerStopCommand registers the 'stop' command
func registerStopCommand(dispatcher *mflags.Dispatcher) {
	fs := mflags.NewFlagSet("stop")

	// Dockerfile-based options
	fs.String("dockerfile", 'f', defaultDockerfile, "Path to Dockerfile")
	fs.String("image", 'i', defaultImageName, "Name of the Docker image")
	fs.String("container", 'c', defaultContainerName, "Name of the container")

	// Compose-based options
	fs.String("compose", 'C', "", "Path to docker-compose.yml file")
	fs.String("service", 's', "", "Service name in compose file")
	fs.String("project", 'p', "", "Compose project name (defaults to directory name)")

	handler := func(fs *mflags.FlagSet, args []string) error {
		composePath := fs.Lookup("compose").Value.String()
		serviceName := fs.Lookup("service").Value.String()
		projectName := fs.Lookup("project").Value.String()
		dockerfile := fs.Lookup("dockerfile").Value.String()
		imageName := fs.Lookup("image").Value.String()
		containerName := fs.Lookup("container").Value.String()

		client, err := iso.New(iso.Options{
			DockerfilePath: dockerfile,
			ImageName:      imageName,
			ContainerName:  containerName,
			ComposePath:    composePath,
			ServiceName:    serviceName,
			ProjectName:    projectName,
		})
		if err != nil {
			return err
		}
		defer client.Close()

		return client.Stop()
	}

	cmd := mflags.NewCommand(fs, handler,
		mflags.WithUsage("Stop and remove the container"),
	)

	dispatcher.Dispatch("stop", cmd)
}

// registerStatusCommand registers the 'status' command
func registerStatusCommand(dispatcher *mflags.Dispatcher) {
	fs := mflags.NewFlagSet("status")

	// Dockerfile-based options
	fs.String("dockerfile", 'f', defaultDockerfile, "Path to Dockerfile")
	fs.String("image", 'i', defaultImageName, "Name of the Docker image")
	fs.String("container", 'c', defaultContainerName, "Name of the container")

	// Compose-based options
	fs.String("compose", 'C', "", "Path to docker-compose.yml file")
	fs.String("service", 's', "", "Service name in compose file")
	fs.String("project", 'p', "", "Compose project name (defaults to directory name)")

	handler := func(fs *mflags.FlagSet, args []string) error {
		composePath := fs.Lookup("compose").Value.String()
		serviceName := fs.Lookup("service").Value.String()
		projectName := fs.Lookup("project").Value.String()
		dockerfile := fs.Lookup("dockerfile").Value.String()
		imageName := fs.Lookup("image").Value.String()
		containerName := fs.Lookup("container").Value.String()

		client, err := iso.New(iso.Options{
			DockerfilePath: dockerfile,
			ImageName:      imageName,
			ContainerName:  containerName,
			ComposePath:    composePath,
			ServiceName:    serviceName,
			ProjectName:    projectName,
		})
		if err != nil {
			return err
		}
		defer client.Close()

		status, err := client.Status()
		if err != nil {
			return err
		}

		fmt.Printf("Image: %s\n", status.ImageName)
		if status.ImageExists {
			fmt.Println("  Status: exists")
		} else {
			fmt.Println("  Status: does not exist")
		}

		fmt.Printf("\nContainer: %s\n", status.ContainerName)
		fmt.Printf("  Status: %s\n", status.ContainerState)

		return nil
	}

	cmd := mflags.NewCommand(fs, handler,
		mflags.WithUsage("Show status of image and container"),
	)

	dispatcher.Dispatch("status", cmd)
}
