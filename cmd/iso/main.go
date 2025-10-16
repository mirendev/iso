package main

import (
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

func main() {
	if err := run(); err != nil {
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

	fs.String("dockerfile", 'f', defaultDockerfile, "Path to Dockerfile")
	fs.String("image", 'i', defaultImageName, "Name of the Docker image")
	fs.String("container", 'c', defaultContainerName, "Name of the container")

	handler := func(fs *mflags.FlagSet, args []string) error {
		dockerfile := fs.Lookup("dockerfile").Value.String()
		imageName := fs.Lookup("image").Value.String()
		containerName := fs.Lookup("container").Value.String()

		client, err := iso.New(iso.Options{
			DockerfilePath: dockerfile,
			ImageName:      imageName,
			ContainerName:  containerName,
		})
		if err != nil {
			return err
		}
		defer client.Close()

		return client.Run(args)
	}

	cmd := mflags.NewCommand(fs, handler,
		mflags.WithUsage("Run a command in the isolated environment"),
	)

	dispatcher.Dispatch("run", cmd)
}

// registerBuildCommand registers the 'build' command
func registerBuildCommand(dispatcher *mflags.Dispatcher) {
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

		client, err := iso.New(iso.Options{
			DockerfilePath: dockerfile,
			ImageName:      imageName,
			ContainerName:  containerName,
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

	fs.String("dockerfile", 'f', defaultDockerfile, "Path to Dockerfile")
	fs.String("image", 'i', defaultImageName, "Name of the Docker image")
	fs.String("container", 'c', defaultContainerName, "Name of the container")

	handler := func(fs *mflags.FlagSet, args []string) error {
		dockerfile := fs.Lookup("dockerfile").Value.String()
		imageName := fs.Lookup("image").Value.String()
		containerName := fs.Lookup("container").Value.String()

		client, err := iso.New(iso.Options{
			DockerfilePath: dockerfile,
			ImageName:      imageName,
			ContainerName:  containerName,
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

	fs.String("dockerfile", 'f', defaultDockerfile, "Path to Dockerfile")
	fs.String("image", 'i', defaultImageName, "Name of the Docker image")
	fs.String("container", 'c', defaultContainerName, "Name of the container")

	handler := func(fs *mflags.FlagSet, args []string) error {
		dockerfile := fs.Lookup("dockerfile").Value.String()
		imageName := fs.Lookup("image").Value.String()
		containerName := fs.Lookup("container").Value.String()

		client, err := iso.New(iso.Options{
			DockerfilePath: dockerfile,
			ImageName:      imageName,
			ContainerName:  containerName,
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
