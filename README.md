# iso - Isolated Docker Environment Tool

`iso` is a Go tool that provides an isolated Docker environment for running tests and commands. It manages Docker containers automatically, reusing them when possible for faster execution.

## Features

- **Docker Compose Support**: Use docker-compose files in addition to Dockerfiles
- **Automatic Container Management**: Detects if a container is already running and reuses it
- **Automatic Image Building**: Builds Docker images from Dockerfiles when needed
- **Simple CLI Interface**: Uses `miren.dev/mflags` for command-line parsing
- **Workspace Mounting**: Automatically mounts the current directory into the container at `/workspace`
- **Exit Code Mirroring**: Mirrors container exit codes for proper CI/CD integration

## Installation

### As a CLI Tool

```bash
go build -o iso ./cmd/iso
```

Or install it:

```bash
go install miren.dev/iso/cmd/iso@latest
```

### As a Library

```bash
go get miren.dev/iso
```

## Usage

### Run a Command

Run a command in the isolated environment:

```bash
./iso run [flags] <command> [args...]
```

Example:
```bash
./iso run go test ./...
./iso run make build
./iso run echo "Hello from iso"
./iso run ls -la
./iso run grep -r "pattern" .
```

The `--` separator is optional but still supported for compatibility:
```bash
./iso run -- ls -la
```

The first time you run a command, `iso` will:
1. Build the Docker image from your Dockerfile (if not already built)
2. Start a new container
3. Execute your command inside the container

Subsequent runs will reuse the existing container for faster execution.

### Build the Image

Build or rebuild the Docker image:

```bash
./iso build
```

Force a rebuild:
```bash
./iso build --rebuild
```

### Check Status

View the current status of the image and container:

```bash
./iso status
```

### Stop the Container

Stop and remove the container (image is preserved):

```bash
./iso stop
```

## Configuration

### Using Docker Compose

`iso` now supports docker-compose files as an alternative to Dockerfiles:

```bash
./iso run -C docker-compose.yml -s myservice go test ./...
```

When using compose mode:
- `-C, --compose <path>`: Path to docker-compose.yml file
- `-s, --service <name>`: Service name to run commands in (defaults to first service)
- `-p, --project <name>`: Compose project name (defaults to directory name)

Example docker-compose.yml:
```yaml
services:
  test:
    image: golang:1.23-alpine
    command: /bin/sh -c "while true; do sleep 1000; done"
    volumes:
      - .:/workspace
    working_dir: /workspace
```

### Using Dockerfile

All commands support these flags:

- `-f, --dockerfile <path>`: Path to Dockerfile (default: `Dockerfile`)
- `-i, --image <name>`: Name of the Docker image (default: `iso-test-env`)
- `-c, --container <name>`: Name of the container (default: `iso-test-container`)

Example:
```bash
./iso run -f Dockerfile.test -i my-test-env -c my-container go test ./...
```

### Dockerfile

Create a `Dockerfile` in your project root. Example:

```dockerfile
FROM golang:1.23-alpine

RUN apk add --no-cache \
    git \
    make \
    bash \
    curl

WORKDIR /workspace
CMD ["/bin/bash"]
```

The tool will automatically mount your current directory to `/workspace` in the container.

## How It Works

1. **Container Detection**: Checks if a container with the specified name is already running
2. **Image Building**: If no image exists, builds it from the Dockerfile
3. **Container Lifecycle**:
   - If container is running: executes command directly inside it
   - If container exists but stopped: starts it and executes command
   - If container doesn't exist: creates a new one and executes command
4. **Command Execution**: Uses `docker exec` to run commands inside the container

## Example Workflow

### CLI Usage with Dockerfile

```bash
# Check status (nothing exists yet)
./iso status

# Run tests (builds image, creates container, runs tests)
./iso run go test ./...

# Run another command (reuses existing container)
./iso run go build

# List files in the workspace
./iso run ls -la

# Check status (shows running container)
./iso status

# Stop the container when done
./iso stop
```

### CLI Usage with Docker Compose

```bash
# Check status (nothing exists yet)
./iso status -C docker-compose.yml -s test

# Run tests (starts compose stack, runs tests)
./iso run -C docker-compose.yml -s test go test ./...

# Run another command (reuses existing container)
./iso run -C docker-compose.yml -s test go build

# Check status (shows running container)
./iso status -C docker-compose.yml -s test

# Stop the compose stack when done
./iso stop -C docker-compose.yml -s test
```

### Library Usage

#### Using Dockerfile

```go
package main

import (
    "fmt"
    "log"

    "miren.dev/iso"
)

func main() {
    // Create a new ISO client with Dockerfile
    client, err := iso.New(iso.Options{
        DockerfilePath: "Dockerfile",
        ImageName:      "my-test-env",
        ContainerName:  "my-container",
    })
    if err != nil {
        log.Fatal(err)
    }
    defer client.Close()

    // Run a command
    exitCode, err := client.Run([]string{"go", "test", "./..."})
    if err != nil {
        log.Fatal(err)
    }
    if exitCode != 0 {
        log.Fatalf("Command failed with exit code %d", exitCode)
    }

    // Check status
    status, err := client.Status()
    if err != nil {
        log.Fatal(err)
    }

    fmt.Printf("Image exists: %v\n", status.ImageExists)
    fmt.Printf("Container state: %s\n", status.ContainerState)
}
```

#### Using Docker Compose

```go
package main

import (
    "fmt"
    "log"

    "miren.dev/iso"
)

func main() {
    // Create a new ISO client with docker-compose
    client, err := iso.New(iso.Options{
        ComposePath: "docker-compose.yml",
        ServiceName: "test",
        ProjectName: "myproject",
    })
    if err != nil {
        log.Fatal(err)
    }
    defer client.Close()

    // Run a command
    exitCode, err := client.Run([]string{"go", "test", "./..."})
    if err != nil {
        log.Fatal(err)
    }
    if exitCode != 0 {
        log.Fatalf("Command failed with exit code %d", exitCode)
    }

    // Check status
    status, err := client.Status()
    if err != nil {
        log.Fatal(err)
    }

    fmt.Printf("Image: %s\n", status.ImageName)
    fmt.Printf("Container state: %s\n", status.ContainerState)
}
```

## Project Structure

```
iso/
├── iso.go              # Public API
├── docker.go           # Docker client wrapper (internal)
├── container.go        # Container management (internal)
├── compose.go          # Docker compose management (internal)
├── cmd/
│   └── iso/
│       └── main.go    # CLI implementation
├── Dockerfile         # Example test environment
├── go.mod
└── README.md
```

## Requirements

- Docker installed and running
- Go 1.21 or later (for building)
- Docker daemon accessible (typically via `/var/run/docker.sock`)

## License

This tool uses:
- `miren.dev/mflags` for CLI parsing
- `github.com/docker/docker` for Docker API access
- `github.com/compose-spec/compose-go` for Docker Compose file parsing
