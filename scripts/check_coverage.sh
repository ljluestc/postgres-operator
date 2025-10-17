#!/bin/bash
# check_coverage.sh - Pre-commit hook for test coverage validation

set -e

echo "=== Postgres Operator Test Coverage Check ==="

# Check if Go is available
if ! command -v go &> /dev/null; then
    echo "Warning: Go not available, skipping coverage check"
    exit 0
fi

# Run tests with coverage
echo "Running tests with coverage..."
go test -coverprofile=coverage.out ./... || {
    echo "Tests failed!"
    exit 1
}

# Generate coverage report
echo "Generating coverage report..."
go tool cover -func=coverage.out > coverage.txt

# Extract coverage percentage
COVERAGE=$(go tool cover -func=coverage.out | grep total | awk '{print $3}' | sed 's/%//')

echo "Current coverage: ${COVERAGE}%"

# Check if coverage meets target (80%)
TARGET=80
if (( $(echo "$COVERAGE >= $TARGET" | bc -l) )); then
    echo "✅ Coverage target met: ${COVERAGE}% >= ${TARGET}%"
    exit 0
else
    echo "❌ Coverage target not met: ${COVERAGE}% < ${TARGET}%"
    echo "Please add more tests to reach the target coverage."
    exit 1
fi
