# iso - Isolated Docker Environment Tool

`iso` is a CLI tool that provides isolated Docker environments for running commands and tests. It manages Docker containers automatically, handling image builds, service dependencies, and container lifecycle.

> **Note**: ISO is an internal tool built by the [Miren](https://miren.dev) team to solve our own development environment needs. We're sharing it publicly to allow others to contribute to the [Miren Runtime](https://github.com/mirendev/runtime) (which uses ISO for development and tests), and in case others find it useful. See [CONTRIBUTING.md](CONTRIBUTING.md) for more context on the project's scope.

## Features

- **Service Support**: Define additional services (databases, caches, etc.) in `.iso/services.yml`
- **Automatic Container Management**: Detects if a container is already running and reuses it
- **Automatic Image Building**: Builds Docker images from Dockerfiles when needed
- **Pre/Post-run Hooks**: Execute setup and teardown scripts automatically
- **Service Readiness Checks**: Wait for services to be ready before running commands
- **Simple CLI Interface**: Clean command-line interface for common tasks
- **Workspace Mounting**: Automatically mounts the current directory into the container at `/workspace`
- **Exit Code Mirroring**: Mirrors container exit codes for proper CI/CD integration
- **Cross-platform**: Works on macOS, Windows, and Linux with embedded Linux binaries

## Installation

### Prerequisites

This project uses [quake](https://miren.dev/quake) as its build tool. Install it first:

```bash
go install miren.dev/quake@latest
```

### As a CLI Tool

```bash
# Build using quake
quake build

# Install to ~/bin
quake install

# Or build directly
go build -o bin/iso ./cmd/iso
```

Or install via go:

```bash
go install miren.dev/iso/cmd/iso@latest
```

### Using Nix

If you use Nix, you can install or run `iso` directly from the flake:

```bash
# Run without installing
nix run github:mirendev/iso -- run go test ./...

# Install to your profile
nix profile install github:mirendev/iso

# Add to your flake.nix as an input
inputs.iso.url = "github:mirendev/iso";

# Use in a dev shell
nix develop github:mirendev/iso
```

The Nix build produces static binaries with embedded portable Linux binaries for container use.

### As a Library

```bash
go get miren.dev/iso
```

## Usage

### Run a Command

Run a command in the isolated environment:

```bash
./iso run [command] [args...]
```

Example:
```bash
./iso run go test ./...
./iso run make build
./iso run echo "Hello from iso"
./iso run ls -la
./iso run grep -r "pattern" .
```

The first time you run a command, `iso` will:
1. Build the Docker image from your Dockerfile (if not already built)
2. Start any services defined in services.yml
3. Start the main container
4. Execute your command inside the container

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

Stop and remove the container and services (image is preserved):

```bash
./iso stop
```

## Configuration

### Project Structure

Create a `.iso/` directory in your project root:

```
myproject/
├── .iso/
│   ├── Dockerfile      # Main container definition
│   ├── services.yml    # Additional services (optional)
│   ├── pre-run.sh      # Pre-run hook (optional)
│   └── post-run.sh     # Post-run hook (optional)
├── src/
└── ...
```

### Dockerfile

Create a `.iso/Dockerfile` in your project. Example:

```dockerfile
FROM golang:1.23-alpine

RUN apk add --no-cache \
    git \
    make \
    bash \
    curl

WORKDIR /workspace
```

The tool will automatically mount your current directory to `/workspace` in the container.

### Services Configuration

Create a `.iso/services.yml` file to define additional services:

```yaml
services:
  mysql:
    image: mysql:8.0
    environment:
      MYSQL_ROOT_PASSWORD: rootpass
      MYSQL_DATABASE: testdb
      MYSQL_USER: testuser
      MYSQL_PASSWORD: testpass
    port: 3306  # Optional: ISO will wait for this port to be ready

  redis:
    image: redis:7-alpine
    port: 6379
```

Services are accessible by their service name (e.g., `mysql`, `redis`) from the main container.

### Pre/Post-run Hooks

Create optional hook scripts in `.iso/`:

**`.iso/pre-run.sh`** - Runs before each command:
```bash
#!/bin/bash
echo "Setting up environment..."
# Database migrations, cache warming, etc.
```

**`.iso/post-run.sh`** - Runs after each command:
```bash
#!/bin/bash
echo "Cleaning up..."
# Cleanup temporary files, reset database, etc.
```

## How It Works

1. **Container Detection**: Checks if a container with the specified name is already running
2. **Image Building**: If no image exists, builds it from the Dockerfile
3. **Service Management**: Starts and monitors services defined in services.yml
4. **Container Lifecycle**:
   - If container is running: executes command directly inside it
   - If container exists but stopped: starts it and executes command
   - If container doesn't exist: creates a new one and executes command
5. **Command Execution**: Uses the embedded Linux binary inside the container to handle pre/post hooks

## Example Workflow

```bash
# Check status (nothing exists yet)
./iso status

# Run tests (builds image, starts services, creates container, runs tests)
./iso run go test ./...

# Run another command (reuses existing container)
./iso run go build

# List files in the workspace
./iso run ls -la

# Check status (shows running container and services)
./iso status

# Stop everything when done
./iso stop
```

## Library Usage

```go
package main

import (
    "fmt"
    "log"

    "miren.dev/iso"
)

func main() {
    // Create a new ISO client
    client, err := iso.New(iso.Options{
        IsoDir: ".iso",  // Directory containing Dockerfile and services.yml
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

## Project Structure

```
iso/
├── iso.go              # Public API
├── docker.go           # Docker client wrapper (internal)
├── container.go        # Container management (internal)
├── services.go         # Service management (internal)
├── embedded_binary.go  # Cross-platform binary embedding
├── cmd/
│   └── iso/
│       └── main.go     # CLI implementation
├── testdata/           # Example MySQL integration test
├── Quakefile          # Build configuration
├── go.mod
└── README.md
```

## Requirements

- Docker installed and running
- Go 1.21 or later (for building)
- Docker daemon accessible (typically via `/var/run/docker.sock`)

## License

Apache License 2.0 - see [LICENSE](LICENSE) for details.
