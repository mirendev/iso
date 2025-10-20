# ISO Test Data - MySQL Integration Test

This directory contains a test setup demonstrating ISO with services and MySQL.

## Setup

The `.iso/` directory contains:

1. **Dockerfile** - Defines the main container environment with MySQL client tools
2. **services.yml** - Defines additional services that run alongside the main container

The services.yml defines a MySQL 8.0 service:
- Automatically creates a test database and user
- Includes health check to ensure it's ready before commands run
- Available at hostname `mysql` from the main container

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

- Connecting to MySQL from the main container
- Creating a database table
- Inserting test data
- Querying data
- Running SQL commands

This proves that:
- Multi-service setups work with ISO
- Service discovery works (main container can reach mysql by service name)
- Health checks and readiness work correctly
- The ISO tool can run commands in a multi-service environment
