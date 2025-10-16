package main

import (
	"fmt"
	"os"

	"miren.dev/mflags"
)

const (
	defaultDockerfile    = "Dockerfile"
	defaultImageName     = "iso-test-env"
	defaultContainerName = "iso-test-container"
)

// GlobalFlags contains global flags for all commands
type GlobalFlags struct {
	Dockerfile    string `long:"dockerfile" short:"f" default:"Dockerfile"`
	ImageName     string `long:"image" short:"i" default:"iso-test-env"`
	ContainerName string `long:"container" short:"c" default:"iso-test-container"`
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	dispatcher := mflags.NewDispatcher("iso")

	// Global flags
	globalFlags := &GlobalFlags{}

	// Register commands
	registerRunCommand(dispatcher, globalFlags)
	registerBuildCommand(dispatcher, globalFlags)
	registerStopCommand(dispatcher, globalFlags)
	registerStatusCommand(dispatcher, globalFlags)

	// Execute the dispatcher
	return dispatcher.Execute(os.Args[1:])
}

// registerRunCommand registers the 'run' command
func registerRunCommand(dispatcher *mflags.Dispatcher, globalFlags *GlobalFlags) {
	fs := mflags.NewFlagSet("run")

	// Parse global flags
	fs.String("dockerfile", 'f', defaultDockerfile, "Path to Dockerfile")
	fs.String("image", 'i', defaultImageName, "Name of the Docker image")
	fs.String("container", 'c', defaultContainerName, "Name of the container")

	handler := func(fs *mflags.FlagSet, args []string) error {
		if len(args) == 0 {
			return fmt.Errorf("no command specified")
		}

		dockerfile := fs.Lookup("dockerfile").Value.String()
		imageName := fs.Lookup("image").Value.String()
		containerName := fs.Lookup("container").Value.String()

		// Check if Dockerfile exists
		if _, err := os.Stat(dockerfile); os.IsNotExist(err) {
			return fmt.Errorf("Dockerfile not found: %s", dockerfile)
		}

		cm, err := NewContainerManager(dockerfile, imageName, containerName)
		if err != nil {
			return err
		}
		defer cm.Close()

		return cm.RunCommand(args)
	}

	cmd := mflags.NewCommand(fs, handler,
		mflags.WithUsage("Run a command in the isolated environment"),
	)

	dispatcher.Dispatch("run", cmd)
}

// registerBuildCommand registers the 'build' command
func registerBuildCommand(dispatcher *mflags.Dispatcher, globalFlags *GlobalFlags) {
	fs := mflags.NewFlagSet("build")

	fs.String("dockerfile", 'f', defaultDockerfile, "Path to Dockerfile")
	fs.String("image", 'i', defaultImageName, "Name of the Docker image")
	fs.String("container", 'c', defaultContainerName, "Name of the container")
	rebuild := fs.Bool("rebuild", 'r', false, "Force rebuild even if image exists")

	handler := func(fs *mflags.FlagSet, args []string) error {
		dockerfile := fs.Lookup("dockerfile").Value.String()
		imageName := fs.Lookup("image").Value.String()
		containerName := fs.Lookup("container").Value.String()
		doRebuild := *rebuild

		// Check if Dockerfile exists
		if _, err := os.Stat(dockerfile); os.IsNotExist(err) {
			return fmt.Errorf("Dockerfile not found: %s", dockerfile)
		}

		cm, err := NewContainerManager(dockerfile, imageName, containerName)
		if err != nil {
			return err
		}
		defer cm.Close()

		if doRebuild {
			return cm.RebuildImage()
		}
		return cm.EnsureImage()
	}

	cmd := mflags.NewCommand(fs, handler,
		mflags.WithUsage("Build the Docker image"),
	)

	dispatcher.Dispatch("build", cmd)
}

// registerStopCommand registers the 'stop' command
func registerStopCommand(dispatcher *mflags.Dispatcher, globalFlags *GlobalFlags) {
	fs := mflags.NewFlagSet("stop")

	fs.String("dockerfile", 'f', defaultDockerfile, "Path to Dockerfile")
	fs.String("image", 'i', defaultImageName, "Name of the Docker image")
	fs.String("container", 'c', defaultContainerName, "Name of the container")

	handler := func(fs *mflags.FlagSet, args []string) error {
		dockerfile := fs.Lookup("dockerfile").Value.String()
		imageName := fs.Lookup("image").Value.String()
		containerName := fs.Lookup("container").Value.String()

		cm, err := NewContainerManager(dockerfile, imageName, containerName)
		if err != nil {
			return err
		}
		defer cm.Close()

		return cm.StopContainer()
	}

	cmd := mflags.NewCommand(fs, handler,
		mflags.WithUsage("Stop and remove the container"),
	)

	dispatcher.Dispatch("stop", cmd)
}

// registerStatusCommand registers the 'status' command
func registerStatusCommand(dispatcher *mflags.Dispatcher, globalFlags *GlobalFlags) {
	fs := mflags.NewFlagSet("status")

	fs.String("dockerfile", 'f', defaultDockerfile, "Path to Dockerfile")
	fs.String("image", 'i', defaultImageName, "Name of the Docker image")
	fs.String("container", 'c', defaultContainerName, "Name of the container")

	handler := func(fs *mflags.FlagSet, args []string) error {
		dockerfile := fs.Lookup("dockerfile").Value.String()
		imageName := fs.Lookup("image").Value.String()
		containerName := fs.Lookup("container").Value.String()

		cm, err := NewContainerManager(dockerfile, imageName, containerName)
		if err != nil {
			return err
		}
		defer cm.Close()

		// Check image status
		imageExists, err := cm.docker.ImageExists(imageName)
		if err != nil {
			return err
		}

		fmt.Printf("Image: %s\n", imageName)
		if imageExists {
			fmt.Println("  Status: exists")
		} else {
			fmt.Println("  Status: does not exist")
		}

		// Check container status
		fmt.Printf("\nContainer: %s\n", containerName)
		status, err := cm.GetStatus()
		if err != nil {
			return err
		}
		fmt.Printf("  Status: %s\n", status)

		return nil
	}

	cmd := mflags.NewCommand(fs, handler,
		mflags.WithUsage("Show status of image and container"),
	)

	dispatcher.Dispatch("status", cmd)
}
