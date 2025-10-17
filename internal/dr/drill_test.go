// Copyright 2025 Crunchy Data Solutions, Inc.
//
// SPDX-License-Identifier: Apache-2.0

package dr

import (
	"context"
	"testing"
	"time"

	"gotest.tools/v3/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/crunchydata/postgres-operator/pkg/apis/postgres-operator.crunchydata.com/v1beta1"
)

func TestCreateDRDrillJob(t *testing.T) {
	ctx := context.Background()

	cluster := &v1beta1.PostgresCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-cluster",
			Namespace: "test-ns",
		},
		Spec: v1beta1.PostgresClusterSpec{
			Image: "registry.developers.crunchydata.com/crunchydata/crunchy-postgres:ubi8-16-latest",
		},
	}

	t.Run("DisabledConfig", func(t *testing.T) {
		config := &DRDrillConfig{Enabled: false}
		job, err := CreateDRDrillJob(ctx, cluster, config)
		assert.NilError(t, err)
		assert.Assert(t, job == nil, "expected nil job when disabled")
	})

	t.Run("FullRestoreDrill", func(t *testing.T) {
		config := &DRDrillConfig{
			Enabled:        true,
			DrillType:      DrillTypeFullRestore,
			TargetCluster:  "test-cluster",
			DrillNamespace: "test-ns",
			RepoName:       "repo1",
			RTOTarget:      30 * time.Second,
		}

		job, err := CreateDRDrillJob(ctx, cluster, config)
		assert.NilError(t, err)
		assert.Assert(t, job != nil, "expected job to be created")
		assert.Assert(t, job.Spec.Template.Spec.Containers[0].Name == "dr-drill")
		assert.Assert(t, job.Spec.ActiveDeadlineSeconds != nil, "expected timeout to be set")
	})

	t.Run("PointInTimeDrill", func(t *testing.T) {
		pitrTime := time.Now().Add(-1 * time.Hour)
		config := &DRDrillConfig{
			Enabled:        true,
			DrillType:      DrillTypePointInTime,
			TargetCluster:  "test-cluster",
			DrillNamespace: "test-ns",
			RepoName:       "repo1",
			PointInTime:    &pitrTime,
			RTOTarget:      30 * time.Second,
		}

		job, err := CreateDRDrillJob(ctx, cluster, config)
		assert.NilError(t, err)
		assert.Assert(t, job != nil)

		// Check environment variables
		envFound := false
		for _, env := range job.Spec.Template.Spec.Containers[0].Env {
			if env.Name == "PITR_TARGET" {
				envFound = true
				assert.Equal(t, env.Value, pitrTime.Format(time.RFC3339))
			}
		}
		assert.Assert(t, envFound, "expected PITR_TARGET env var")
	})

	t.Run("DataVerificationDrill", func(t *testing.T) {
		config := &DRDrillConfig{
			Enabled:        true,
			DrillType:      DrillTypeDataVerification,
			TargetCluster:  "test-cluster",
			DrillNamespace: "test-ns",
			RepoName:       "repo1",
			ValidationQueries: []string{
				"SELECT COUNT(*) FROM users",
				"SELECT pg_database_size('postgres')",
			},
			RTOTarget: 30 * time.Second,
		}

		job, err := CreateDRDrillJob(ctx, cluster, config)
		assert.NilError(t, err)
		assert.Assert(t, job != nil)
	})

	t.Run("CustomTimeout", func(t *testing.T) {
		customTimeout := 3 * time.Hour
		config := &DRDrillConfig{
			Enabled:        true,
			DrillType:      DrillTypeFullRestore,
			TargetCluster:  "test-cluster",
			DrillNamespace: "test-ns",
			RepoName:       "repo1",
			Timeout:        customTimeout,
			RTOTarget:      30 * time.Second,
		}

		job, err := CreateDRDrillJob(ctx, cluster, config)
		assert.NilError(t, err)
		assert.Assert(t, job != nil)
		assert.Equal(t, *job.Spec.ActiveDeadlineSeconds, int64(customTimeout.Seconds()))
	})

	t.Run("DefaultTimeout", func(t *testing.T) {
		config := &DRDrillConfig{
			Enabled:        true,
			DrillType:      DrillTypeFullRestore,
			TargetCluster:  "test-cluster",
			DrillNamespace: "test-ns",
			RepoName:       "repo1",
			RTOTarget:      30 * time.Second,
		}

		job, err := CreateDRDrillJob(ctx, cluster, config)
		assert.NilError(t, err)
		assert.Assert(t, job != nil)
		assert.Equal(t, *job.Spec.ActiveDeadlineSeconds, int64(defaultDrillTimeout.Seconds()))
	})
}

func TestCreateDrillScript(t *testing.T) {
	t.Run("FullRestoreScript", func(t *testing.T) {
		config := &DRDrillConfig{
			DrillType: DrillTypeFullRestore,
			RTOTarget: 30 * time.Second,
		}

		script := createDrillScript(config)
		assert.Assert(t, len(script) > 0)
		assert.Equal(t, script[0], "bash")
		assert.Equal(t, script[1], "-ceu")

		// Check script contains expected commands
		scriptContent := script[3]
		assert.Assert(t, len(scriptContent) > 0)
	})

	t.Run("PITRScript", func(t *testing.T) {
		pitrTime := time.Now().Add(-1 * time.Hour)
		config := &DRDrillConfig{
			DrillType:   DrillTypePointInTime,
			PointInTime: &pitrTime,
			RTOTarget:   30 * time.Second,
		}

		script := createDrillScript(config)
		assert.Assert(t, len(script) > 0)
	})

	t.Run("WithValidationQueries", func(t *testing.T) {
		config := &DRDrillConfig{
			DrillType: DrillTypeFullRestore,
			ValidationQueries: []string{
				"SELECT COUNT(*) FROM users",
			},
			RTOTarget: 30 * time.Second,
		}

		script := createDrillScript(config)
		assert.Assert(t, len(script) > 0)
	})
}

func TestAnalyzeDRDrillResults(t *testing.T) {
	t.Run("RTOAchieved", func(t *testing.T) {
		result := &DRDrillResult{
			RTOAchieved: true,
			Duration:    25 * time.Second,
			Metrics: DRDrillMetrics{
				RestoreThroughput:   100, // High throughput to avoid recommendations
				DatabaseStartupTime: 1 * time.Minute, // Low startup time
			},
		}

		recommendations := AnalyzeDRDrillResults(result)
		assert.Assert(t, len(recommendations) > 0)
		assert.Assert(t, recommendations[0] == "✓ Excellent! All DR drill objectives achieved.")
	})

	t.Run("RTOMissed", func(t *testing.T) {
		result := &DRDrillResult{
			RTOAchieved: false,
			Metrics: DRDrillMetrics{
				RestoreThroughput: 100,
			},
		}

		recommendations := AnalyzeDRDrillResults(result)
		assert.Assert(t, len(recommendations) > 0)
	})

	t.Run("LowThroughput", func(t *testing.T) {
		result := &DRDrillResult{
			RTOAchieved: true,
			Metrics: DRDrillMetrics{
				RestoreThroughput: 30, // Less than 50 MB/s
			},
		}

		recommendations := AnalyzeDRDrillResults(result)
		assert.Assert(t, len(recommendations) > 0)
	})

	t.Run("SlowDatabaseStartup", func(t *testing.T) {
		result := &DRDrillResult{
			RTOAchieved: true,
			Metrics: DRDrillMetrics{
				DatabaseStartupTime: 6 * time.Minute,
				RestoreThroughput:   100,
			},
		}

		recommendations := AnalyzeDRDrillResults(result)
		assert.Assert(t, len(recommendations) > 0)
	})

	t.Run("FailedValidations", func(t *testing.T) {
		result := &DRDrillResult{
			RTOAchieved: true,
			Metrics: DRDrillMetrics{
				RestoreThroughput: 100,
			},
			ValidationResults: []ValidationResult{
				{Passed: true},
				{Passed: false},
				{Passed: false},
			},
		}

		recommendations := AnalyzeDRDrillResults(result)
		assert.Assert(t, len(recommendations) > 0)
	})
}

func TestGenerateDRReport(t *testing.T) {
	t.Run("SuccessfulDrill", func(t *testing.T) {
		result := &DRDrillResult{
			DrillID:     "drill-12345",
			StartTime:   time.Now().Add(-30 * time.Minute),
			EndTime:     time.Now(),
			Duration:    25 * time.Second,
			RTOAchieved: true,
			Metrics: DRDrillMetrics{
				RestoreDuration:     20 * time.Second,
				BackupSize:          1024 * 1024 * 1024,
				RestoreThroughput:   51.2,
				DatabaseStartupTime: 5 * time.Second,
			},
			ValidationResults: []ValidationResult{
				{
					QueryName: "User count",
					Passed:    true,
					Message:   "Count matches expected value",
				},
			},
			Recommendations: []string{
				"Continue regular drills",
			},
		}

		report := GenerateDRReport(result)
		assert.Assert(t, len(report) > 0)
		assert.Assert(t, report != "")
	})

	t.Run("FailedDrill", func(t *testing.T) {
		result := &DRDrillResult{
			DrillID:     "drill-67890",
			StartTime:   time.Now().Add(-1 * time.Hour),
			EndTime:     time.Now(),
			Duration:    45 * time.Second,
			RTOAchieved: false,
			ValidationResults: []ValidationResult{
				{
					QueryName: "Data check",
					Passed:    false,
					Message:   "Mismatch detected",
				},
			},
		}

		report := GenerateDRReport(result)
		assert.Assert(t, len(report) > 0)
	})
}

func TestReconcileDRDrills(t *testing.T) {
	t.Run("DisabledConfig", func(t *testing.T) {
		ctx := context.Background()

		cluster := &v1beta1.PostgresCluster{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-cluster",
				Namespace: "test-ns",
			},
		}

		err := ReconcileDRDrills(ctx, nil, cluster, nil)
		assert.NilError(t, err)
	})

	t.Run("EnabledButNoTrigger", func(t *testing.T) {
		ctx := context.Background()

		cluster := &v1beta1.PostgresCluster{
			ObjectMeta: metav1.ObjectMeta{
				Name:        "test-cluster",
				Namespace:   "test-ns",
				Annotations: map[string]string{},
			},
		}

		config := &DRDrillConfig{
			Enabled:   true,
			DrillType: DrillTypeFullRestore,
		}

		err := ReconcileDRDrills(ctx, nil, cluster, config)
		assert.NilError(t, err)
	})
}

func TestCreateFullRestoreDrill(t *testing.T) {
	config := &DRDrillConfig{
		RepoName: "repo1",
	}

	script := createFullRestoreDrill(config)
	assert.Assert(t, len(script) > 0)
}

func TestCreatePITRDrill(t *testing.T) {
	config := &DRDrillConfig{
		RepoName: "repo1",
	}

	script := createPITRDrill(config)
	assert.Assert(t, len(script) > 0)
}

func TestCreateDataVerificationDrill(t *testing.T) {
	config := &DRDrillConfig{}

	script := createDataVerificationDrill(config)
	assert.Assert(t, len(script) > 0)
}

func TestDRDrillConfig(t *testing.T) {
	t.Run("DefaultConfiguration", func(t *testing.T) {
		config := &DRDrillConfig{
			Enabled:        true,
			DrillType:      DrillTypeFullRestore,
			TargetCluster:  "cluster",
			DrillNamespace: "default",
			RepoName:       "repo1",
			RTOTarget:      30 * time.Second,
		}

		assert.Assert(t, config.Enabled)
		assert.Equal(t, config.DrillType, DrillTypeFullRestore)
		assert.Equal(t, config.RTOTarget, 30*time.Second)
	})

	t.Run("WithNotifications", func(t *testing.T) {
		config := &DRDrillConfig{
			NotifyOnSuccess: true,
			NotifyOnFailure: true,
			WebhookURL:      "https://hooks.slack.com/test",
			SlackChannel:    "#ops",
		}

		assert.Assert(t, config.NotifyOnSuccess)
		assert.Assert(t, config.NotifyOnFailure)
		assert.Equal(t, config.WebhookURL, "https://hooks.slack.com/test")
	})
}

func TestDRDrillResult(t *testing.T) {
	t.Run("SuccessResult", func(t *testing.T) {
		result := &DRDrillResult{
			DrillID:     "test-drill",
			Success:     true,
			RTOAchieved: true,
			Duration:    20 * time.Second,
		}

		assert.Assert(t, result.Success)
		assert.Assert(t, result.RTOAchieved)
	})

	t.Run("FailureResult", func(t *testing.T) {
		result := &DRDrillResult{
			DrillID:      "test-drill",
			Success:      false,
			ErrorMessage: "Backup not found",
		}

		assert.Assert(t, !result.Success)
		assert.Equal(t, result.ErrorMessage, "Backup not found")
	})
}

func TestValidationResult(t *testing.T) {
	t.Run("PassedValidation", func(t *testing.T) {
		result := ValidationResult{
			QueryName:      "row_count",
			Query:          "SELECT COUNT(*) FROM users",
			ExpectedResult: 1000,
			ActualResult:   1000,
			Passed:         true,
			Message:        "Count matches",
		}

		assert.Assert(t, result.Passed)
		assert.Equal(t, result.ExpectedResult, 1000)
		assert.Equal(t, result.ActualResult, 1000)
	})

	t.Run("FailedValidation", func(t *testing.T) {
		result := ValidationResult{
			QueryName:      "row_count",
			Query:          "SELECT COUNT(*) FROM users",
			ExpectedResult: 1000,
			ActualResult:   900,
			Passed:         false,
			Message:        "Count mismatch",
		}

		assert.Assert(t, !result.Passed)
	})
}
