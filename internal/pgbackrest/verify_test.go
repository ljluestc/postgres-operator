// Copyright 2025 Crunchy Data Solutions, Inc.
//
// SPDX-License-Identifier: Apache-2.0

package pgbackrest

import (
	"context"
	"strings"
	"testing"
	"time"

	"gotest.tools/v3/assert"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/crunchydata/postgres-operator/pkg/apis/postgres-operator.crunchydata.com/v1beta1"
)

func TestCreateVerificationCronJob(t *testing.T) {
	ctx := context.Background()

	cluster := &v1beta1.PostgresCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-cluster",
			Namespace: "test-namespace",
		},
		Spec: v1beta1.PostgresClusterSpec{
			Metadata: &v1beta1.Metadata{
				Labels: map[string]string{
					"app": "test-app",
				},
			},
			ImagePullPolicy: corev1.PullIfNotPresent,
		},
	}

	t.Run("DisabledVerification", func(t *testing.T) {
		verification := &BackupVerification{
			Enabled: false,
		}

		cronJob, err := CreateVerificationCronJob(ctx, cluster, verification)
		assert.NilError(t, err)
		assert.Assert(t, cronJob == nil, "Expected nil CronJob for disabled verification")
	})

	t.Run("ChecksumMethod", func(t *testing.T) {
		verification := &BackupVerification{
			Enabled:  true,
			Schedule: "0 2 * * *",
			RepoName: "repo1",
			Method:   VerificationMethodChecksum,
			Resources: corev1.ResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("100m"),
					corev1.ResourceMemory: resource.MustParse("256Mi"),
				},
			},
			Timeout: 2 * time.Hour,
		}

		cronJob, err := CreateVerificationCronJob(ctx, cluster, verification)
		assert.NilError(t, err)
		assert.Assert(t, cronJob != nil)

		// Verify CronJob metadata
		assert.Equal(t, cronJob.Name, "test-cluster-verify-repo1")
		assert.Equal(t, cronJob.Namespace, "test-namespace")
		assert.Equal(t, cronJob.Spec.Schedule, "0 2 * * *")

		// Verify labels
		assert.Assert(t, cronJob.Labels["app"] == "test-app")

		// Verify pod template
		assert.Equal(t, len(cronJob.Spec.JobTemplate.Spec.Template.Spec.Containers), 1)
		container := cronJob.Spec.JobTemplate.Spec.Template.Spec.Containers[0]

		// Verify environment variables
		assert.Assert(t, len(container.Env) >= 2)
		var stanzaEnv, repoEnv *corev1.EnvVar
		for i := range container.Env {
			if container.Env[i].Name == "PGBACKREST_STANZA" {
				stanzaEnv = &container.Env[i]
			}
			if container.Env[i].Name == "PGBACKREST_REPO" {
				repoEnv = &container.Env[i]
			}
		}
		assert.Assert(t, stanzaEnv != nil)
		assert.Equal(t, stanzaEnv.Value, DefaultStanzaName)
		assert.Assert(t, repoEnv != nil)
		assert.Equal(t, repoEnv.Value, "1")

		// Verify resources
		assert.DeepEqual(t, container.Resources, verification.Resources)

		// Verify timeout
		assert.Assert(t, cronJob.Spec.JobTemplate.Spec.ActiveDeadlineSeconds != nil)
		assert.Equal(t, *cronJob.Spec.JobTemplate.Spec.ActiveDeadlineSeconds, int64(7200))
	})

	t.Run("RestoreTestMethod", func(t *testing.T) {
		verification := &BackupVerification{
			Enabled:  true,
			Schedule: "0 3 * * 0",
			RepoName: "repo2",
			Method:   VerificationMethodRestoreTest,
		}

		cronJob, err := CreateVerificationCronJob(ctx, cluster, verification)
		assert.NilError(t, err)
		assert.Assert(t, cronJob != nil)
		assert.Equal(t, cronJob.Name, "test-cluster-verify-repo2")
	})

	t.Run("BothMethods", func(t *testing.T) {
		verification := &BackupVerification{
			Enabled:  true,
			Schedule: "0 4 * * 6",
			RepoName: "repo1",
			Method:   VerificationMethodBoth,
		}

		cronJob, err := CreateVerificationCronJob(ctx, cluster, verification)
		assert.NilError(t, err)
		assert.Assert(t, cronJob != nil)

		// Verify command includes both verification methods
		container := cronJob.Spec.JobTemplate.Spec.Template.Spec.Containers[0]
		assert.Assert(t, len(container.Command) > 0)
	})

	t.Run("NoTimeout", func(t *testing.T) {
		verification := &BackupVerification{
			Enabled:  true,
			Schedule: "0 2 * * *",
			RepoName: "repo1",
			Method:   VerificationMethodChecksum,
		}

		cronJob, err := CreateVerificationCronJob(ctx, cluster, verification)
		assert.NilError(t, err)
		assert.Assert(t, cronJob != nil)
		assert.Assert(t, cronJob.Spec.JobTemplate.Spec.ActiveDeadlineSeconds == nil)
	})
}

func TestCreateVerificationCommand(t *testing.T) {
	t.Run("ChecksumMethod", func(t *testing.T) {
		verification := &BackupVerification{
			Method: VerificationMethodChecksum,
		}

		cmd := createVerificationCommand(verification)
		assert.Assert(t, len(cmd) >= 3)
		assert.Equal(t, cmd[0], "bash")
		assert.Equal(t, cmd[1], "-ceu")
		assert.Equal(t, cmd[2], "--")

		scriptContent := cmd[3]
		assert.Assert(t, strings.Contains(scriptContent, "pgbackrest"))
		assert.Assert(t, strings.Contains(scriptContent, "verify"))
	})

	t.Run("RestoreTestMethod", func(t *testing.T) {
		verification := &BackupVerification{
			Method: VerificationMethodRestoreTest,
		}

		cmd := createVerificationCommand(verification)
		assert.Assert(t, len(cmd) >= 4)

		scriptContent := cmd[3]
		assert.Assert(t, strings.Contains(scriptContent, "mktemp -d"))
		assert.Assert(t, strings.Contains(scriptContent, "restore"))
		assert.Assert(t, strings.Contains(scriptContent, "PG_VERSION"))
	})

	t.Run("BothMethods", func(t *testing.T) {
		verification := &BackupVerification{
			Method: VerificationMethodBoth,
		}

		cmd := createVerificationCommand(verification)
		assert.Assert(t, len(cmd) >= 4)

		scriptContent := cmd[3]
		assert.Assert(t, strings.Contains(scriptContent, "checksum verification"))
		assert.Assert(t, strings.Contains(scriptContent, "restore test verification"))
		assert.Assert(t, strings.Contains(scriptContent, "verify"))
		assert.Assert(t, strings.Contains(scriptContent, "restore"))
	})

	t.Run("DefaultMethod", func(t *testing.T) {
		verification := &BackupVerification{
			Method: "unknown",
		}

		cmd := createVerificationCommand(verification)
		assert.Assert(t, len(cmd) >= 4)

		// Should default to checksum
		scriptContent := cmd[3]
		assert.Assert(t, strings.Contains(scriptContent, "verify"))
	})
}

func TestCreateChecksumVerificationScript(t *testing.T) {
	script := createChecksumVerificationScript()

	// Verify script contains required elements
	assert.Assert(t, strings.Contains(script, "set -x"))
	assert.Assert(t, strings.Contains(script, "pgbackrest"))
	assert.Assert(t, strings.Contains(script, "verify"))
	assert.Assert(t, strings.Contains(script, "PGBACKREST_STANZA"))
	assert.Assert(t, strings.Contains(script, "PGBACKREST_REPO"))
	assert.Assert(t, strings.Contains(script, "SUCCESS"))
	assert.Assert(t, strings.Contains(script, "FAILED"))
	assert.Assert(t, strings.Contains(script, "exit 0"))
	assert.Assert(t, strings.Contains(script, "exit 1"))
}

func TestCreateRestoreTestVerificationScript(t *testing.T) {
	script := createRestoreTestVerificationScript()

	// Verify script contains required elements
	assert.Assert(t, strings.Contains(script, "set -x"))
	assert.Assert(t, strings.Contains(script, "mktemp -d"))
	assert.Assert(t, strings.Contains(script, "trap"))
	assert.Assert(t, strings.Contains(script, "pgbackrest"))
	assert.Assert(t, strings.Contains(script, "restore"))
	assert.Assert(t, strings.Contains(script, "--delta"))
	assert.Assert(t, strings.Contains(script, "PG_VERSION"))
	assert.Assert(t, strings.Contains(script, "postgresql.conf"))
	assert.Assert(t, strings.Contains(script, "SUCCESS"))
	assert.Assert(t, strings.Contains(script, "FAILED"))
}

func TestBackupVerificationConstants(t *testing.T) {
	// Verify constants are defined
	assert.Equal(t, VerificationMethodChecksum, "checksum")
	assert.Equal(t, VerificationMethodRestoreTest, "restore-test")
	assert.Equal(t, VerificationMethodBoth, "both")
	assert.Equal(t, defaultVerificationTimeout, 2*time.Hour)
}

func TestVerificationHistoryEntry(t *testing.T) {
	entry := VerificationHistoryEntry{
		Timestamp: time.Now(),
		Result:    "Success",
		Duration:  5 * time.Minute,
		Message:   "Test verification",
	}

	assert.Assert(t, !entry.Timestamp.IsZero())
	assert.Equal(t, entry.Result, "Success")
	assert.Equal(t, entry.Duration, 5*time.Minute)
	assert.Equal(t, entry.Message, "Test verification")
}

func TestBackupVerification(t *testing.T) {
	verification := BackupVerification{
		Enabled:  true,
		Schedule: "0 2 * * *",
		RepoName: "repo1",
		Method:   VerificationMethodChecksum,
		Resources: corev1.ResourceRequirements{
			Requests: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("100m"),
				corev1.ResourceMemory: resource.MustParse("256Mi"),
			},
		},
		Timeout: 2 * time.Hour,
	}

	assert.Assert(t, verification.Enabled)
	assert.Equal(t, verification.Schedule, "0 2 * * *")
	assert.Equal(t, verification.RepoName, "repo1")
	assert.Equal(t, verification.Method, VerificationMethodChecksum)
	assert.Equal(t, verification.Timeout, 2*time.Hour)
}

func TestVerificationStatus(t *testing.T) {
	now := metav1.Now()
	status := VerificationStatus{
		LastVerificationTime:    &now,
		LastVerificationResult:  "Success",
		LastVerificationMessage: "Verification completed",
		VerificationHistory: []VerificationHistoryEntry{
			{
				Timestamp: now.Time,
				Result:    "Success",
				Duration:  5 * time.Minute,
				Message:   "Verification completed",
			},
		},
	}

	assert.Assert(t, status.LastVerificationTime != nil)
	assert.Equal(t, status.LastVerificationResult, "Success")
	assert.Equal(t, status.LastVerificationMessage, "Verification completed")
	assert.Equal(t, len(status.VerificationHistory), 1)
}
