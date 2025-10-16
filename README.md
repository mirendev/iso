# iso - Isolated Docker Environment Tool

`iso` is a Go tool that provides an isolated Docker environment for running tests and commands. It manages Docker containers automatically, reusing them when possible for faster execution.

## Features

- **Automatic Container Management**: Detects if a container is already running and reuses it
- **Automatic Image Building**: Builds Docker images from Dockerfiles when needed
- **Simple CLI Interface**: Uses `miren.dev/mflags` for command-line parsing
- **Workspace Mounting**: Automatically mounts the current directory into the container at `/workspace`

## Installation

```bash
go build -o iso
```

## Usage

### Run a Command

Run a command in the isolated environment:

```bash
./iso run [flags] -- <command> [args...]
```

**Note**: Use `--` to separate iso flags from the command you want to run.

Example:
```bash
./iso run -- go test ./...
./iso run -- make build
./iso run -- echo "Hello from iso"
./iso run -- ls -la
```

If your command doesn't have flags that conflict with iso flags, you can omit `--`:
```bash
./iso run go version
./iso run pwd
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

### Flags

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

```bash
# Check status (nothing exists yet)
./iso status

# Run tests (builds image, creates container, runs tests)
./iso run -- go test ./...

# Run another command (reuses existing container)
./iso run -- go build

# List files in the workspace
./iso run -- ls -la

# Check status (shows running container)
./iso status

# Stop the container when done
./iso stop
```

## Requirements

- Docker installed and running
- Go 1.21 or later (for building)
- Docker daemon accessible (typically via `/var/run/docker.sock`)

## License

This tool uses:
- `miren.dev/mflags` for CLI parsing
- `github.com/docker/docker` for Docker API access
