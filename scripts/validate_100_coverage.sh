#!/bin/bash
# validate_100_coverage.sh - Script to validate 100% test coverage for Go project

set -e

echo "=== Postgres Operator 100% Coverage Validation ==="

# Check if Go is available
if ! command -v go &> /dev/null; then
    echo "❌ Go is not available. Please install Go first:"
    echo "   curl -L https://go.dev/dl/go1.21.0.linux-amd64.tar.gz | tar -xzC /usr/local"
    echo "   export PATH=\$PATH:/usr/local/go/bin"
    exit 1
fi

echo "✅ Go is available: $(go version)"

# Clean previous coverage files
rm -f coverage.out coverage.html coverage.txt

# Run tests with coverage
echo "Running tests with coverage..."
go test -coverprofile=coverage.out -covermode=count ./... || {
    echo "❌ Tests failed!"
    exit 1
}

# Generate coverage reports
echo "Generating coverage reports..."
go tool cover -func=coverage.out > coverage.txt
go tool cover -html=coverage.out -o coverage.html

# Extract coverage percentage
COVERAGE=$(go tool cover -func=coverage.out | grep total | awk '{print $3}' | sed 's/%//')

echo "Current coverage: ${COVERAGE}%"

# Check if coverage is 100%
if (( $(echo "$COVERAGE >= 100.0" | bc -l) )); then
    echo "🎉 100% COVERAGE ACHIEVED!"
    echo "✅ Coverage: ${COVERAGE}%"
    echo "✅ HTML report: coverage.html"
    echo "✅ Text report: coverage.txt"
    exit 0
else
    echo "❌ Coverage not at 100%: ${COVERAGE}%"
    echo ""
    echo "Uncovered functions:"
    go tool cover -func=coverage.out | grep -v "100.0%" | head -20
    
    echo ""
    echo "To achieve 100% coverage:"
    echo "1. Review uncovered functions above"
    echo "2. Add tests for missing coverage"
    echo "3. Run this script again"
    echo ""
    echo "Detailed HTML report: coverage.html"
    exit 1
fi