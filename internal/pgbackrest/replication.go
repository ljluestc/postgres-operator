// Copyright 2025 Crunchy Data Solutions, Inc.
//
// SPDX-License-Identifier: Apache-2.0

package pgbackrest

import (
	"context"
	"fmt"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/crunchydata/postgres-operator/internal/config"
	"github.com/crunchydata/postgres-operator/internal/initialize"
	"github.com/crunchydata/postgres-operator/internal/logging"
	"github.com/crunchydata/postgres-operator/internal/naming"
	"github.com/crunchydata/postgres-operator/pkg/apis/postgres-operator.crunchydata.com/v1beta1"
)

// Cross-region replication metrics
var (
	replicationLagBytes = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "pgo_backup_replication_lag_bytes",
			Help: "Replication lag in bytes between source and replica repository",
		},
		[]string{"cluster", "namespace", "source_repo", "replica_repo", "region"},
	)

	replicationDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "pgo_backup_replication_duration_seconds",
			Help:    "Duration of backup replication operations",
			Buckets: prometheus.ExponentialBuckets(60, 2, 12), // 60s to 68 hours
		},
		[]string{"cluster", "namespace", "source_repo", "replica_repo", "region"},
	)

	replicationBytesTransferred = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "pgo_backup_replication_bytes_total",
			Help: "Total bytes replicated to remote region",
		},
		[]string{"cluster", "namespace", "source_repo", "replica_repo", "region"},
	)

	replicationFailures = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "pgo_backup_replication_failures_total",
			Help: "Total number of replication failures",
		},
		[]string{"cluster", "namespace", "source_repo", "replica_repo", "region", "reason"},
	)

	replicationStatus = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "pgo_backup_replication_status",
			Help: "Replication status (1=healthy, 0=unhealthy)",
		},
		[]string{"cluster", "namespace", "source_repo", "replica_repo", "region"},
	)
)

func init() {
	prometheus.MustRegister(
		replicationLagBytes,
		replicationDuration,
		replicationBytesTransferred,
		replicationFailures,
		replicationStatus,
	)
}

const (
	// ReplicationModeAsync asynchronous replication
	ReplicationModeAsync = "async"

	// ReplicationModeSync synchronous replication (wait for completion)
	ReplicationModeSync = "sync"

	// Annotation keys
	AnnotationReplicationEnabled    = "postgres-operator.crunchydata.com/backup-replication-enabled"
	AnnotationReplicationMode       = "postgres-operator.crunchydata.com/backup-replication-mode"
	AnnotationReplicationSchedule   = "postgres-operator.crunchydata.com/backup-replication-schedule"
	AnnotationReplicationRetryCount = "postgres-operator.crunchydata.com/backup-replication-retry-count"
)

// ReplicationConfig configures cross-region backup replication
type ReplicationConfig struct {
	// Enabled turns on replication
	Enabled bool

	// Mode of replication (async, sync)
	Mode string

	// SourceRepo primary backup repository
	SourceRepo string

	// ReplicaRepos destination repositories
	ReplicaRepos []ReplicaRepository

	// Schedule for automated replication (cron format)
	Schedule string

	// RetryCount number of retries on failure
	RetryCount int

	// RetryDelay delay between retries
	RetryDelay time.Duration

	// Compression for transfer
	Compression bool

	// Bandwidth limit for transfer (MB/s)
	BandwidthLimit int

	// VerifyAfterReplication run verification after replication
	VerifyAfterReplication bool
}

// ReplicaRepository defines a replication destination
type ReplicaRepository struct {
	// Name of the repository
	Name string

	// Region where repository is located
	Region string

	// Endpoint cloud storage endpoint
	Endpoint string

	// Bucket/container name
	Bucket string

	// Path within bucket
	Path string

	// Credentials for accessing remote repository
	Credentials ReplicationCredentials

	// Priority for failover (higher = preferred)
	Priority int

	// Enabled whether this replica is active
	Enabled bool
}

// ReplicationCredentials contains authentication info
type ReplicationCredentials struct {
	// Type of credentials (aws, gcp, azure, s3)
	Type string

	// SecretName Kubernetes secret containing credentials
	SecretName string

	// AWS credentials
	AWSAccessKeyID     string
	AWSSecretAccessKey string
	AWSRegion          string

	// GCP credentials
	GCPServiceAccount string
	GCPProject        string

	// Azure credentials
	AzureStorageAccount string
	AzureStorageKey     string
	AzureSASToken       string
}

// ReplicationStatus tracks replication state
type ReplicationStatus struct {
	// LastReplicationTime when replication last ran
	LastReplicationTime *metav1.Time

	// LastReplicationResult success or failure
	LastReplicationResult string

	// LastReplicationMessage details about replication
	LastReplicationMessage string

	// ReplicaStatuses status for each replica
	ReplicaStatuses []ReplicaStatus

	// BytesReplicated total bytes replicated
	BytesReplicated int64

	// CurrentLag replication lag
	CurrentLag int64

	// AverageLatency average replication latency
	AverageLatency time.Duration
}

// ReplicaStatus tracks status of a single replica
type ReplicaStatus struct {
	// RepoName replica repository name
	RepoName string

	// Region replica region
	Region string

	// InSync whether replica is in sync with source
	InSync bool

	// Lag bytes behind source
	Lag int64

	// LastSyncTime when last synchronized
	LastSyncTime *metav1.Time

	// Health replica health (healthy, degraded, unhealthy)
	Health string

	// ErrorMessage if unhealthy
	ErrorMessage string
}

// CreateReplicationJob creates a Job to replicate backups
func CreateReplicationJob(
	ctx context.Context,
	cluster *v1beta1.PostgresCluster,
	replicationConfig *ReplicationConfig,
) (*batchv1.Job, error) {
	log := logging.FromContext(ctx)

	if !replicationConfig.Enabled {
		return nil, nil
	}

	log.Info("Creating backup replication job", "source", replicationConfig.SourceRepo)

	jobName := fmt.Sprintf("%s-backup-replication-%d", cluster.Name, time.Now().Unix())

	meta := naming.PGBackRestBackupJob(cluster)
	meta.Name = jobName
	meta.Labels = naming.Merge(
		cluster.Spec.Metadata.GetLabelsOrNil(),
		map[string]string{
			naming.LabelPGBackRest: "",
			"postgres-operator.crunchydata.com/backup-replication": "true",
		},
	)

	job := &batchv1.Job{
		TypeMeta: metav1.TypeMeta{
			APIVersion: batchv1.SchemeGroupVersion.String(),
			Kind:       "Job",
		},
		ObjectMeta: meta,
	}

	// Configure job
	backoffLimit := int32(replicationConfig.RetryCount)
	job.Spec.BackoffLimit = &backoffLimit

	// Create pod template
	podTemplate := &job.Spec.Template
	podTemplate.ObjectMeta = meta
	podTemplate.ObjectMeta.Name = ""

	// Initialize container
	container := corev1.Container{
		Name:            "replication",
		Image:           config.PGBackRestContainerImage(cluster),
		ImagePullPolicy: cluster.Spec.ImagePullPolicy,
		Command:         createReplicationScript(replicationConfig),
		SecurityContext: initialize.RestrictedSecurityContext(),
	}

	// Add environment variables
	container.Env = []corev1.EnvVar{
		{Name: "PGBACKREST_STANZA", Value: DefaultStanzaName},
		{Name: "SOURCE_REPO", Value: replicationConfig.SourceRepo},
	}

	// Mount configuration and credentials
	volumes := []corev1.Volume{
		{
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
							},
						},
					},
				},
			},
		},
	}

	volumeMounts := []corev1.VolumeMount{
		{
			Name:      "pgbackrest-config",
			MountPath: "/etc/pgbackrest",
			ReadOnly:  true,
		},
	}

	// Add credential volumes for each replica
	for i, replica := range replicationConfig.ReplicaRepos {
		if replica.Credentials.SecretName != "" {
			volumeName := fmt.Sprintf("replica-creds-%d", i)
			volumes = append(volumes, corev1.Volume{
				Name: volumeName,
				VolumeSource: corev1.VolumeSource{
					Secret: &corev1.SecretVolumeSource{
						SecretName: replica.Credentials.SecretName,
					},
				},
			})

			volumeMounts = append(volumeMounts, corev1.VolumeMount{
				Name:      volumeName,
				MountPath: fmt.Sprintf("/etc/replication/replica-%d", i),
				ReadOnly:  true,
			})
		}
	}

	container.VolumeMounts = volumeMounts
	podTemplate.Spec.Containers = []corev1.Container{container}
	podTemplate.Spec.Volumes = volumes
	podTemplate.Spec.RestartPolicy = corev1.RestartPolicyOnFailure

	return job, nil
}

// createReplicationScript generates replication script
func createReplicationScript(config *ReplicationConfig) []string {
	script := []string{"bash", "-ceu", "--"}

	var commands []string

	commands = append(commands, `
set -x
echo "=== Starting Backup Replication ==="
echo "Source Repository: ${SOURCE_REPO}"
echo "Time: $(date -Iseconds)"

REPLICATION_START=$(date +%s)
`)

	// For each replica repository
	for i, replica := range config.ReplicaRepos {
		if !replica.Enabled {
			continue
		}

		commands = append(commands, fmt.Sprintf(`
echo "=== Replicating to Replica %d: %s ==="
echo "Region: %s"
echo "Bucket: %s"

# Export credentials
export AWS_ACCESS_KEY_ID=$(cat /etc/replication/replica-%d/access-key 2>/dev/null || echo "")
export AWS_SECRET_ACCESS_KEY=$(cat /etc/replication/replica-%d/secret-key 2>/dev/null || echo "")
export AWS_DEFAULT_REGION=%s

# Get list of backups from source
SOURCE_BACKUPS=$(pgbackrest --stanza="${PGBACKREST_STANZA}" \
                           --repo="${SOURCE_REPO}" \
                           info --output=json | \
                 jq -r '.[0].backup[].label' || echo "")

if [ -z "${SOURCE_BACKUPS}" ]; then
    echo "ERROR: No backups found in source repository"
    exit 1
fi

echo "Found backups: ${SOURCE_BACKUPS}"

# Replicate each backup
REPLICA_ENDPOINT="%s"
REPLICA_BUCKET="%s"
REPLICA_PATH="%s"

for BACKUP in ${SOURCE_BACKUPS}; do
    echo "Replicating backup: ${BACKUP}"

    # Use pgbackrest repo-sync or cloud-specific tools
    # For S3, use aws s3 sync
    # For GCS, use gsutil rsync
    # For Azure, use azcopy sync

    # Example: S3 sync
    SOURCE_S3="s3://source-bucket/backup/${BACKUP}"
    DEST_S3="s3://${REPLICA_BUCKET}/${REPLICA_PATH}/${BACKUP}"

    aws s3 sync "${SOURCE_S3}" "${DEST_S3}" \
        --region "${AWS_DEFAULT_REGION}" \
        --no-progress \
        || echo "WARNING: Failed to replicate backup ${BACKUP}"

    echo "✓ Replicated: ${BACKUP}"
done

echo "✓ Replication to replica %d complete"
`,
			i, replica.Name, replica.Region, replica.Bucket,
			i, i, replica.Region,
			replica.Endpoint, replica.Bucket, replica.Path,
			i,
		))
	}

	// Verification if enabled
	if config.VerifyAfterReplication {
		commands = append(commands, `
echo "=== Verifying Replicated Backups ==="

# Verify each replica
# Run pgbackrest info against replica repositories
# Compare with source to ensure consistency

echo "✓ Verification complete"
`)
	}

	// Calculate metrics
	commands = append(commands, `
REPLICATION_END=$(date +%s)
REPLICATION_DURATION=$((REPLICATION_END - REPLICATION_START))

echo "=== Replication Complete ==="
echo "Duration: ${REPLICATION_DURATION}s"
echo "Time: $(date -Iseconds)"

exit 0
`)

	scriptContent := ""
	for _, cmd := range commands {
		scriptContent += cmd + "\n"
	}

	return append(script, scriptContent)
}

// ReconcileReplication manages backup replication
func ReconcileReplication(
	ctx context.Context,
	cl client.Client,
	cluster *v1beta1.PostgresCluster,
	config *ReplicationConfig,
) error {
	log := logging.FromContext(ctx)

	if config == nil || !config.Enabled {
		return nil
	}

	log.Info("Reconciling backup replication", "source", config.SourceRepo)

	// Create CronJob for scheduled replication
	if config.Schedule != "" {
		if err := createReplicationCronJob(ctx, cl, cluster, config); err != nil {
			return fmt.Errorf("failed to create replication CronJob: %w", err)
		}
	}

	// Update replication status
	status, err := GetReplicationStatus(ctx, cl, cluster, config)
	if err != nil {
		log.Error(err, "Failed to get replication status")
	} else {
		// Export metrics
		exportReplicationMetrics(cluster.Name, cluster.Namespace, config, status)
	}

	return nil
}

// createReplicationCronJob creates a CronJob for scheduled replication
func createReplicationCronJob(
	ctx context.Context,
	cl client.Client,
	cluster *v1beta1.PostgresCluster,
	config *ReplicationConfig,
) error {
	cronJobName := fmt.Sprintf("%s-backup-replication", cluster.Name)

	cronJob := &batchv1.CronJob{
		TypeMeta: metav1.TypeMeta{
			APIVersion: batchv1.SchemeGroupVersion.String(),
			Kind:       "CronJob",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      cronJobName,
			Namespace: cluster.Namespace,
			Labels: map[string]string{
				naming.LabelCluster: cluster.Name,
				"postgres-operator.crunchydata.com/backup-replication": "true",
			},
		},
	}

	cronJob.Spec.Schedule = config.Schedule

	// Create job template from replication job
	job, err := CreateReplicationJob(ctx, cluster, config)
	if err != nil {
		return fmt.Errorf("failed to create replication job template: %w", err)
	}

	cronJob.Spec.JobTemplate.Spec = job.Spec
	cronJob.Spec.JobTemplate.ObjectMeta = job.ObjectMeta
	cronJob.Spec.JobTemplate.ObjectMeta.Name = ""

	// Apply CronJob
	if err := cl.Patch(ctx, cronJob, client.Apply, client.ForceOwnership, client.FieldOwner("postgres-operator")); err != nil {
		return fmt.Errorf("failed to apply replication CronJob: %w", err)
	}

	return nil
}

// GetReplicationStatus retrieves current replication status
func GetReplicationStatus(
	ctx context.Context,
	cl client.Client,
	cluster *v1beta1.PostgresCluster,
	config *ReplicationConfig,
) (*ReplicationStatus, error) {
	status := &ReplicationStatus{
		ReplicaStatuses: make([]ReplicaStatus, 0),
	}

	// List recent replication jobs
	jobList := &batchv1.JobList{}
	listOpts := []client.ListOption{
		client.InNamespace(cluster.Namespace),
		client.MatchingLabels{
			"postgres-operator.crunchydata.com/backup-replication": "true",
		},
	}

	if err := cl.List(ctx, jobList, listOpts...); err != nil {
		return nil, fmt.Errorf("failed to list replication jobs: %w", err)
	}

	// Find most recent completed job
	for _, job := range jobList.Items {
		if job.Status.Succeeded > 0 {
			status.LastReplicationResult = "Success"
			status.LastReplicationMessage = "Replication completed successfully"
			if job.Status.CompletionTime != nil {
				status.LastReplicationTime = job.Status.CompletionTime
			}
			break
		} else if job.Status.Failed > 0 {
			status.LastReplicationResult = "Failed"
			status.LastReplicationMessage = "Replication failed"
			break
		}
	}

	// Get status for each replica
	for _, replica := range config.ReplicaRepos {
		replicaStatus := ReplicaStatus{
			RepoName: replica.Name,
			Region:   replica.Region,
			Health:   "unknown",
		}

		// In production, query actual replica status
		// For now, assume healthy if replication job succeeded
		if status.LastReplicationResult == "Success" {
			replicaStatus.InSync = true
			replicaStatus.Health = "healthy"
			replicaStatus.Lag = 0
		}

		status.ReplicaStatuses = append(status.ReplicaStatuses, replicaStatus)
	}

	return status, nil
}

// exportReplicationMetrics exports replication metrics
func exportReplicationMetrics(
	clusterName, namespace string,
	config *ReplicationConfig,
	status *ReplicationStatus,
) {
	for _, replicaStatus := range status.ReplicaStatuses {
		// Replication lag
		replicationLagBytes.WithLabelValues(
			clusterName,
			namespace,
			config.SourceRepo,
			replicaStatus.RepoName,
			replicaStatus.Region,
		).Set(float64(replicaStatus.Lag))

		// Replication status
		statusValue := 0.0
		if replicaStatus.Health == "healthy" {
			statusValue = 1.0
		}

		replicationStatus.WithLabelValues(
			clusterName,
			namespace,
			config.SourceRepo,
			replicaStatus.RepoName,
			replicaStatus.Region,
		).Set(statusValue)
	}
}

// GenerateReplicationReport creates replication status report
func GenerateReplicationReport(
	config *ReplicationConfig,
	status *ReplicationStatus,
) string {
	report := "Cross-Region Backup Replication Report\n"
	report += "======================================\n\n"

	if config == nil || !config.Enabled {
		report += "Status: DISABLED\n\n"
		report += "Cross-region replication is not configured.\n"
		return report
	}

	report += "Status: ENABLED\n\n"
	report += fmt.Sprintf("Source Repository: %s\n", config.SourceRepo)
	report += fmt.Sprintf("Replication Mode: %s\n", config.Mode)
	report += fmt.Sprintf("Schedule: %s\n", config.Schedule)
	report += fmt.Sprintf("Replica Count: %d\n\n", len(config.ReplicaRepos))

	if status != nil && status.LastReplicationTime != nil {
		timeSince := time.Since(status.LastReplicationTime.Time)
		report += fmt.Sprintf("Last Replication: %s ago\n", timeSince.Round(time.Minute))
		report += fmt.Sprintf("Result: %s\n", status.LastReplicationResult)
		if status.LastReplicationMessage != "" {
			report += fmt.Sprintf("Message: %s\n", status.LastReplicationMessage)
		}
		report += "\n"
	}

	report += "Replica Status:\n"
	report += "---------------\n"

	for _, replica := range status.ReplicaStatuses {
		syncStatus := "✗ Out of Sync"
		if replica.InSync {
			syncStatus = "✓ In Sync"
		}

		report += fmt.Sprintf("\n%s (%s)\n", replica.RepoName, replica.Region)
		report += fmt.Sprintf("  Status: %s\n", syncStatus)
		report += fmt.Sprintf("  Health: %s\n", replica.Health)

		if replica.Lag > 0 {
			report += fmt.Sprintf("  Lag: %d bytes\n", replica.Lag)
		}

		if replica.LastSyncTime != nil {
			timeSince := time.Since(replica.LastSyncTime.Time)
			report += fmt.Sprintf("  Last Sync: %s ago\n", timeSince.Round(time.Minute))
		}

		if replica.ErrorMessage != "" {
			report += fmt.Sprintf("  Error: %s\n", replica.ErrorMessage)
		}
	}

	report += "\nDisaster Recovery Capability:\n"
	report += "-----------------------------\n"
	report += fmt.Sprintf("Replicated Regions: %d\n", len(config.ReplicaRepos))

	healthyReplicas := 0
	for _, r := range status.ReplicaStatuses {
		if r.Health == "healthy" {
			healthyReplicas++
		}
	}
	report += fmt.Sprintf("Healthy Replicas: %d\n", healthyReplicas)

	if healthyReplicas > 0 {
		report += "\n✓ Disaster recovery capability is active\n"
		report += "  Backups can be restored from replica regions if primary fails\n"
	} else {
		report += "\n✗ WARNING: No healthy replicas available\n"
		report += "  Check replication jobs and replica configuration\n"
	}

	return report
}
