# ISO Test Data - MySQL Integration Test

This directory contains a test setup demonstrating ISO with Docker Compose and MySQL.

## Setup

The `.iso/docker-compose.yml` file defines two services:

1. **shell** - A shell environment with MySQL client tools
   - Uses the `iso init` command to keep the container running
   - Mounts the workspace and iso binary
   - Has environment variables for MySQL connection

2. **mysql** - MySQL 8.0 database
   - Automatically creates a test database and user
   - Includes health check to ensure it's ready before shell starts
   - Exposed on port 3306

## Running the Test

From this directory (`testdata/`), run:

```bash
# Build the environment
../iso build

# Run the MySQL connection test
../iso run bash test-mysql.sh

# Or run individual MySQL commands
../iso run mysql -h mysql -u testuser -ptestpass testdb -e "SHOW DATABASES;"

# Check service status
../iso status

# Stop the services
../iso stop
```

## What the Test Demonstrates

The `test-mysql.sh` script demonstrates:

- Connecting to MySQL from the shell container
- Creating a database table
- Inserting test data
- Querying data
- Running SQL commands

This proves that:
- Multi-service compose setups work with ISO
- Service discovery works (shell can reach mysql by service name)
- Dependencies and health checks work correctly
- Environment variables are properly passed
- The ISO tool can run commands in a multi-container environment
