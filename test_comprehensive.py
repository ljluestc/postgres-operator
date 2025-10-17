#!/usr/bin/env python3
"""
Comprehensive Test Coverage Analysis for Postgres Operator (Go Project)
This script orchestrates the execution of various test suites and generates comprehensive reports.
"""

import subprocess
import json
import os
import sys
import time
from datetime import datetime
from pathlib import Path

class TestComprehensive:
    def __init__(self):
        self.project_root = Path.cwd()
        self.results = {
            "timestamp": time.time(),
            "coverage": {},
            "tests": {},
            "features": {}
        }
        self.go_available = self.check_go_availability()
        
    def check_go_availability(self):
        """Check if Go is available in the environment"""
        try:
            result = subprocess.run(["go", "version"], capture_output=True, text=True, timeout=10)
            if result.returncode == 0:
                print("✅ Go is available:", result.stdout.strip())
                return True
            else:
                print("❌ Go is not available")
                return False
        except (subprocess.TimeoutExpired, FileNotFoundError):
            print("❌ Go is not available")
            return False
    
    def run_command(self, command, description):
        """Run a command and capture its output"""
        print(f"Running: {description}")
        print(f"Command: {command}")
        
        try:
            result = subprocess.run(
                command, 
                shell=True, 
                capture_output=True, 
                text=True, 
                timeout=300,  # 5 minute timeout
                cwd=self.project_root
            )
            
            return {
                "success": result.returncode == 0,
                "stdout": result.stdout,
                "stderr": result.stderr,
                "returncode": result.returncode
            }
        except subprocess.TimeoutExpired:
            return {
                "success": False,
                "stdout": "",
                "stderr": "Command timed out after 5 minutes",
                "returncode": 124
            }
        except Exception as e:
            return {
                "success": False,
                "stdout": "",
                "stderr": str(e),
                "returncode": 1
            }
    
    def run_unit_tests(self):
        """Run unit tests with coverage"""
        print("\n=== Running Unit Tests ===")
        
        if not self.go_available:
            print("Skipping Go-specific tests...")
            return {
                "success": False,
                "stdout": "",
                "stderr": "Go is not available",
                "returncode": 1
            }
        
        # Run unit tests with coverage
        command = "make check"
        result = self.run_command(command, "Unit tests with coverage")
        
        if result["success"]:
            print("✅ Unit tests passed")
        else:
            print("❌ Unit tests failed")
            print("Error:", result["stderr"])
        
        return result
    
    def run_integration_tests(self):
        """Run integration tests"""
        print("\n=== Running Integration Tests ===")
        
        if not self.go_available:
            print("Skipping Go-specific tests...")
            return {
                "success": False,
                "stdout": "",
                "stderr": "Go is not available",
                "returncode": 1
            }
        
        # Run integration tests
        command = "make check-envtest"
        result = self.run_command(command, "Integration tests")
        
        if result["success"]:
            print("✅ Integration tests passed")
        else:
            print("❌ Integration tests failed")
            print("Error:", result["stderr"])
        
        return result
    
    def run_e2e_tests(self):
        """Run end-to-end tests"""
        print("\n=== Running E2E Tests ===")
        
        if not self.go_available:
            print("Skipping Go-specific tests...")
            return {
                "success": False,
                "stdout": "",
                "stderr": "Go is not available",
                "returncode": 1
            }
        
        # Run E2E tests
        command = "make check-kuttl"
        result = self.run_command(command, "E2E tests")
        
        if result["success"]:
            print("✅ E2E tests passed")
        else:
            print("❌ E2E tests failed")
            print("Error:", result["stderr"])
        
        return result
    
    def generate_coverage_report(self):
        """Generate comprehensive coverage report"""
        print("\n=== Generating Coverage Report ===")
        
        if not self.go_available:
            print("Skipping Go-specific coverage...")
            return {
                "success": False,
                "stdout": "",
                "stderr": "Go is not available",
                "returncode": 1
            }
        
        # Generate coverage profile
        command = "go test -coverprofile=coverage.out -covermode=count ./..."
        result = self.run_command(command, "Generating coverage profile")
        
        if result["success"]:
            # Generate HTML report
            html_command = "go tool cover -html=coverage.out -o coverage.html"
            html_result = self.run_command(html_command, "Generating HTML coverage report")
            
            # Generate text report
            text_command = "go tool cover -func=coverage.out > coverage.txt"
            text_result = self.run_command(text_command, "Generating text coverage report")
            
            if html_result["success"] and text_result["success"]:
                print("✅ Coverage reports generated")
                print("📊 HTML report: coverage.html")
                print("📊 Text report: coverage.txt")
            else:
                print("⚠️ Some coverage reports failed to generate")
        else:
            print("❌ Failed to generate coverage profile")
            print("Error:", result["stderr"])
        
        return result
    
    def analyze_feature_coverage(self):
        """Analyze coverage of implemented features"""
        print("\n=== Analyzing Feature Coverage ===")
        
        # List of implemented features based on PRD analysis
        features = {
            "Backup Verification Automation": "internal/pgbackrest/verify.go",
            "Enhanced Backup Metrics": "internal/pgbackrest/metrics.go",
            "Automated Secrets Rotation": "internal/controller/postgrescluster/secrets_rotation.go",
            "Configuration Validation Framework": "internal/validation/cluster_validator.go",
            "FIPS Mode Support": "internal/fips/fips.go",
            "Disaster Recovery Drill Automation": "internal/dr/drill.go",
            "Query Performance Insights": "internal/postgres/performance.go",
            "Connection Pool Analytics": "internal/pgbouncer/analytics.go",
            "Failover Time Optimization": "internal/patroni/failover.go",
            "Auto-Scaling Read Replicas": "internal/controller/postgrescluster/autoscaling.go",
            "Interactive Cluster Creation Wizard": "cmd/pgo-wizard/wizard/wizard.go",
            "Backup Encryption at Rest": "internal/pgbackrest/encryption.go",
            "Cross-Region Backup Replication": "internal/pgbackrest/replication.go"
        }
        
        feature_results = {}
        
        for feature_name, file_path in features.items():
            if os.path.exists(file_path):
                print(f"✅ {feature_name}: {file_path}")
                feature_results[feature_name] = {
                    "file": file_path,
                    "exists": True
                }
            else:
                print(f"❌ {feature_name}: {file_path} (not found)")
                feature_results[feature_name] = {
                    "file": file_path,
                    "exists": False
                }
        
        return feature_results
    
    def calculate_coverage_percentage(self):
        """Calculate overall coverage percentage"""
        if not self.go_available:
            return 0.0
        
        try:
            # Read coverage.txt if it exists
            if os.path.exists("coverage.txt"):
                with open("coverage.txt", "r") as f:
                    content = f.read()
                    # Extract total coverage percentage
                    for line in content.split('\n'):
                        if 'total:' in line:
                            # Extract percentage from line like "total: (statements) 85.2%"
                            parts = line.split()
                            for part in parts:
                                if part.endswith('%'):
                                    return float(part[:-1])
            return 0.0
        except Exception as e:
            print(f"Error calculating coverage: {e}")
            return 0.0
    
    def run_comprehensive_analysis(self):
        """Run comprehensive test analysis"""
        print("🚀 Starting Comprehensive Test Coverage Analysis")
        print("=" * 60)
        
        # Check Go availability
        print("Running: Checking Go availability")
        if not self.go_available:
            print("❌ Go is not available")
            print("Skipping Go-specific tests...")
        else:
            print("✅ Go is available")
        
        # Run test suites
        self.results["tests"]["unit"] = self.run_unit_tests()
        self.results["tests"]["integration"] = self.run_integration_tests()
        self.results["tests"]["e2e"] = self.run_e2e_tests()
        
        # Generate coverage report
        self.results["coverage"]["generation"] = self.generate_coverage_report()
        
        # Analyze feature coverage
        self.results["features"] = self.analyze_feature_coverage()
        
        # Calculate coverage percentage
        coverage_pct = self.calculate_coverage_percentage()
        self.results["coverage"]["percentage"] = coverage_pct
        
        # Print summary
        print("\n=== Test Coverage Summary ===")
        print(f"Unit Tests: {'✅ PASS' if self.results['tests']['unit']['success'] else '❌ FAIL'}")
        print(f"Integration Tests: {'✅ PASS' if self.results['tests']['integration']['success'] else '❌ FAIL'}")
        print(f"E2E Tests: {'✅ PASS' if self.results['tests']['e2e']['success'] else '❌ FAIL'}")
        print(f"Coverage: {coverage_pct:.1f}% (Target: 80%)")
        
        # Count implemented features
        implemented_features = sum(1 for f in self.results["features"].values() if f["exists"])
        total_features = len(self.results["features"])
        print(f"Features Implemented: {implemented_features}/{total_features}")
        
        # Determine overall status
        all_tests_passed = all(test["success"] for test in self.results["tests"].values())
        coverage_target_met = coverage_pct >= 80.0
        
        if all_tests_passed and coverage_target_met:
            print("\n🎉 ALL TESTS PASSED AND COVERAGE TARGET MET!")
        elif all_tests_passed:
            print("\n⚠️ ALL TESTS PASSED BUT COVERAGE TARGET NOT MET")
        else:
            print("\n⚠️ SOME TESTS FAILED OR COVERAGE TARGET NOT MET")
        
        # Save results
        with open("test_results.json", "w") as f:
            json.dump(self.results, f, indent=2)
        
        print(f"\nResults saved to: {self.project_root}/test_results.json")
        
        if all_tests_passed and coverage_target_met:
            print("\n✅ Comprehensive test analysis completed successfully.")
            return True
        else:
            print("\n❌ Comprehensive test analysis completed with issues.")
            return False

def main():
    """Main entry point"""
    analyzer = TestComprehensive()
    success = analyzer.run_comprehensive_analysis()
    sys.exit(0 if success else 1)

if __name__ == "__main__":
    main()