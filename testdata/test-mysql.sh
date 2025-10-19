#!/bin/bash
set -e

echo "=== MySQL Connection Test ==="
echo

# Wait a moment for MySQL to be fully ready
echo "Waiting for MySQL to be ready..."
sleep 10

export MYSQL_ROOT_PASSWORD=rootpass
export MYSQL_DATABASE=testdb
export MYSQL_USER=testuser
export MYSQL_PASSWORD=testpass

# MySQL connection options (skip SSL for this test)
MYSQL_OPTS="--host=mysql --user=$MYSQL_USER --password=$MYSQL_PASSWORD --skip-ssl $MYSQL_DATABASE"

# Test connection
echo "Testing connection to MySQL..."
mysql $MYSQL_OPTS -e "SELECT 'Connection successful!' AS status;"
echo

# Create a test table
echo "Creating test table..."
mysql $MYSQL_OPTS <<EOF
CREATE TABLE IF NOT EXISTS users (
    id INT AUTO_INCREMENT PRIMARY KEY,
    name VARCHAR(100) NOT NULL,
    email VARCHAR(100) NOT NULL,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);
EOF
echo "Table created successfully"
echo

# Insert test data
echo "Inserting test data..."
mysql $MYSQL_OPTS <<EOF
INSERT INTO users (name, email) VALUES
    ('Alice Smith', 'alice@example.com'),
    ('Bob Jones', 'bob@example.com'),
    ('Charlie Brown', 'charlie@example.com');
EOF
echo "Data inserted successfully"
echo

# Query the data
echo "Querying data from database:"
mysql $MYSQL_OPTS -e "SELECT * FROM users;"
echo

# Count records
echo "Record count:"
mysql $MYSQL_OPTS -e "SELECT COUNT(*) AS total_users FROM users;"
echo

# Show database info
echo "Database information:"
mysql $MYSQL_OPTS -e "SHOW TABLES;"
echo

echo "=== All tests passed! ==="
