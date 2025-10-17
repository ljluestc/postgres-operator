// Copyright 2025 Crunchy Data Solutions, Inc.
//
// SPDX-License-Identifier: Apache-2.0

package pgbackrest

import (
	"context"
	"fmt"
	"strings"
	"time"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/crunchydata/postgres-operator/internal/config"
	"github.com/crunchydata/postgres-operator/internal/initialize"
	"github.com/crunchydata/postgres-operator/internal/naming"
	"github.com/crunchydata/postgres-operator/pkg/apis/postgres-operator.crunchydata.com/v1beta1"
)

const (
	// VerificationMethodChecksum uses pgbackrest verify command
	VerificationMethodChecksum = "checksum"

	// VerificationMethodRestoreTest performs actual restore to test volume
	VerificationMethodRestoreTest = "restore-test"

	// VerificationMethodBoth performs both checksum and restore test
	VerificationMethodBoth = "both"

	// Default timeout for verification operations
	defaultVerificationTimeout = 2 * time.Hour
)

// BackupVerification represents the configuration for automated backup verification
type BackupVerification struct {
	// Enabled determines if verification is active
	Enabled bool

	// Schedule in cron format
	Schedule string

	// RepoName specifies which repository to verify
	RepoName string

	// Method specifies the verification approach
	Method string

	// Resources for verification job
	Resources corev1.ResourceRequirements

	// Timeout for verification
	Timeout time.Duration
}

// VerificationStatus tracks the status of backup verification
type VerificationStatus struct {
	// LastVerificationTime is when verification last ran
	LastVerificationTime *metav1.Time

	// LastVerificationResult indicates success or failure
	LastVerificationResult string

	// LastVerificationMessage contains details about the verification
	LastVerificationMessage string

	// VerificationHistory tracks recent verification attempts
	VerificationHistory []VerificationHistoryEntry
}

// VerificationHistoryEntry represents a single verification attempt
type VerificationHistoryEntry struct {
	Timestamp time.Time
	Result    string
	Duration  time.Duration
	Message   string
}

// CreateVerificationCronJob creates a CronJob for periodic backup verification
func CreateVerificationCronJob(
	ctx context.Context,
	cluster *v1beta1.PostgresCluster,
	verification *BackupVerification,
) (*batchv1.CronJob, error) {

	if !verification.Enabled {
		return nil, nil
	}

	meta := naming.PGBackRestCronJob(cluster, "verify", verification.RepoName)
	meta.Name = fmt.Sprintf("%s-verify-%s", cluster.Name, verification.RepoName)
	meta.Labels = naming.Merge(
		cluster.Spec.Metadata.GetLabelsOrNil(),
		map[string]string{
			naming.LabelPGBackRest:           "",
			naming.LabelPGBackRestBackup:     "",
			naming.LabelPGBackRestRepo:       verification.RepoName,
			naming.LabelPGBackRestCronJob:    "",
		},
	)

	cronJob := &batchv1.CronJob{
		TypeMeta: metav1.TypeMeta{
			APIVersion: batchv1.SchemeGroupVersion.String(),
			Kind:       "CronJob",
		},
		ObjectMeta: meta,
	}

	// Set schedule
	cronJob.Spec.Schedule = verification.Schedule

	// Configure job template
	jobTemplate := &cronJob.Spec.JobTemplate
	jobTemplate.ObjectMeta = meta
	jobTemplate.ObjectMeta.Name = ""

	// Create pod template
	podTemplate := &jobTemplate.Spec.Template
	podTemplate.ObjectMeta = meta
	podTemplate.ObjectMeta.Name = ""

	// Initialize container
	container := corev1.Container{
		Name:            naming.ContainerPGBackRestConfig,
		Image:           config.PGBackRestContainerImage(cluster),
		ImagePullPolicy: cluster.Spec.ImagePullPolicy,
		Command:         createVerificationCommand(verification),
		Resources:       verification.Resources,
		SecurityContext: initialize.RestrictedSecurityContext(),
	}

	// Add environment variables
	container.Env = []corev1.EnvVar{
		{Name: "PGBACKREST_STANZA", Value: DefaultStanzaName},
		{Name: "PGBACKREST_REPO", Value: verification.RepoName[len("repo"):]}, // Extract number from "repoN"
	}

	// Mount configuration
	configVolume := corev1.Volume{
		Name: "pgbackrest-config",
		VolumeSource: corev1.VolumeSource{
			Projected: &corev1.ProjectedVolumeSource{
				DefaultMode: initialize.Int32(0o600),
				Sources: []corev1.VolumeProjection{
					{
						ConfigMap: &corev1.ConfigMapProjection{
							LocalObjectReference: corev1.LocalObjectReference{
								Name: naming.PGBackRestConfig(cluster).Name,
							},
							Items: []corev1.KeyToPath{
								{
									Key:  CMCloudRepoKey,
									Path: "pgbackrest.conf",
								},
							},
						},
					},
				},
			},
		},
	}

	container.VolumeMounts = []corev1.VolumeMount{
		{
			Name:      configVolume.Name,
			MountPath: "/etc/pgbackrest",
			ReadOnly:  true,
		},
	}

	podTemplate.Spec.Containers = []corev1.Container{container}
	podTemplate.Spec.Volumes = []corev1.Volume{configVolume}
	podTemplate.Spec.RestartPolicy = corev1.RestartPolicyOnFailure

	// Set timeout if specified
	if verification.Timeout > 0 {
		timeoutSeconds := int64(verification.Timeout.Seconds())
		jobTemplate.Spec.ActiveDeadlineSeconds = &timeoutSeconds
	}

	return cronJob, nil
}

// createVerificationCommand generates the command to run for verification
func createVerificationCommand(verification *BackupVerification) []string {
	script := []string{"bash", "-ceu", "--"}

	var commands []string

	switch verification.Method {
	case VerificationMethodChecksum:
		commands = append(commands, createChecksumVerificationScript())

	case VerificationMethodRestoreTest:
		commands = append(commands, createRestoreTestVerificationScript())

	case VerificationMethodBoth:
		commands = append(commands,
			"echo 'Running checksum verification...'",
			createChecksumVerificationScript(),
			"echo 'Checksum verification passed'",
			"echo 'Running restore test verification...'",
			createRestoreTestVerificationScript(),
			"echo 'Restore test verification passed'",
		)

	default:
		// Default to checksum
		commands = append(commands, createChecksumVerificationScript())
	}

	// Join commands with newlines
	scriptContent := strings.Join(commands, "\n")
	return append(script, scriptContent)
}

// createChecksumVerificationScript creates script for checksum-based verification
func createChecksumVerificationScript() string {
	return `
set -x

echo "=== Starting Backup Verification (Checksum) ==="
echo "Repository: ${PGBACKREST_REPO}"
echo "Stanza: ${PGBACKREST_STANZA}"
echo "Time: $(date)"

# Run pgbackrest verify
if pgbackrest --stanza="${PGBACKREST_STANZA}" --repo="${PGBACKREST_REPO}" verify; then
    echo "SUCCESS: Backup verification completed successfully"
    exit 0
else
    echo "FAILED: Backup verification failed"
    exit 1
fi
`
}

// createRestoreTestVerificationScript creates script for restore test verification
func createRestoreTestVerificationScript() string {
	return `
set -x

echo "=== Starting Backup Verification (Restore Test) ==="
echo "Repository: ${PGBACKREST_REPO}"
echo "Stanza: ${PGBACKREST_STANZA}"
echo "Time: $(date)"

# Create temporary directory for restore test
TEST_DIR=$(mktemp -d)
trap "rm -rf ${TEST_DIR}" EXIT

echo "Test directory: ${TEST_DIR}"

# Perform delta restore to test directory
if pgbackrest --stanza="${PGBACKREST_STANZA}" \
              --repo="${PGBACKREST_REPO}" \
              --delta \
              --pg1-path="${TEST_DIR}/pgdata" \
              restore; then
    echo "Restore completed successfully"

    # Verify critical files exist
    if [[ -f "${TEST_DIR}/pgdata/PG_VERSION" ]]; then
        PG_VERSION=$(cat "${TEST_DIR}/pgdata/PG_VERSION")
        echo "PostgreSQL version: ${PG_VERSION}"
    else
        echo "FAILED: PG_VERSION file not found"
        exit 1
    fi

    if [[ -f "${TEST_DIR}/pgdata/postgresql.conf" ]]; then
        echo "postgresql.conf found"
    else
        echo "WARNING: postgresql.conf not found"
    fi

    echo "SUCCESS: Restore test verification completed successfully"
    exit 0
else
    echo "FAILED: Restore test verification failed"
    exit 1
fi
`
}

// ReconcileVerification ensures verification CronJob exists and is up to date
func ReconcileVerification(
	ctx context.Context,
	cl client.Client,
	cluster *v1beta1.PostgresCluster,
	verification *BackupVerification,
) error {

	if verification == nil || !verification.Enabled {
		// Delete any existing verification CronJob
		return deleteVerificationCronJob(ctx, cl, cluster, verification)
	}

	// Create or update verification CronJob
	cronJob, err := CreateVerificationCronJob(ctx, cluster, verification)
	if err != nil {
		return fmt.Errorf("failed to create verification CronJob: %w", err)
	}

	// Apply the CronJob
	if err := cl.Patch(ctx, cronJob, client.Apply, client.ForceOwnership, client.FieldOwner("postgres-operator")); err != nil {
		return fmt.Errorf("failed to apply verification CronJob: %w", err)
	}

	return nil
}

// deleteVerificationCronJob removes the verification CronJob if it exists
func deleteVerificationCronJob(
	ctx context.Context,
	cl client.Client,
	cluster *v1beta1.PostgresCluster,
	verification *BackupVerification,
) error {

	if verification == nil {
		return nil
	}

	cronJob := &batchv1.CronJob{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("%s-verify-%s", cluster.Name, verification.RepoName),
			Namespace: cluster.Namespace,
		},
	}

	if err := cl.Delete(ctx, cronJob); client.IgnoreNotFound(err) != nil {
		return fmt.Errorf("failed to delete verification CronJob: %w", err)
	}

	return nil
}

// GetVerificationStatus retrieves the status of backup verification
func GetVerificationStatus(
	ctx context.Context,
	cl client.Client,
	cluster *v1beta1.PostgresCluster,
	repoName string,
) (*VerificationStatus, error) {

	// List recent verification jobs
	jobList := &batchv1.JobList{}
	listOpts := []client.ListOption{
		client.InNamespace(cluster.Namespace),
		client.MatchingLabels{
			naming.LabelPGBackRestBackup: "",
			naming.LabelPGBackRestRepo:   repoName,
		},
	}

	if err := cl.List(ctx, jobList, listOpts...); err != nil {
		return nil, fmt.Errorf("failed to list verification jobs: %w", err)
	}

	status := &VerificationStatus{
		VerificationHistory: make([]VerificationHistoryEntry, 0),
	}

	// Process job results
	for _, job := range jobList.Items {
		entry := VerificationHistoryEntry{
			Timestamp: job.CreationTimestamp.Time,
		}

		if job.Status.CompletionTime != nil {
			entry.Duration = job.Status.CompletionTime.Sub(job.Status.StartTime.Time)
		}

		if job.Status.Succeeded > 0 {
			entry.Result = "Success"
			entry.Message = "Verification completed successfully"
		} else if job.Status.Failed > 0 {
			entry.Result = "Failed"
			entry.Message = "Verification failed"
		} else if job.Status.Active > 0 {
			entry.Result = "Running"
			entry.Message = "Verification in progress"
		} else {
			entry.Result = "Unknown"
			entry.Message = "Verification status unknown"
		}

		status.VerificationHistory = append(status.VerificationHistory, entry)

		// Update last verification if this is the most recent
		if status.LastVerificationTime == nil || job.CreationTimestamp.After(status.LastVerificationTime.Time) {
			status.LastVerificationTime = &job.CreationTimestamp
			status.LastVerificationResult = entry.Result
			status.LastVerificationMessage = entry.Message
		}
	}

	return status, nil
}
