# ISO - Isolated Docker Environment

ISO is a tool for running commands in isolated Docker containers with automatic service management.

## Directory Structure

ISO requires a `.iso` directory in your project root containing:

```
project-root/
├── .iso/
│   ├── Dockerfile          # Required: Defines the container environment
│   ├── config.yml          # Optional: Configuration options
│   ├── services.yml        # Optional: Defines service containers
│   ├── pre-run.sh          # Optional: Runs before every command
│   └── post-run.sh         # Optional: Runs after every command
├── your-project-files/
└── ...
```

### .iso/config.yml

Optional configuration file for ISO environment settings.

Format:
```yaml
# Enable/disable privileged mode for the container (default: false)
privileged: false

# Set the mount point for your project inside the container (default: /workspace)
workdir: /workspace

# Paths that should be mounted as Docker volumes (optional)
volumes:
  - /data
  - /go/pkg
```

**Available Options**:

- **privileged** (boolean, default: `false`): Run the container in privileged mode, giving it extended capabilities. Useful for Docker-in-Docker, systemd, or operations requiring elevated permissions.

- **workdir** (string, default: `/workspace`): The directory path inside the container where your project root will be mounted. This affects where your code is accessible in the container.

- **volumes** (list of strings, optional): List of container paths that should be mounted as persistent Docker volumes instead of being part of the project directory. These volumes persist across container restarts and are automatically removed when you run `iso stop`. Useful for caching build artifacts, dependencies, or data that should persist between runs but doesn't need to be in your project directory.

Example:
```yaml
privileged: true
workdir: /code
volumes:
  - /data           # Persistent data directory
  - /go/pkg         # Go package cache
  - /root/.cache    # General cache directory
```

**Volume Naming**: Volumes are automatically named as `<project>-<sanitized-path>`. For example, if your project is named `myapp` and you configure `/go/pkg` as a volume, it will be created as `myapp-go-pkg`.

### .iso/Dockerfile

The Dockerfile defines your project's container environment. ISO will:
- Build an image named `<project>-shell` from this Dockerfile
- Mount your project root at the configured workdir (default: `/workspace`) in the container
- Set the working directory based on where you run commands

Example:
```dockerfile
FROM golang:1.23-alpine

RUN apk add --no-cache \
    git \
    make \
    bash \
    mysql-client

WORKDIR /workspace
```

### .iso/services.yml

Optional file defining service containers (databases, caches, etc.). Services will:
- Start automatically when running commands
- Be accessible via DNS using their service name
- Share a Docker network with the main container
- Wait for readiness if a `port` is specified

Format:
```yaml
services:
  mysql:
    image: mysql:8.0
    port: 3306                            # Optional: Wait for this port to be ready
    command:                              # Optional: Override container command
      - --default-authentication-plugin=mysql_native_password
    environment:                          # Optional: Environment variables
      MYSQL_ROOT_PASSWORD: rootpass
      MYSQL_DATABASE: testdb
      MYSQL_USER: testuser
      MYSQL_PASSWORD: testpass

  redis:
    image: redis:alpine
    port: 6379                            # Optional: Wait for this port to be ready
    environment:
      REDIS_PASSWORD: secret
```

**Service Readiness**: When a service specifies a `port`, ISO will automatically wait for that service to be reachable on that port before running commands. This eliminates the need for manual wait loops in pre-run.sh scripts.

### .iso/pre-run.sh and .iso/post-run.sh

Optional shell scripts that run automatically before and after every `iso run` command:

**pre-run.sh**:
- Executes before your command runs (after service readiness checks)
- Useful for setup tasks like running migrations or checking prerequisites
- If the script exits with a non-zero code, the main command is aborted
- Runs in the same working directory as your command

**post-run.sh**:
- Executes after your command completes
- Useful for cleanup tasks, generating reports, or logging
- Always runs regardless of the main command's exit code
- Failures in post-run.sh are logged but don't affect the main command's exit code

Example pre-run.sh:
```bash
#!/bin/bash
# Run database migrations (services are already ready)
mysql -h mysql -u testuser -ptestpass testdb < /workspace/migrations/schema.sql
echo "Migrations complete"
```

Example post-run.sh:
```bash
#!/bin/bash
# Clean up temporary files
rm -rf /workspace/tmp/*
echo "Cleanup complete"
```

**Note**: Both scripts must be executable (`chmod +x .iso/pre-run.sh .iso/post-run.sh`)

## Naming Conventions

All resources are automatically named based on your project directory:

- **Project name**: Base name of the directory containing `.iso`
- **Image**: `<project>-shell`
- **Main container**: `<project>-shell`
- **Service containers**: `<project>-<service-name>`
- **Network**: `<project>-network`

Example: If your project is in `/home/user/myapp`:
- Image: `myapp-shell`
- Container: `myapp-shell`
- MySQL service: `myapp-mysql`
- Network: `myapp-network`

## Commands

### iso run <command>

Run a command in the isolated container. By default, each command runs in a **fresh container** that is automatically removed after execution, ensuring a clean environment every time.

The container will:
1. Start any defined services (if not already running)
2. Create a fresh container (building the image if needed)
3. Wait for services to be ready (if ports are specified)
4. Execute `.iso/pre-run.sh` if it exists (aborts if it fails)
5. Execute your command in the correct working directory
6. Execute `.iso/post-run.sh` if it exists (failure logged but doesn't affect exit code)
7. Forward stdin/stdout/stderr transparently
8. Automatically remove the container after command completes

**Options**:
- `--reuse` / `-r`: Use a persistent container instead of creating a fresh one. The container will be reused across multiple `iso run` commands for faster startup.
- `--session` / `-s`: Specify a session name (default: ISO_SESSION env var or 'default')

**Environment Variables**: You can set environment variables for the command by prefixing them in `KEY=VALUE` format:

```bash
iso run VERBOSE=1 go test ./...
iso run DEBUG=true LOG_LEVEL=debug ./script.sh
iso run DB_HOST=mysql DB_PORT=3306 ./migrate.sh
```

Environment variables must:
- Appear before the actual command
- Have names that start with a letter or underscore
- Contain only letters, digits, and underscores

Examples:
```bash
iso run go test ./...              # Fresh container (default)
iso run --reuse bash               # Persistent container
iso run make build
iso run mysql -h mysql -u testuser -ptestpass testdb
iso run VERBOSE=1 shell.sh
```

### iso start

Start the main container and all services with verbose logging. Useful for:
- Pre-starting services before running commands
- Debugging service startup issues
- Keeping services running between commands

### iso stop

Stop and remove the main container and all service containers. Also removes the Docker network.

### iso build [--rebuild]

Build (or rebuild) the Docker image from the Dockerfile.

Options:
- `--rebuild` / `-r`: Force rebuild even if image exists

### iso status

Show the current status of the image and container.

### iso init

Internal command used as the init process inside containers. You shouldn't need to call this directly.

## Service Communication

Services are accessible by their name via Docker's DNS:

```bash
# If you have a mysql service defined in services.yml
iso run mysql -h mysql -u testuser -ptestpass testdb

# In your code
DATABASE_URL=mysql://testuser:testpass@mysql:3306/testdb
```

## Working Directory Behavior

ISO preserves your working directory context:

- If you run `iso run` from `/project/subdir`, the command runs in `/workspace/subdir`
- The entire project root is mounted at `/workspace`
- Relative paths work as expected

## Typical Workflows

### Initial Setup

1. Create `.iso/Dockerfile` in your project root
2. Optionally create `.iso/services.yml` for databases/services
3. Run `iso build` to build the initial image

### Daily Development

```bash
# Run tests
iso run go test ./...

# Build your project
iso run make build

# Start an interactive shell
iso run bash

# Run database migrations
iso run ./migrate up
```

### Working with Services

```bash
# Services start automatically with any command
iso run go test ./...  # Starts mysql, runs tests

# Or pre-start them explicitly
iso start

# Check what's running
iso status

# Stop everything when done
iso stop
```

### Rebuilding After Changes

```bash
# After modifying .iso/Dockerfile
iso build --rebuild

# Then stop and restart to use new image
iso stop
iso run <your-command>
```

## Tips for AI Agents

1. **Always check for .iso directory**: Before running iso commands, verify `.iso/Dockerfile` exists
2. **Auto-detection**: ISO automatically finds the `.iso` directory by searching upward from the current directory
3. **No configuration needed**: All settings are inferred from the directory structure
4. **Service naming**: Services in `services.yml` are accessed by their key name (e.g., `mysql`, `redis`)
5. **Image caching**: Images are cached; use `--rebuild` only when Dockerfile changes
6. **Fresh containers by default**: Each `iso run` uses a fresh container that auto-removes after execution, ensuring clean environments
7. **Persistent containers**: Use `--reuse` flag when you need containers to persist between commands for faster startup (e.g., interactive shells)
8. **Network isolation**: Each project gets its own isolated network
9. **Clean shutdown**: Use `iso stop` to clean up all resources
10. **Service readiness**: Add `port` to services in `services.yml` for automatic readiness checks - no manual wait loops needed
11. **Pre/Post hooks**: Use `.iso/pre-run.sh` for migrations/setup and `.iso/post-run.sh` for cleanup tasks
12. **Hook executability**: Remember to make hook scripts executable with `chmod +x`

## Troubleshooting

- **"no .iso directory found"**: Create `.iso/Dockerfile` in your project root
- **Services not accessible**: Verify `services.yml` syntax and service names
- **Image build fails**: Check Dockerfile syntax and base image availability
- **Container name conflicts**: Run `iso stop` to remove old containers
