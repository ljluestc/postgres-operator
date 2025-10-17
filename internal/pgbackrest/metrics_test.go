// Copyright 2025 Crunchy Data Solutions, Inc.
//
// SPDX-License-Identifier: Apache-2.0

package pgbackrest

import (
	"context"
	"testing"
	"time"

	"gotest.tools/v3/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/crunchydata/postgres-operator/pkg/apis/postgres-operator.crunchydata.com/v1beta1"
)

func TestCollectBackupMetrics(t *testing.T) {
	ctx := context.Background()

	cluster := &v1beta1.PostgresCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-cluster",
			Namespace: "test-namespace",
		},
	}

	t.Run("ValidJSON", func(t *testing.T) {
		infoJSON := `[{
			"name": "db",
			"repo": [{"key": 1, "status": "ok"}],
			"backup": [
				{
					"label": "20250116-020000F",
					"type": "full",
					"timestamp": {"start": 1705368000, "stop": 1705368600},
					"database": {"id": 1},
					"archive": {"start": "000000010000000000000001", "stop": "000000010000000000000002"},
					"info": {"size": 1073741824, "delta": 1073741824}
				}
			],
			"archive": [
				{"id": "16.1", "min": "000000010000000000000001", "max": "000000010000000000000010"}
			]
		}]`

		err := CollectBackupMetrics(ctx, cluster, "repo1", infoJSON)
		assert.NilError(t, err)
	})

	t.Run("InvalidJSON", func(t *testing.T) {
		infoJSON := `invalid json`

		err := CollectBackupMetrics(ctx, cluster, "repo1", infoJSON)
		assert.ErrorContains(t, err, "failed to parse")
	})

	t.Run("EmptyStanzaInfo", func(t *testing.T) {
		infoJSON := `[]`

		err := CollectBackupMetrics(ctx, cluster, "repo1", infoJSON)
		assert.ErrorContains(t, err, "no stanza information")
	})

	t.Run("InvalidRepoName", func(t *testing.T) {
		infoJSON := `[{
			"name": "db",
			"repo": [{"key": 1, "status": "ok"}]
		}]`

		err := CollectBackupMetrics(ctx, cluster, "invalid", infoJSON)
		assert.ErrorContains(t, err, "invalid repo name")
	})

	t.Run("RepoNotFound", func(t *testing.T) {
		infoJSON := `[{
			"name": "db",
			"repo": [{"key": 1, "status": "ok"}]
		}]`

		err := CollectBackupMetrics(ctx, cluster, "repo2", infoJSON)
		assert.ErrorContains(t, err, "repository repo2 not found")
	})

	t.Run("MultipleBackupTypes", func(t *testing.T) {
		infoJSON := `[{
			"name": "db",
			"repo": [{"key": 1, "status": "ok"}],
			"backup": [
				{
					"label": "20250116-020000F",
					"type": "full",
					"timestamp": {"start": 1705368000, "stop": 1705368600},
					"database": {"id": 1},
					"archive": {"start": "000000010000000000000001", "stop": "000000010000000000000002"},
					"info": {"size": 1073741824, "delta": 1073741824}
				},
				{
					"label": "20250116-030000D",
					"type": "diff",
					"timestamp": {"start": 1705371600, "stop": 1705372200},
					"database": {"id": 1},
					"archive": {"start": "000000010000000000000002", "stop": "000000010000000000000003"},
					"info": {"size": 104857600, "delta": 104857600}
				},
				{
					"label": "20250116-040000I",
					"type": "incr",
					"timestamp": {"start": 1705375200, "stop": 1705375800},
					"database": {"id": 1},
					"archive": {"start": "000000010000000000000003", "stop": "000000010000000000000004"},
					"info": {"size": 10485760, "delta": 10485760}
				}
			]
		}]`

		err := CollectBackupMetrics(ctx, cluster, "repo1", infoJSON)
		assert.NilError(t, err)
	})

	t.Run("BackupWithError", func(t *testing.T) {
		infoJSON := `[{
			"name": "db",
			"repo": [{"key": 1, "status": "ok"}],
			"backup": [
				{
					"label": "20250116-020000F",
					"type": "full",
					"timestamp": {"start": 1705368000, "stop": 1705368600},
					"database": {"id": 1},
					"archive": {"start": "000000010000000000000001", "stop": "000000010000000000000002"},
					"info": {"size": 1073741824, "delta": 1073741824},
					"error": true
				}
			]
		}]`

		err := CollectBackupMetrics(ctx, cluster, "repo1", infoJSON)
		assert.NilError(t, err)
	})
}

func TestCollectBackupInfo(t *testing.T) {
	backups := []BackupInfo{
		{
			Label: "20250116-020000F",
			Type:  "full",
			Start: BackupTimestamp{Start: 1705368000, Stop: 1705368600},
			Info:  BackupInfoDetail{Size: 1073741824, Delta: 1073741824},
		},
		{
			Label: "20250116-030000D",
			Type:  "diff",
			Start: BackupTimestamp{Start: 1705371600, Stop: 1705372200},
			Info:  BackupInfoDetail{Size: 104857600, Delta: 104857600},
		},
		{
			Label: "20250116-040000I",
			Type:  "incr",
			Start: BackupTimestamp{Start: 1705375200, Stop: 1705375800},
			Info:  BackupInfoDetail{Size: 10485760, Delta: 10485760},
		},
	}

	// This function updates Prometheus metrics, so we just verify it doesn't panic
	collectBackupInfo("test-cluster", "test-namespace", "repo1", backups)
}

func TestCollectArchiveInfo(t *testing.T) {
	archives := []ArchiveInfo{
		{
			ID:  "16.1",
			Min: stringPtr("000000010000000000000001"),
			Max: stringPtr("000000010000000000000010"),
		},
	}

	// This function is a stub in the implementation, verify it doesn't panic
	collectArchiveInfo("test-cluster", "test-namespace", "repo1", archives)
}

func TestRecordBackupStart(t *testing.T) {
	cluster := &v1beta1.PostgresCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-cluster",
			Namespace: "test-namespace",
		},
	}

	// Verify function doesn't panic (it's currently a stub)
	RecordBackupStart(cluster, "repo1", "full")
}

func TestRecordBackupCompletion(t *testing.T) {
	cluster := &v1beta1.PostgresCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-cluster",
			Namespace: "test-namespace",
		},
	}

	t.Run("SuccessfulBackup", func(t *testing.T) {
		RecordBackupCompletion(
			cluster,
			"repo1",
			"full",
			true,
			10*time.Minute,
			1073741824,
		)
	})

	t.Run("FailedBackup", func(t *testing.T) {
		RecordBackupCompletion(
			cluster,
			"repo1",
			"full",
			false,
			5*time.Minute,
			0,
		)
	})

	t.Run("SuccessfulBackupNoSize", func(t *testing.T) {
		RecordBackupCompletion(
			cluster,
			"repo1",
			"diff",
			true,
			8*time.Minute,
			0,
		)
	})
}

func TestUpdateRepositoryUtilization(t *testing.T) {
	cluster := &v1beta1.PostgresCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-cluster",
			Namespace: "test-namespace",
		},
	}

	t.Run("NormalUtilization", func(t *testing.T) {
		UpdateRepositoryUtilization(cluster, "repo1", 45.5)
	})

	t.Run("HighUtilization", func(t *testing.T) {
		UpdateRepositoryUtilization(cluster, "repo1", 95.0)
	})

	t.Run("LowUtilization", func(t *testing.T) {
		UpdateRepositoryUtilization(cluster, "repo1", 5.0)
	})
}

func TestUpdateVerificationMetrics(t *testing.T) {
	cluster := &v1beta1.PostgresCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-cluster",
			Namespace: "test-namespace",
		},
	}

	t.Run("SuccessfulVerification", func(t *testing.T) {
		UpdateVerificationMetrics(
			cluster,
			"repo1",
			true,
			time.Now().Add(-1*time.Hour),
		)
	})

	t.Run("FailedVerification", func(t *testing.T) {
		UpdateVerificationMetrics(
			cluster,
			"repo1",
			false,
			time.Now().Add(-2*time.Hour),
		)
	})
}

func TestResetMetrics(t *testing.T) {
	cluster := &v1beta1.PostgresCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-cluster",
			Namespace: "test-namespace",
		},
	}

	// Verify function doesn't panic (it's currently a stub)
	ResetMetrics(cluster)
}

func TestPGBackRestInfo(t *testing.T) {
	info := PGBackRestInfo{
		Name:   "db",
		Cipher: "aes-256-cbc",
		Repo: []RepositoryInfo{
			{Key: 1, Status: "ok"},
		},
		Backup: []BackupInfo{
			{
				Label: "20250116-020000F",
				Type:  "full",
				Start: BackupTimestamp{Start: 1705368000, Stop: 1705368600},
				Info:  BackupInfoDetail{Size: 1073741824, Delta: 1073741824},
			},
		},
		Archive: []ArchiveInfo{
			{
				ID:  "16.1",
				Min: stringPtr("000000010000000000000001"),
				Max: stringPtr("000000010000000000000010"),
			},
		},
	}

	assert.Equal(t, info.Name, "db")
	assert.Equal(t, info.Cipher, "aes-256-cbc")
	assert.Equal(t, len(info.Repo), 1)
	assert.Equal(t, len(info.Backup), 1)
	assert.Equal(t, len(info.Archive), 1)
}

func TestBackupInfo(t *testing.T) {
	backup := BackupInfo{
		Label: "20250116-020000F",
		Type:  "full",
		Start: BackupTimestamp{Start: 1705368000, Stop: 1705368600},
		Database: BackupDatabase{ID: 1},
		Archive: BackupArchive{
			Start: "000000010000000000000001",
			Stop:  "000000010000000000000002",
		},
		Info:  BackupInfoDetail{Size: 1073741824, Delta: 1073741824},
		Error: false,
	}

	assert.Equal(t, backup.Label, "20250116-020000F")
	assert.Equal(t, backup.Type, "full")
	assert.Equal(t, backup.Start.Start, int64(1705368000))
	assert.Equal(t, backup.Start.Stop, int64(1705368600))
	assert.Equal(t, backup.Info.Size, int64(1073741824))
	assert.Assert(t, !backup.Error)
}

func TestArchiveInfo(t *testing.T) {
	archive := ArchiveInfo{
		ID:  "16.1",
		Min: stringPtr("000000010000000000000001"),
		Max: stringPtr("000000010000000000000010"),
		Database: &ArchiveDB{ID: 1},
	}

	assert.Equal(t, archive.ID, "16.1")
	assert.Assert(t, archive.Min != nil)
	assert.Equal(t, *archive.Min, "000000010000000000000001")
	assert.Assert(t, archive.Max != nil)
	assert.Equal(t, *archive.Max, "000000010000000000000010")
	assert.Assert(t, archive.Database != nil)
	assert.Equal(t, archive.Database.ID, 1)
}

// Helper function
func stringPtr(s string) *string {
	return &s
}
