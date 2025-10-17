#!/usr/bin/env bash
# Copyright 2025 Crunchy Data Solutions, Inc.
# SPDX-License-Identifier: Apache-2.0

# enforce_100_coverage.sh - Enforce 100% test coverage target
# This script runs tests with coverage and fails if coverage is below the target

set -euo pipefail

COVERAGE_TARGET="${COVERAGE_TARGET:-100}"
COVERAGE_FILE="${COVERAGE_FILE:-coverage.out}"
COVERAGE_HTML="${COVERAGE_HTML:-coverage.html}"
PACKAGES="${PACKAGES:-./...}"

echo "========================================"
echo "  100% Coverage Enforcement Check"
echo "========================================"
echo "Target: ${COVERAGE_TARGET}%"
echo "Packages: ${PACKAGES}"
echo ""

# Run tests with coverage
echo "Running tests with coverage..."
go test -coverprofile="${COVERAGE_FILE}" -covermode=atomic -coverpkg="${PACKAGES}" ${PACKAGES}

# Generate coverage report
echo ""
echo "Generating coverage report..."
go tool cover -func="${COVERAGE_FILE}" -o coverage.txt

# Generate HTML report
go tool cover -html="${COVERAGE_FILE}" -o "${COVERAGE_HTML}"

# Extract total coverage percentage
TOTAL_COVERAGE=$(go tool cover -func="${COVERAGE_FILE}" | grep total | awk '{print $3}' | sed 's/%//')

echo ""
echo "========================================"
echo "  Coverage Results"
echo "========================================"
echo "Total Coverage: ${TOTAL_COVERAGE}%"
echo "Target Coverage: ${COVERAGE_TARGET}%"
echo ""

# Check if coverage meets target
if (( $(echo "${TOTAL_COVERAGE} < ${COVERAGE_TARGET}" | bc -l) )); then
    echo "❌ FAILED: Coverage ${TOTAL_COVERAGE}% is below target ${COVERAGE_TARGET}%"
    echo ""
    echo "Files with less than 100% coverage:"
    echo "-----------------------------------"
    awk < coverage.txt '$3 != "100.0%" && NR > 1 { print $1 ": " $3 }' | head -20
    echo ""
    echo "HTML report saved to: ${COVERAGE_HTML}"
    exit 1
else
    echo "✅ SUCCESS: Coverage ${TOTAL_COVERAGE}% meets or exceeds target ${COVERAGE_TARGET}%"
    echo ""
    echo "HTML report saved to: ${COVERAGE_HTML}"
    exit 0
fi
