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
│   ├── peers.yml           # Optional: Defines peer containers for multi-container workflows
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

# Paths that should be mounted as shared cache volumes (optional)
cache:
  - /go/pkg/mod
  - /root/.cache/go-build

# Host directories to bind mount into the container (optional)
# Use ~ to reference the current user's home directory
binds:
  - "~/.ssh:/root/.ssh:ro"
  - "/var/run/docker.sock:/var/run/docker.sock"

# Add custom host-to-IP mappings (optional)
extra_hosts:
  - "myhost:192.168.1.100"
  - "host.docker.internal:host-gateway"
```

**Available Options**:

- **privileged** (boolean, default: `false`): Run the container in privileged mode, giving it extended capabilities. Useful for Docker-in-Docker, systemd, or operations requiring elevated permissions.

- **workdir** (string, default: `/workspace`): The directory path inside the container where your project root will be mounted. This affects where your code is accessible in the container.

- **volumes** (list of strings, optional): List of container paths that should be mounted as persistent Docker volumes instead of being part of the project directory. These volumes are isolated per worktree/session and are automatically removed when you run `iso stop`. Useful for application state or data that should persist between runs but remain isolated per worktree.

- **cache** (list of strings, optional): List of container paths that should be mounted as shared cache volumes. Cache volumes are **shared across all worktrees** of the same repository and persist until you run `iso prune`. Ideal for package manager caches (Go modules, npm, pip, cargo) that can be safely shared to avoid redundant downloads.

- **binds** (list of strings, optional): List of host directory bind mounts in Docker format `"host_path:container_path[:options]"`. This allows mounting specific host directories into the container. The host path supports `~` expansion to reference the current user's home directory (e.g., `~/.ssh:/root/.ssh`). Common uses include mounting SSH keys, Docker socket, or other host resources. Options can include `ro` for read-only or `rw` for read-write (default).

- **extra_hosts** (list of strings, optional): List of custom host-to-IP mappings to add to the container's `/etc/hosts` file. Each entry should be in the format `"hostname:ip"`. Use `host-gateway` as a special IP to refer to the host's gateway IP. This is particularly useful on Linux for accessing services running on the host machine.

Example:
```yaml
privileged: true
workdir: /code
volumes:
  - /data           # Isolated data directory (per worktree)
cache:
  - /go/pkg/mod               # Shared Go module cache
  - /root/.cache/go-build     # Shared Go build cache
  - /root/.cache/pip          # Shared Python package cache
binds:
  - "~/.ssh:/root/.ssh:ro"                       # SSH keys (read-only, ~ expands to home)
  - "/var/run/docker.sock:/var/run/docker.sock"  # Docker socket
extra_hosts:
  - "host.docker.internal:host-gateway"  # Access host services on Linux
```

**Volume Naming**:
- Session volumes are named as `<worktree>-<sanitized-path>` and are isolated per worktree
- Cache volumes are named as `<base-project>-cache-<sanitized-path>` and are shared across worktrees

**Git Worktree Support**: ISO automatically detects git worktrees and shares cache volumes across all worktrees of the same repository. For example, if your main repo is `myproject` and you create worktrees `myproject-feature1` and `myproject-feature2`, all three will share the same cache volumes (e.g., `myproject-cache-go-pkg-mod`) while maintaining isolated session volumes.

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
    extra_hosts:                          # Optional: Custom host mappings
      - "host.docker.internal:host-gateway"

  redis:
    image: redis:alpine
    port: 6379                            # Optional: Wait for this port to be ready
    environment:
      REDIS_PASSWORD: secret
```

**Service Readiness**: When a service specifies a `port`, ISO will automatically wait for that service to be reachable on that port before running commands. This eliminates the need for manual wait loops in pre-run.sh scripts.

**Extra Hosts**: Services can specify `extra_hosts` to add custom host-to-IP mappings, allowing service containers to access external hosts or services running on the Docker host.

### .iso/peers.yml

Optional file defining peer containers for multi-container workflows. Peers are multiple containers built from the same Dockerfile that can communicate over a shared network. This is useful for testing distributed systems, multi-node architectures, or scenarios requiring multiple instances of your application.

Format:
```yaml
network: my-test-network           # Optional: Custom network name (default: <project>-iso-peers)

peers:
  coordinator:
    hostname: coordinator          # Required: Hostname for DNS resolution
    environment:                   # Optional: Environment variables
      ROLE: coordinator
      NODE_ID: "1"
    ports:                         # Optional: Host port mappings
      - "8443:8443"

  runner1:
    hostname: runner1
    environment:
      ROLE: runner
      NODE_ID: "2"

  runner2:
    hostname: runner2
    environment:
      ROLE: runner
      NODE_ID: "3"
```

**Peer Options**:

- **network** (string, optional): Custom network name for peer communication. Defaults to `<project>-iso-peers`. All peers and services are connected to this network.

- **hostname** (string, required): The DNS hostname for this peer. Other peers can reach this container using this hostname.

- **environment** (map, optional): Environment variables to set in the peer container. Useful for configuring roles, node IDs, or other peer-specific settings.

- **ports** (list, optional): Host-to-container port mappings in the format `"hostPort:containerPort"`. Use this to expose specific peers to the host machine.

**Peer Features**:
- All peers share the same Docker image (built from your Dockerfile)
- Peers share the same bind mounts, volumes, and cache as the main container
- Services defined in `services.yml` are automatically connected to the peers network
- Peers can communicate with each other and services using DNS hostnames

**Peer Naming**:
- Peer containers: `<project>-iso-peer-<name>`
- Peers network: Value from `network` field, or `<project>-iso-peers` by default

**Environment Variables in Peers**:
In addition to standard ISO environment variables, peer containers receive:
- **ISO_PEER_NAME**: The peer's name (e.g., "coordinator", "runner1")
- **ISO_PEER_HOSTNAME**: The peer's hostname

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

### Environment Variables

ISO automatically sets the following environment variables inside the container:

- **ISO_WORKDIR**: The container path where your project is mounted (default: `/workspace`)
- **ISO_SESSION**: The name of the current session
- **ISO_UID**: The UID of the host user running the `iso` command
- **ISO_GID**: The GID of the host user running the `iso` command

The ISO_UID and ISO_GID variables are useful when you need to run commands as the host user (to preserve file ownership) while allowing setup scripts to run as root. For example:

```bash
#!/bin/bash
# Run setup as root
apt-get update && apt-get install -y some-package

# Then run user code as the host user to preserve file ownership
su -s /bin/bash "#${ISO_UID}" -c "your-command-here"
```

## Naming Conventions

All resources are automatically named based on your project directory:

- **Project name**: Base name of the directory containing `.iso`
- **Image**: `<project>-shell`
- **Main container**: `<project>-shell`
- **Service containers**: `<project>-<service-name>`
- **Network**: `<project>-network`
- **Peer containers**: `<project>-iso-peer-<name>`
- **Peers network**: `<project>-iso-peers` (or custom name from peers.yml)

Example: If your project is in `/home/user/myapp`:
- Image: `myapp-shell`
- Container: `myapp-shell`
- MySQL service: `myapp-mysql`
- Network: `myapp-network`
- Coordinator peer: `myapp-iso-peer-coordinator`
- Peers network: `myapp-iso-peers`

## Commands

### iso run <command>

Run a command in the isolated container. By default, each command runs in an **ephemeral session** that is automatically cleaned up after execution, ensuring a clean environment every time.

The container will:
1. Start any defined services (if not already running)
2. Create a container (building the image if needed)
3. Wait for services to be ready (if ports are specified)
4. Execute `.iso/pre-run.sh` if it exists (aborts if it fails)
5. Execute your command in the correct working directory
6. Execute `.iso/post-run.sh` if it exists (failure logged but doesn't affect exit code)
7. Forward stdin/stdout/stderr transparently
8. Automatically remove the container and services after command completes (ephemeral mode only)

**Options**:
- `--session` / `-s`: Specify a session name to use a persistent container instead of an ephemeral one (default: ISO_SESSION env var or ephemeral)

**Ephemeral vs Persistent Sessions**:
- **Ephemeral** (default): Fresh container auto-removed after each command, perfect for one-off tasks
- **Persistent**: Use `--session <name>` to create a reusable container that persists until `iso stop`. Use `iso start --session <name>` to pre-start the container, or it will be created automatically on first run.

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
iso run go test ./...                    # Ephemeral session (default)
iso run --session dev bash               # Persistent session named "dev"
ISO_SESSION=dev iso run make build       # Same, using env var
iso run mysql -h mysql -u testuser -ptestpass testdb
iso run VERBOSE=1 shell.sh
```

### iso start

Start a persistent session container and all services with verbose logging. **Requires** a session name via `--session` flag or `ISO_SESSION` env var.

Useful for:
- Pre-starting containers before running commands
- Debugging container startup issues
- Keeping containers running between commands

### iso stop

Stop and remove containers for a session. **Requires** a session name via `--session` flag or `ISO_SESSION` env var.

**Options**:
- `--session` / `-s`: Stop a specific session
- `--all` / `-a`: Stop all ISO-managed containers across all projects
- `--all-sessions` / `-S`: Stop all sessions for the current project

### iso build [--rebuild]

Build (or rebuild) the Docker image from the Dockerfile.

Options:
- `--rebuild` / `-r`: Force rebuild even if image exists

### iso status

Show the current status of the image and container for a session. **Requires** a session name via `--session` flag or `ISO_SESSION` env var.

### iso list

List all ISO-managed containers across all projects and sessions, grouped by project.

### iso reset

Reset a persistent session's container by stopping and recreating it. **Requires** a session name via `--session` flag or `ISO_SESSION` env var. Useful when you need a fresh container state but want to keep the same session.

### iso prune

Remove all cache volumes for the current project. Cache volumes are shared across all sessions/worktrees of the same repository. Use this to free up disk space or force a clean rebuild of caches.

### iso version

Show version information, including the git commit hash the binary was built from.

Example:
```bash
iso version
# Output: iso dev (commit: 2a27c1b1488a0b5b0cd647e9c28a7c3bbec9c801)
```

### iso init

Initialize a new `.iso` directory with AI-generated Dockerfile and services.yml based on your project.

### iso in-env run

Internal command used to run commands inside containers with pre/post hook support. You shouldn't need to call this directly.

## Peers Commands

The peers commands enable multi-container workflows for testing distributed systems.

### iso peers up [peer-names...]

Start all peer containers, or specific peers if names are provided. Also starts any services defined in `services.yml` and connects them to the peers network.

```bash
iso peers up                    # Start all peers
iso peers up coordinator        # Start only the coordinator peer
iso peers up runner1 runner2    # Start specific peers
```

### iso peers down

Stop and remove all peer containers, services, and the peers network.

```bash
iso peers down
```

### iso peers exec [--all] <peer> -- <command>

Execute a command in a peer container. Use `--all` to run on all peers sequentially.

```bash
iso peers exec coordinator -- hostname           # Run on single peer
iso peers exec coordinator -- ./start-server     # Start server on coordinator
iso peers exec --all -- echo "hello"             # Run on all peers
iso peers exec runner1 -- ps aux                 # Check processes on runner1
```

### iso peers shell <peer>

Open an interactive shell (bash) in a peer container.

```bash
iso peers shell coordinator     # Shell into coordinator
iso peers shell runner1         # Shell into runner1
```

### iso peers status

Show the status of all peer containers.

```bash
iso peers status
# Output:
# PEER            HOSTNAME             CONTAINER    STATE
# coordinator     coordinator          a1b2c3d4e5f6 running
# runner1         runner1              -            not created
# runner2         runner2              b2c3d4e5f6g7 stopped
```

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

### Working with Persistent Sessions

```bash
# Set a session name to use persistent containers
export ISO_SESSION=dev

# Services start automatically with any command
iso run go test ./...  # Starts mysql, runs tests in 'dev' session

# Or pre-start them explicitly
iso start  # Starts 'dev' session

# Check what's running
iso status  # Shows status of 'dev' session

# List all sessions across all projects
iso list

# Stop the session when done
iso stop

# Or stop all sessions for the project
iso stop --all-sessions
```

### Working with Ephemeral Sessions

```bash
# No setup needed - just run commands
iso run go test ./...     # Fresh environment, auto-cleaned
iso run make build        # Another fresh environment
iso run bash -c "echo hi" # Yet another fresh environment
```

### Rebuilding After Changes

```bash
# After modifying .iso/Dockerfile
iso build --rebuild

# For ephemeral sessions, just run your command (uses new image automatically)
iso run <your-command>

# For persistent sessions, reset the container to use the new image
ISO_SESSION=dev iso reset
ISO_SESSION=dev iso run <your-command>
```

### Working with Peers (Distributed Testing)

```bash
# Start all peers and services
iso peers up

# Check peer status
iso peers status

# Run commands on specific peers
iso peers exec coordinator -- ./start-coordinator
iso peers exec runner1 -- ./start-runner --coordinator=coordinator:8443

# Run a command on all peers
iso peers exec --all -- echo "Hello from all nodes"

# Interactive debugging
iso peers shell coordinator

# Stop everything when done
iso peers down
```

Example `.iso/peers.yml` for a coordinator/runner architecture:
```yaml
network: test-cluster

peers:
  coordinator:
    hostname: coordinator
    environment:
      ROLE: coordinator
    ports:
      - "8443:8443"

  runner1:
    hostname: runner1
    environment:
      ROLE: runner
      COORDINATOR_URL: "http://coordinator:8443"

  runner2:
    hostname: runner2
    environment:
      ROLE: runner
      COORDINATOR_URL: "http://coordinator:8443"
```

## Tips for AI Agents

1. **Always check for .iso directory**: Before running iso commands, verify `.iso/Dockerfile` exists
2. **Auto-detection**: ISO automatically finds the `.iso` directory by searching upward from the current directory
3. **No configuration needed**: All settings are inferred from the directory structure
4. **Service naming**: Services in `services.yml` are accessed by their key name (e.g., `mysql`, `redis`)
5. **Image caching**: Images are cached; use `--rebuild` only when Dockerfile changes
6. **Ephemeral by default**: Each `iso run` uses an ephemeral session that auto-cleans after execution
7. **Persistent sessions**: Use `--session <name>` or set `ISO_SESSION` env var for reusable containers across multiple commands
8. **Session management**: Use `iso list` to see all sessions, `iso stop --session <name>` to clean up specific sessions
9. **Network isolation**: Each session gets its own isolated network
10. **Service readiness**: Add `port` to services in `services.yml` for automatic readiness checks - no manual wait loops needed
11. **Pre/Post hooks**: Use `.iso/pre-run.sh` for migrations/setup and `.iso/post-run.sh` for cleanup tasks
12. **Hook executability**: Remember to make hook scripts executable with `chmod +x`
13. **Cache volumes**: Use `cache` in config.yml for shared caches (Go modules, npm, etc.) across all sessions/worktrees
14. **Peers for multi-container**: Use `.iso/peers.yml` when testing distributed systems or multi-node architectures
15. **Peer networking**: Peers and services share a network - use hostnames for inter-container communication
16. **Peer lifecycle**: Use `iso peers up` to start, `iso peers status` to check, and `iso peers down` to stop all peers

## Troubleshooting

- **"no .iso directory found"**: Create `.iso/Dockerfile` in your project root
- **Services not accessible**: Verify `services.yml` syntax and service names
- **Image build fails**: Check Dockerfile syntax and base image availability
- **"session is required" errors**: Commands like `iso start`, `iso stop`, and `iso status` require `--session` flag or `ISO_SESSION` env var
- **Container/session conflicts**: Use `iso list` to see all sessions, then `iso stop --session <name>` or `iso stop --all-sessions` to clean up
- **"no peers configured"**: Create `.iso/peers.yml` to use peer commands
- **Peers can't communicate**: Verify hostnames in peers.yml match what your code expects; use `iso peers status` to check peer states
- **"peer is not running"**: Run `iso peers up` before using `iso peers exec` or `iso peers shell`
