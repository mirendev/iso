# ISO - Isolated Docker Environment

ISO is a tool for running commands in isolated Docker containers with automatic service management.

## Directory Structure

ISO requires a `.iso` directory in your project root containing:

```
project-root/
├── .iso/
│   ├── Dockerfile          # Required: Defines the container environment
│   └── services.yml        # Optional: Defines service containers
├── your-project-files/
└── ...
```

### .iso/Dockerfile

The Dockerfile defines your project's container environment. ISO will:
- Build an image named `<project>-shell` from this Dockerfile
- Mount your project root at `/workspace` in the container
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

Format:
```yaml
services:
  mysql:
    image: mysql:8.0
    command:                          # Optional: Override container command
      - --default-authentication-plugin=mysql_native_password
    environment:                       # Optional: Environment variables
      MYSQL_ROOT_PASSWORD: rootpass
      MYSQL_DATABASE: testdb
      MYSQL_USER: testuser
      MYSQL_PASSWORD: testpass

  redis:
    image: redis:alpine
    environment:
      REDIS_PASSWORD: secret
```

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

Run a command in the isolated container. The container will:
1. Start any defined services (if not already running)
2. Start the main container (building the image if needed)
3. Execute your command in the correct working directory
4. Forward stdin/stdout/stderr transparently

Examples:
```bash
iso run go test ./...
iso run make build
iso run bash
iso run mysql -h mysql -u testuser -ptestpass testdb
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
6. **Persistent containers**: Containers persist between commands for faster startup
7. **Network isolation**: Each project gets its own isolated network
8. **Clean shutdown**: Use `iso stop` to clean up all resources

## Troubleshooting

- **"no .iso directory found"**: Create `.iso/Dockerfile` in your project root
- **Services not accessible**: Verify `services.yml` syntax and service names
- **Image build fails**: Check Dockerfile syntax and base image availability
- **Container name conflicts**: Run `iso stop` to remove old containers
