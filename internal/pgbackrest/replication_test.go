// Copyright 2025 Crunchy Data Solutions, Inc.
//
// SPDX-License-Identifier: Apache-2.0

package pgbackrest

import (
	"context"
	"testing"

	"gotest.tools/v3/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/crunchydata/postgres-operator/pkg/apis/postgres-operator.crunchydata.com/v1beta1"
)

func TestReplicationConfig(t *testing.T) {
	t.Run("BasicConfig", func(t *testing.T) {
		config := &ReplicationConfig{
			Enabled:    true,
			Mode:       ReplicationModeAsync,
			SourceRepo: "repo1",
			ReplicaRepos: []ReplicaRepository{
				{
					Name:   "replica1",
					Region: "us-west-2",
				},
			},
		}

		assert.Assert(t, config.Enabled)
		assert.Equal(t, config.Mode, ReplicationModeAsync)
		assert.Equal(t, len(config.ReplicaRepos), 1)
	})

	t.Run("SyncMode", func(t *testing.T) {
		config := &ReplicationConfig{
			Enabled: true,
			Mode:    ReplicationModeSync,
		}

		assert.Equal(t, config.Mode, ReplicationModeSync)
	})

	t.Run("WithSchedule", func(t *testing.T) {
		config := &ReplicationConfig{
			Enabled:  true,
			Schedule: "0 */6 * * *",
		}

		assert.Equal(t, config.Schedule, "0 */6 * * *")
	})

	t.Run("WithRetry", func(t *testing.T) {
		config := &ReplicationConfig{
			RetryCount: 3,
		}

		assert.Equal(t, config.RetryCount, 3)
	})
}

func TestReplicaRepository(t *testing.T) {
	t.Run("BasicRepo", func(t *testing.T) {
		repo := ReplicaRepository{
			Name:     "replica1",
			Region:   "us-east-1",
			Endpoint: "s3.amazonaws.com",
			Bucket:   "my-backup-bucket",
			Path:     "/backups",
			Priority: 1,
			Enabled:  true,
		}

		assert.Equal(t, repo.Name, "replica1")
		assert.Assert(t, repo.Enabled)
		assert.Equal(t, repo.Priority, 1)
	})

	t.Run("WithAWSCredentials", func(t *testing.T) {
		repo := ReplicaRepository{
			Credentials: ReplicationCredentials{
				Type:               "aws",
				AWSAccessKeyID:     "AKIAIOSFODNN7EXAMPLE",
				AWSSecretAccessKey: "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY",
				AWSRegion:          "us-west-2",
			},
		}

		assert.Equal(t, repo.Credentials.Type, "aws")
		assert.Assert(t, repo.Credentials.AWSAccessKeyID != "")
	})

	t.Run("WithGCPCredentials", func(t *testing.T) {
		repo := ReplicaRepository{
			Credentials: ReplicationCredentials{
				Type:              "gcp",
				GCPServiceAccount: "service-account@project.iam.gserviceaccount.com",
				GCPProject:        "my-project",
			},
		}

		assert.Equal(t, repo.Credentials.Type, "gcp")
	})

	t.Run("WithAzureCredentials", func(t *testing.T) {
		repo := ReplicaRepository{
			Credentials: ReplicationCredentials{
				Type:                "azure",
				AzureStorageAccount: "mystorageaccount",
				AzureStorageKey:     "key123",
			},
		}

		assert.Equal(t, repo.Credentials.Type, "azure")
	})
}

func TestReplicationStatus(t *testing.T) {
	t.Run("BasicStatus", func(t *testing.T) {
		now := metav1.Now()
		status := &ReplicationStatus{
			LastReplicationTime:   &now,
			LastReplicationResult: "Success",
			BytesReplicated:       1024 * 1024 * 1024,
			CurrentLag:            0,
		}

		assert.Equal(t, status.LastReplicationResult, "Success")
		assert.Equal(t, status.CurrentLag, int64(0))
	})

	t.Run("WithFailure", func(t *testing.T) {
		status := &ReplicationStatus{
			LastReplicationResult:  "Failed",
			LastReplicationMessage: "Connection timeout",
		}

		assert.Equal(t, status.LastReplicationResult, "Failed")
		assert.Assert(t, status.LastReplicationMessage != "")
	})

	t.Run("WithReplicaStatuses", func(t *testing.T) {
		now := metav1.Now()
		status := &ReplicationStatus{
			ReplicaStatuses: []ReplicaStatus{
				{
					RepoName:     "replica1",
					Region:       "us-east-1",
					InSync:       true,
					Lag:          0,
					LastSyncTime: &now,
					Health:       "healthy",
				},
			},
		}

		assert.Equal(t, len(status.ReplicaStatuses), 1)
		assert.Assert(t, status.ReplicaStatuses[0].InSync)
	})
}

func TestReplicaStatus(t *testing.T) {
	t.Run("HealthyReplica", func(t *testing.T) {
		now := metav1.Now()
		status := ReplicaStatus{
			RepoName:     "replica1",
			Region:       "us-west-2",
			InSync:       true,
			Lag:          0,
			LastSyncTime: &now,
			Health:       "healthy",
		}

		assert.Equal(t, status.Health, "healthy")
		assert.Assert(t, status.InSync)
	})

	t.Run("DegradedReplica", func(t *testing.T) {
		status := ReplicaStatus{
			RepoName: "replica1",
			Region:   "eu-west-1",
			InSync:   false,
			Lag:      1024 * 1024 * 100, // 100MB lag
			Health:   "degraded",
		}

		assert.Equal(t, status.Health, "degraded")
		assert.Assert(t, !status.InSync)
		assert.Assert(t, status.Lag > 0)
	})

	t.Run("UnhealthyReplica", func(t *testing.T) {
		status := ReplicaStatus{
			RepoName:     "replica1",
			Region:       "ap-southeast-1",
			InSync:       false,
			Health:       "unhealthy",
			ErrorMessage: "Connection refused",
		}

		assert.Equal(t, status.Health, "unhealthy")
		assert.Assert(t, status.ErrorMessage != "")
	})
}

func TestCreateReplicationScript(t *testing.T) {
	t.Run("BasicScript", func(t *testing.T) {
		config := &ReplicationConfig{
			SourceRepo: "repo1",
			ReplicaRepos: []ReplicaRepository{
				{
					Name:     "replica1",
					Region:   "us-east-1",
					Endpoint: "s3.amazonaws.com",
					Bucket:   "backup-bucket",
					Path:     "/backups",
					Enabled:  true,
				},
			},
		}

		script := createReplicationScript(config)
		assert.Assert(t, len(script) > 0)
		assert.Equal(t, script[0], "bash")
		assert.Equal(t, script[1], "-ceu")
	})

	t.Run("WithMultipleReplicas", func(t *testing.T) {
		config := &ReplicationConfig{
			SourceRepo: "repo1",
			ReplicaRepos: []ReplicaRepository{
				{Name: "replica1", Region: "us-east-1", Enabled: true, Endpoint: "s3.amazonaws.com", Bucket: "bucket1", Path: "/path1"},
				{Name: "replica2", Region: "eu-west-1", Enabled: true, Endpoint: "s3.amazonaws.com", Bucket: "bucket2", Path: "/path2"},
			},
		}

		script := createReplicationScript(config)
		assert.Assert(t, len(script) > 0)
	})

	t.Run("WithVerification", func(t *testing.T) {
		config := &ReplicationConfig{
			SourceRepo: "repo1",
			ReplicaRepos: []ReplicaRepository{
				{Name: "replica1", Region: "us-east-1", Enabled: true, Endpoint: "s3.amazonaws.com", Bucket: "bucket1", Path: "/path1"},
			},
			VerifyAfterReplication: true,
		}

		script := createReplicationScript(config)
		assert.Assert(t, len(script) > 0)
	})

	t.Run("WithDisabledReplica", func(t *testing.T) {
		config := &ReplicationConfig{
			SourceRepo: "repo1",
			ReplicaRepos: []ReplicaRepository{
				{Name: "replica1", Region: "us-east-1", Enabled: false, Endpoint: "s3.amazonaws.com", Bucket: "bucket1", Path: "/path1"},
			},
		}

		script := createReplicationScript(config)
		assert.Assert(t, len(script) > 0)
	})
}

func TestGenerateReplicationReport(t *testing.T) {
	t.Run("DisabledReplication", func(t *testing.T) {
		report := GenerateReplicationReport(nil, nil)
		assert.Assert(t, len(report) > 0)
	})

	t.Run("EnabledWithStatus", func(t *testing.T) {
		now := metav1.Now()
		config := &ReplicationConfig{
			Enabled:    true,
			SourceRepo: "repo1",
			Mode:       ReplicationModeAsync,
			Schedule:   "0 */6 * * *",
			ReplicaRepos: []ReplicaRepository{
				{Name: "replica1", Region: "us-east-1"},
			},
		}

		status := &ReplicationStatus{
			LastReplicationTime:   &now,
			LastReplicationResult: "Success",
			ReplicaStatuses: []ReplicaStatus{
				{
					RepoName:     "replica1",
					Region:       "us-east-1",
					InSync:       true,
					Health:       "healthy",
					LastSyncTime: &now,
				},
			},
		}

		report := GenerateReplicationReport(config, status)
		assert.Assert(t, len(report) > 0)
	})

	t.Run("WithFailedReplication", func(t *testing.T) {
		config := &ReplicationConfig{
			Enabled:    true,
			SourceRepo: "repo1",
			ReplicaRepos: []ReplicaRepository{
				{Name: "replica1", Region: "us-east-1"},
			},
		}

		status := &ReplicationStatus{
			LastReplicationResult:  "Failed",
			LastReplicationMessage: "Network error",
			ReplicaStatuses: []ReplicaStatus{
				{
					RepoName:     "replica1",
					Region:       "us-east-1",
					InSync:       false,
					Health:       "unhealthy",
					ErrorMessage: "Connection timeout",
				},
			},
		}

		report := GenerateReplicationReport(config, status)
		assert.Assert(t, len(report) > 0)
	})

	t.Run("WithLag", func(t *testing.T) {
		now := metav1.Now()
		config := &ReplicationConfig{
			Enabled:    true,
			SourceRepo: "repo1",
			ReplicaRepos: []ReplicaRepository{
				{Name: "replica1", Region: "us-east-1"},
			},
		}

		status := &ReplicationStatus{
			ReplicaStatuses: []ReplicaStatus{
				{
					RepoName:     "replica1",
					Region:       "us-east-1",
					InSync:       false,
					Lag:          1024 * 1024 * 50,
					Health:       "degraded",
					LastSyncTime: &now,
				},
			},
		}

		report := GenerateReplicationReport(config, status)
		assert.Assert(t, len(report) > 0)
	})

	t.Run("MultipleReplicas", func(t *testing.T) {
		now := metav1.Now()
		config := &ReplicationConfig{
			Enabled:    true,
			SourceRepo: "repo1",
			ReplicaRepos: []ReplicaRepository{
				{Name: "replica1", Region: "us-east-1"},
				{Name: "replica2", Region: "eu-west-1"},
			},
		}

		status := &ReplicationStatus{
			ReplicaStatuses: []ReplicaStatus{
				{
					RepoName:     "replica1",
					Region:       "us-east-1",
					InSync:       true,
					Health:       "healthy",
					LastSyncTime: &now,
				},
				{
					RepoName:     "replica2",
					Region:       "eu-west-1",
					InSync:       true,
					Health:       "healthy",
					LastSyncTime: &now,
				},
			},
		}

		report := GenerateReplicationReport(config, status)
		assert.Assert(t, len(report) > 0)
	})

	t.Run("NoHealthyReplicas", func(t *testing.T) {
		config := &ReplicationConfig{
			Enabled:    true,
			SourceRepo: "repo1",
			ReplicaRepos: []ReplicaRepository{
				{Name: "replica1", Region: "us-east-1"},
			},
		}

		status := &ReplicationStatus{
			ReplicaStatuses: []ReplicaStatus{
				{
					RepoName: "replica1",
					Region:   "us-east-1",
					InSync:   false,
					Health:   "unhealthy",
				},
			},
		}

		report := GenerateReplicationReport(config, status)
		assert.Assert(t, len(report) > 0)
	})
}

func TestExportReplicationMetrics(t *testing.T) {
	t.Run("BasicMetrics", func(t *testing.T) {
		now := metav1.Now()
		config := &ReplicationConfig{
			SourceRepo: "repo1",
		}

		status := &ReplicationStatus{
			ReplicaStatuses: []ReplicaStatus{
				{
					RepoName:     "replica1",
					Region:       "us-east-1",
					Lag:          1024,
					Health:       "healthy",
					LastSyncTime: &now,
				},
			},
		}

		// Should not panic
		exportReplicationMetrics("test-cluster", "test-ns", config, status)
	})

	t.Run("MultipleReplicas", func(t *testing.T) {
		config := &ReplicationConfig{
			SourceRepo: "repo1",
		}

		status := &ReplicationStatus{
			ReplicaStatuses: []ReplicaStatus{
				{RepoName: "replica1", Region: "us-east-1", Lag: 0, Health: "healthy"},
				{RepoName: "replica2", Region: "eu-west-1", Lag: 2048, Health: "degraded"},
			},
		}

		exportReplicationMetrics("test-cluster", "test-ns", config, status)
	})
}

func TestReplicationCredentials(t *testing.T) {
	t.Run("AWSCredentials", func(t *testing.T) {
		creds := ReplicationCredentials{
			Type:               "aws",
			AWSAccessKeyID:     "AKIAIOSFODNN7EXAMPLE",
			AWSSecretAccessKey: "secret",
			AWSRegion:          "us-east-1",
		}

		assert.Equal(t, creds.Type, "aws")
		assert.Assert(t, creds.AWSAccessKeyID != "")
	})

	t.Run("GCPCredentials", func(t *testing.T) {
		creds := ReplicationCredentials{
			Type:              "gcp",
			GCPServiceAccount: "sa@project.iam.gserviceaccount.com",
			GCPProject:        "my-project",
		}

		assert.Equal(t, creds.Type, "gcp")
	})

	t.Run("AzureCredentials", func(t *testing.T) {
		creds := ReplicationCredentials{
			Type:                "azure",
			AzureStorageAccount: "account",
			AzureStorageKey:     "key",
		}

		assert.Equal(t, creds.Type, "azure")
	})

	t.Run("WithSecret", func(t *testing.T) {
		creds := ReplicationCredentials{
			SecretName: "replication-secret",
		}

		assert.Assert(t, creds.SecretName != "")
	})
}

func TestReplicationConstants(t *testing.T) {
	t.Run("ReplicationModes", func(t *testing.T) {
		assert.Equal(t, ReplicationModeAsync, "async")
		assert.Equal(t, ReplicationModeSync, "sync")
	})

	t.Run("Annotations", func(t *testing.T) {
		assert.Assert(t, AnnotationReplicationEnabled != "")
		assert.Assert(t, AnnotationReplicationMode != "")
		assert.Assert(t, AnnotationReplicationSchedule != "")
	})
}

func TestReconcileReplicationErrors(t *testing.T) {
	t.Run("NilConfig", func(t *testing.T) {
		ctx := context.Background()
		cluster := &v1beta1.PostgresCluster{}

		err := ReconcileReplication(ctx, nil, cluster, nil)
		assert.NilError(t, err)
	})

	t.Run("DisabledConfig", func(t *testing.T) {
		ctx := context.Background()
		cluster := &v1beta1.PostgresCluster{}
		config := &ReplicationConfig{Enabled: false}

		err := ReconcileReplication(ctx, nil, cluster, config)
		assert.NilError(t, err)
	})
}
