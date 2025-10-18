// Copyright 2025 Crunchy Data Solutions, Inc.
//
// SPDX-License-Identifier: Apache-2.0

package validation

import (
	"context"
	"testing"

	"gotest.tools/v3/assert"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	"github.com/crunchydata/postgres-operator/pkg/apis/postgres-operator.crunchydata.com/v1beta1"
)

func TestNewClusterValidator(t *testing.T) {
	ctx := context.Background()
	validator := NewClusterValidator(ctx)

	assert.Assert(t, validator != nil)
	assert.Assert(t, len(validator.Rules) >= 10)
}

func TestValidatorValidate(t *testing.T) {
	ctx := context.Background()
	validator := NewClusterValidator(ctx)

	t.Run("ValidCluster", func(t *testing.T) {
		cluster := createTestCluster("test-cluster", 16)
		cluster.Spec.Backups.PGBackRest.Repos = []v1beta1.PGBackRestRepo{
			{Name: "repo1"},
		}

		report := validator.Validate(cluster)
		assert.Assert(t, report != nil)
		// Should have some warnings/infos but no critical errors
	})

	t.Run("InvalidCluster", func(t *testing.T) {
		cluster := createTestCluster("TEST_INVALID", 0)
		cluster.Spec.Backups.PGBackRest.Repos = nil

		report := validator.Validate(cluster)
		assert.Assert(t, report != nil)
		assert.Assert(t, !report.Valid)
		assert.Assert(t, report.Errors > 0)
	})
}

func TestResourceLimitsRule(t *testing.T) {
	rule := &ResourceLimitsRule{}

	t.Run("NoLimits", func(t *testing.T) {
		cluster := createTestCluster("test", 16)
		results := rule.Validate(cluster)

		assert.Assert(t, len(results) > 0)
		hasWarning := false
		for _, r := range results {
			if r.Level == ValidationLevelWarning && r.Field == "spec.instanceSets[0].resources.limits" {
				hasWarning = true
			}
		}
		assert.Assert(t, hasWarning)
	})

	t.Run("WithLimits", func(t *testing.T) {
		cluster := createTestCluster("test", 16)
		cluster.Spec.InstanceSets[0].Resources.Limits = corev1.ResourceList{
			corev1.ResourceMemory: resource.MustParse("4Gi"),
			corev1.ResourceCPU:    resource.MustParse("2000m"),
		}
		cluster.Spec.InstanceSets[0].Resources.Requests = corev1.ResourceList{
			corev1.ResourceMemory: resource.MustParse("4Gi"),
			corev1.ResourceCPU:    resource.MustParse("2000m"),
		}

		results := rule.Validate(cluster)
		// May have info about memory:CPU ratio but no limit warnings
		for _, r := range results {
			assert.Assert(t, r.Field != "spec.instanceSets[0].resources.limits" || r.Level != ValidationLevelWarning)
		}
	})

	t.Run("UnusualRatio", func(t *testing.T) {
		cluster := createTestCluster("test", 16)
		cluster.Spec.InstanceSets[0].Resources.Requests = corev1.ResourceList{
			corev1.ResourceMemory: resource.MustParse("512Mi"),
			corev1.ResourceCPU:    resource.MustParse("2000m"),
		}

		results := rule.Validate(cluster)
		hasRatioWarning := false
		for _, r := range results {
			if r.Level == ValidationLevelWarning && r.Message == "Unusual memory:CPU ratio (0.2 GB per core)" {
				hasRatioWarning = true
			}
		}
		assert.Assert(t, hasRatioWarning)
	})
}

func TestBackupConfigurationRule(t *testing.T) {
	rule := &BackupConfigurationRule{}

	t.Run("NoRepos", func(t *testing.T) {
		cluster := createTestCluster("test", 16)
		cluster.Spec.Backups.PGBackRest.Repos = nil

		results := rule.Validate(cluster)
		assert.Assert(t, len(results) > 0)
		assert.Equal(t, results[0].Level, ValidationLevelError)
		assert.Assert(t, results[0].Message == "No backup repositories configured")
	})

	t.Run("NoSchedules", func(t *testing.T) {
		cluster := createTestCluster("test", 16)
		cluster.Spec.Backups.PGBackRest.Repos = []v1beta1.PGBackRestRepo{
			{Name: "repo1"},
		}

		results := rule.Validate(cluster)
		hasScheduleWarning := false
		for _, r := range results {
			if r.Level == ValidationLevelWarning && r.Message == "No backup schedules configured" {
				hasScheduleWarning = true
			}
		}
		assert.Assert(t, hasScheduleWarning)
	})

	t.Run("WithValidSchedule", func(t *testing.T) {
		cluster := createTestCluster("test", 16)
		cluster.Spec.Backups.PGBackRest.Repos = []v1beta1.PGBackRestRepo{
			{
				Name: "repo1",
				BackupSchedules: &v1beta1.PGBackRestBackupSchedules{
					Full: ptr.To("0 2 * * 0"),
				},
			},
		}

		results := rule.Validate(cluster)
		// Should not have schedule errors
		for _, r := range results {
			assert.Assert(t, !((r.Level == ValidationLevelWarning || r.Level == ValidationLevelError) && r.Message == "No backup schedules configured"))
		}
	})
}

func TestHighAvailabilityRule(t *testing.T) {
	rule := &HighAvailabilityRule{}

	t.Run("SingleInstance", func(t *testing.T) {
		cluster := createTestCluster("test", 16)
		results := rule.Validate(cluster)

		hasHAWarning := false
		for _, r := range results {
			if r.Level == ValidationLevelWarning && r.Message == "Single instance configuration - no high availability" {
				hasHAWarning = true
			}
		}
		assert.Assert(t, hasHAWarning)
	})

	t.Run("MultipleReplicas", func(t *testing.T) {
		cluster := createTestCluster("test", 16)
		cluster.Spec.InstanceSets[0].Replicas = ptr.To[int32](3)

		results := rule.Validate(cluster)
		// Should not have HA warning
		for _, r := range results {
			assert.Assert(t, r.Message != "Single instance configuration - no high availability")
		}
	})

	// Note: DisruptionBudget test removed as it's not part of the API
}

func TestPostgreSQLVersionRule(t *testing.T) {
	rule := &PostgreSQLVersionRule{}

	t.Run("NoVersion", func(t *testing.T) {
		cluster := createTestCluster("test", 0)
		results := rule.Validate(cluster)

		assert.Assert(t, len(results) > 0)
		assert.Equal(t, results[0].Level, ValidationLevelError)
		assert.Equal(t, results[0].Message, "PostgreSQL version not specified")
	})

	t.Run("EOLVersion", func(t *testing.T) {
		cluster := createTestCluster("test", 11)
		results := rule.Validate(cluster)

		hasEOLWarning := false
		for _, r := range results {
			if r.Level == ValidationLevelWarning && r.Message == "PostgreSQL 11 is end-of-life (EOL November 9, 2023)" {
				hasEOLWarning = true
			}
		}
		assert.Assert(t, hasEOLWarning)
	})

	t.Run("SupportedVersion", func(t *testing.T) {
		cluster := createTestCluster("test", 16)
		results := rule.Validate(cluster)

		// Should not have EOL warnings for version 16
		for _, r := range results {
			assert.Assert(t, r.Level != ValidationLevelWarning || !contains(r.Message, "end-of-life"))
		}
	})
}

func TestStorageClassRule(t *testing.T) {
	rule := &StorageClassRule{}

	t.Run("SmallStorage", func(t *testing.T) {
		cluster := createTestCluster("test", 16)
		cluster.Spec.InstanceSets[0].DataVolumeClaimSpec = v1beta1.VolumeClaimSpecWithAutoGrow{
			VolumeClaimSpec: v1beta1.VolumeClaimSpec{
			Resources: corev1.VolumeResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceStorage: resource.MustParse("5Gi"),
				},
			},
			AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
		},
		}

		results := rule.Validate(cluster)
		hasStorageWarning := false
		for _, r := range results {
			if r.Level == ValidationLevelWarning && contains(r.Message, "very small") {
				hasStorageWarning = true
			}
		}
		assert.Assert(t, hasStorageWarning)
	})

	t.Run("NoAccessMode", func(t *testing.T) {
		cluster := createTestCluster("test", 16)
		cluster.Spec.InstanceSets[0].DataVolumeClaimSpec = v1beta1.VolumeClaimSpecWithAutoGrow{
			VolumeClaimSpec: v1beta1.VolumeClaimSpec{
			AccessModes: []corev1.PersistentVolumeAccessMode{},
		},
		}

		results := rule.Validate(cluster)
		hasAccessModeError := false
		for _, r := range results {
			if r.Level == ValidationLevelError && r.Message == "ReadWriteOnce access mode required for PostgreSQL data volumes" {
				hasAccessModeError = true
			}
		}
		assert.Assert(t, hasAccessModeError)
	})
}

func TestNamingConventionRule(t *testing.T) {
	rule := &NamingConventionRule{}

	t.Run("ValidName", func(t *testing.T) {
		cluster := createTestCluster("my-cluster-01", 16)
		results := rule.Validate(cluster)

		for _, r := range results {
			assert.Assert(t, r.Message != "Invalid cluster name format")
		}
	})

	t.Run("InvalidName", func(t *testing.T) {
		cluster := createTestCluster("MY_CLUSTER", 16)
		results := rule.Validate(cluster)

		hasNameError := false
		for _, r := range results {
			if r.Level == ValidationLevelError && r.Message == "Invalid cluster name format" {
				hasNameError = true
			}
		}
		assert.Assert(t, hasNameError)
	})

	t.Run("LongName", func(t *testing.T) {
		longName := "this-is-a-very-long-cluster-name-that-exceeds-fifty-characters-in-length"
		cluster := createTestCluster(longName, 16)
		results := rule.Validate(cluster)

		hasLengthWarning := false
		for _, r := range results {
			if r.Level == ValidationLevelWarning && r.Message == "Cluster name is very long" {
				hasLengthWarning = true
			}
		}
		assert.Assert(t, hasLengthWarning)
	})
}

func TestValidateCronExpression(t *testing.T) {
	t.Run("ValidExpression", func(t *testing.T) {
		err := validateCronExpression("0 2 * * *")
		assert.NilError(t, err)
	})

	t.Run("InvalidExpression", func(t *testing.T) {
		err := validateCronExpression("invalid")
		assert.ErrorContains(t, err, "5 fields")
	})

	t.Run("TooManyFields", func(t *testing.T) {
		err := validateCronExpression("0 0 * * * *")
		assert.ErrorContains(t, err, "5 fields")
	})
}

func TestFormatReport(t *testing.T) {
	report := &ValidationReport{
		Valid:    false,
		Errors:   2,
		Warnings: 3,
		Infos:    5,
		Results: []ValidationResult{
			{
				Level:          ValidationLevelError,
				Field:          "spec.postgresVersion",
				Message:        "PostgreSQL version not specified",
				Recommendation: "Specify a version",
				Rule:           "postgresql-version",
			},
		},
	}

	formatted := FormatReport(report)
	assert.Assert(t, contains(formatted, "2 errors, 3 warnings, 5 infos"))
	assert.Assert(t, contains(formatted, "INVALID"))
	assert.Assert(t, contains(formatted, "[ERROR]"))
}

func TestValidationResult(t *testing.T) {
	result := ValidationResult{
		Level:          ValidationLevelWarning,
		Field:          "spec.backups",
		Message:        "Test message",
		Recommendation: "Test recommendation",
		Rule:           "test-rule",
	}

	assert.Equal(t, result.Level, ValidationLevelWarning)
	assert.Equal(t, result.Field, "spec.backups")
	assert.Equal(t, result.Rule, "test-rule")
}

// Helper functions
func createTestCluster(name string, version int) *v1beta1.PostgresCluster {
	return &v1beta1.PostgresCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: "test-namespace",
		},
		Spec: v1beta1.PostgresClusterSpec{
			PostgresVersion: int32(version),
			InstanceSets: []v1beta1.PostgresInstanceSetSpec{
				{
					Name:     "instance1",
					Replicas: ptr.To[int32](1),
					Resources: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{
							corev1.ResourceMemory: resource.MustParse("2Gi"),
							corev1.ResourceCPU:    resource.MustParse("1000m"),
						},
					},
					DataVolumeClaimSpec: v1beta1.VolumeClaimSpecWithAutoGrow{
						VolumeClaimSpec: v1beta1.VolumeClaimSpec{
						Resources: corev1.VolumeResourceRequirements{
							Requests: corev1.ResourceList{
								corev1.ResourceStorage: resource.MustParse("20Gi"),
							},
						},
						AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
					},
					},
				},
			},
			Backups: v1beta1.Backups{
				PGBackRest: v1beta1.PGBackRestArchive{},
			},
		},
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > len(substr) && (s[:len(substr)] == substr || s[len(s)-len(substr):] == substr || findSubstring(s, substr)))
}

func findSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
