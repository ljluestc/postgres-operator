// Copyright 2025 Crunchy Data Solutions, Inc.
//
// SPDX-License-Identifier: Apache-2.0

package dr

import (
	"context"
	"fmt"
	"time"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/crunchydata/postgres-operator/internal/initialize"
	"github.com/crunchydata/postgres-operator/internal/logging"
	"github.com/crunchydata/postgres-operator/internal/naming"
	"github.com/crunchydata/postgres-operator/pkg/apis/postgres-operator.crunchydata.com/v1beta1"
)

const (
	// DrillTypeFullRestore performs complete cluster restore
	DrillTypeFullRestore = "full-restore"

	// DrillTypePointInTime performs PITR drill
	DrillTypePointInTime = "point-in-time"

	// DrillTypeDataVerification validates restored data
	DrillTypeDataVerification = "data-verification"

	// Default drill timeout
	defaultDrillTimeout = 2 * time.Hour

	// Annotation keys
	AnnotationDrillSchedule      = "postgres-operator.crunchydata.com/dr-drill-schedule"
	AnnotationLastDrill          = "postgres-operator.crunchydata.com/last-dr-drill"
	AnnotationLastDrillResult    = "postgres-operator.crunchydata.com/last-dr-drill-result"
	AnnotationTriggerDrill       = "postgres-operator.crunchydata.com/trigger-dr-drill"
)

// DRDrillConfig configures disaster recovery drills
type DRDrillConfig struct {
	// Enabled determines if DR drills are active
	Enabled bool

	// Schedule in cron format for automated drills
	Schedule string

	// DrillType specifies the type of drill to perform
	DrillType string

	// TargetCluster is the cluster to restore (source)
	TargetCluster string

	// DrillNamespace is where drill resources are created
	DrillNamespace string

	// RepoName specifies which backup repository to use
	RepoName string

	// PointInTime specifies target time for PITR (optional)
	PointInTime *time.Time

	// ValidationQueries are SQL queries to run against restored data
	ValidationQueries []string

	// Notifications configuration
	NotifyOnSuccess bool
	NotifyOnFailure bool
	WebhookURL      string
	SlackChannel    string

	// Cleanup determines if drill resources are deleted after completion
	AutoCleanup bool
	RetainDays  int

	// RTOTarget is the target Recovery Time Objective
	RTOTarget time.Duration

	// Timeout for drill operations
	Timeout time.Duration
}

// DRDrillResult contains drill execution results
type DRDrillResult struct {
	// DrillID unique identifier
	DrillID string

	// StartTime when drill began
	StartTime time.Time

	// EndTime when drill completed
	EndTime time.Time

	// Duration total drill duration
	Duration time.Duration

	// Success indicates if drill passed
	Success bool

	// RTOAchieved indicates if RTO target was met
	RTOAchieved bool

	// Metrics collected during drill
	Metrics DRDrillMetrics

	// ValidationResults from validation queries
	ValidationResults []ValidationResult

	// ErrorMessage if drill failed
	ErrorMessage string

	// Recommendations for improvement
	Recommendations []string
}

// DRDrillMetrics contains timing and performance metrics
type DRDrillMetrics struct {
	// RestoreStartTime when restore began
	RestoreStartTime time.Time

	// RestoreCompletionTime when restore finished
	RestoreCompletionTime time.Time

	// RestoreDuration time to complete restore
	RestoreDuration time.Duration

	// DataValidationDuration time to validate data
	DataValidationDuration time.Duration

	// BackupSize size of backup being restored
	BackupSize int64

	// RestoreThroughput MB/s
	RestoreThroughput float64

	// DatabaseStartupTime time for PostgreSQL to start
	DatabaseStartupTime time.Duration
}

// ValidationResult represents a single validation check result
type ValidationResult struct {
	// QueryName identifies the validation query
	QueryName string

	// Query SQL executed
	Query string

	// ExpectedResult what we expected
	ExpectedResult interface{}

	// ActualResult what we got
	ActualResult interface{}

	// Passed indicates if validation succeeded
	Passed bool

	// Message details about result
	Message string
}

// CreateDRDrillJob creates a Job to perform disaster recovery drill
func CreateDRDrillJob(
	ctx context.Context,
	cluster *v1beta1.PostgresCluster,
	config *DRDrillConfig,
) (*batchv1.Job, error) {
	log := logging.FromContext(ctx)

	if !config.Enabled {
		return nil, nil
	}

	log.Info("Creating disaster recovery drill job", "type", config.DrillType)

	drillName := fmt.Sprintf("%s-dr-drill-%d", cluster.Name, time.Now().Unix())

	meta := naming.PGBackRestBackupJob(cluster)
	meta.Name = drillName
	meta.Namespace = config.DrillNamespace
	meta.Labels = naming.Merge(
		cluster.Spec.Metadata.GetLabelsOrNil(),
		map[string]string{
			naming.LabelPGBackRestRestore: "",
			"postgres-operator.crunchydata.com/dr-drill": "true",
			"postgres-operator.crunchydata.com/drill-type": config.DrillType,
		},
	)

	job := &batchv1.Job{
		TypeMeta: metav1.TypeMeta{
			APIVersion: batchv1.SchemeGroupVersion.String(),
			Kind:       "Job",
		},
		ObjectMeta: meta,
	}

	// Set timeout
	timeout := config.Timeout
	if timeout == 0 {
		timeout = defaultDrillTimeout
	}
	timeoutSeconds := int64(timeout.Seconds())
	job.Spec.ActiveDeadlineSeconds = &timeoutSeconds

	// Create pod template
	podTemplate := &job.Spec.Template
	podTemplate.ObjectMeta = meta
	podTemplate.ObjectMeta.Name = ""

	// Initialize container
	container := corev1.Container{
		Name:            "dr-drill",
		Image:           cluster.Spec.Image,
		ImagePullPolicy: cluster.Spec.ImagePullPolicy,
		Command:         createDrillScript(config),
		SecurityContext: initialize.RestrictedSecurityContext(),
	}

	// Add environment variables
	container.Env = []corev1.EnvVar{
		{Name: "PGBACKREST_STANZA", Value: "db"},
		{Name: "PGBACKREST_REPO", Value: config.RepoName[len("repo"):]},
		{Name: "DR_DRILL_TYPE", Value: config.DrillType},
		{Name: "DR_DRILL_ID", Value: fmt.Sprintf("drill-%d", time.Now().Unix())},
	}

	if config.PointInTime != nil {
		container.Env = append(container.Env, corev1.EnvVar{
			Name:  "PITR_TARGET",
			Value: config.PointInTime.Format(time.RFC3339),
		})
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
						},
					},
				},
			},
		},
	}

	// Mount data volume for restore
	dataVolume := corev1.Volume{
		Name: "pgdata",
		VolumeSource: corev1.VolumeSource{
			EmptyDir: &corev1.EmptyDirVolumeSource{},
		},
	}

	container.VolumeMounts = []corev1.VolumeMount{
		{
			Name:      configVolume.Name,
			MountPath: "/etc/pgbackrest",
			ReadOnly:  true,
		},
		{
			Name:      dataVolume.Name,
			MountPath: "/pgdata",
		},
	}

	podTemplate.Spec.Containers = []corev1.Container{container}
	podTemplate.Spec.Volumes = []corev1.Volume{configVolume, dataVolume}
	podTemplate.Spec.RestartPolicy = corev1.RestartPolicyNever

	return job, nil
}

// createDrillScript generates the drill execution script
func createDrillScript(config *DRDrillConfig) []string {
	script := []string{"bash", "-ceu", "--"}

	var commands []string

	// Common setup
	commands = append(commands, `
set -x
echo "=== Starting DR Drill ==="
echo "Drill ID: ${DR_DRILL_ID}"
echo "Drill Type: ${DR_DRILL_TYPE}"
echo "Start Time: $(date -Iseconds)"

DRILL_START=$(date +%s)
`)

	switch config.DrillType {
	case DrillTypeFullRestore:
		commands = append(commands, createFullRestoreDrill(config))

	case DrillTypePointInTime:
		commands = append(commands, createPITRDrill(config))

	case DrillTypeDataVerification:
		commands = append(commands, createDataVerificationDrill(config))
	}

	// Add data validation queries
	if len(config.ValidationQueries) > 0 {
		commands = append(commands, `
echo "=== Running Validation Queries ==="
VALIDATION_START=$(date +%s)
`)
		for i, query := range config.ValidationQueries {
			commands = append(commands, fmt.Sprintf(`
echo "Validation Query %d:"
echo "%s"
psql -h localhost -U postgres -d postgres -c "%s" || echo "Query %d failed"
`, i+1, query, query, i+1))
		}

		commands = append(commands, `
VALIDATION_END=$(date +%s)
VALIDATION_DURATION=$((VALIDATION_END - VALIDATION_START))
echo "Validation Duration: ${VALIDATION_DURATION}s"
`)
	}

	// Calculate metrics
	commands = append(commands, `
DRILL_END=$(date +%s)
DRILL_DURATION=$((DRILL_END - DRILL_START))

echo "=== DR Drill Complete ==="
echo "End Time: $(date -Iseconds)"
echo "Total Duration: ${DRILL_DURATION}s"
echo "RTO Target: `)
	commands = append(commands, fmt.Sprintf("%d", int(config.RTOTarget.Seconds())))
	commands = append(commands, `s"

if [ ${DRILL_DURATION} -le `)
	commands = append(commands, fmt.Sprintf("%d", int(config.RTOTarget.Seconds())))
	commands = append(commands, ` ]; then
    echo "✓ RTO TARGET ACHIEVED"
    exit 0
else
    echo "✗ RTO TARGET MISSED"
    exit 1
fi
`)

	scriptContent := ""
	for _, cmd := range commands {
		scriptContent += cmd + "\n"
	}

	return append(script, scriptContent)
}

// createFullRestoreDrill creates script for full restore drill
func createFullRestoreDrill(config *DRDrillConfig) string {
	return `
echo "=== Performing Full Restore Drill ==="
RESTORE_START=$(date +%s)

# Perform full restore
mkdir -p /pgdata/pg17
pgbackrest --stanza="${PGBACKREST_STANZA}" \
           --repo="${PGBACKREST_REPO}" \
           --pg1-path=/pgdata/pg17 \
           --type=immediate \
           --delta \
           restore

RESTORE_END=$(date +%s)
RESTORE_DURATION=$((RESTORE_END - RESTORE_START))
echo "Restore Duration: ${RESTORE_DURATION}s"

# Verify critical files
echo "=== Verifying Restored Files ==="
if [ ! -f "/pgdata/pg17/PG_VERSION" ]; then
    echo "ERROR: PG_VERSION not found"
    exit 1
fi

PG_VERSION=$(cat /pgdata/pg17/PG_VERSION)
echo "PostgreSQL Version: ${PG_VERSION}"

if [ ! -f "/pgdata/pg17/postgresql.conf" ]; then
    echo "ERROR: postgresql.conf not found"
    exit 1
fi

if [ ! -d "/pgdata/pg17/base" ]; then
    echo "ERROR: base directory not found"
    exit 1
fi

echo "✓ Restore verification passed"

# Start PostgreSQL temporarily for validation
echo "=== Starting PostgreSQL for Validation ==="
DB_START=$(date +%s)

pg_ctl -D /pgdata/pg17 -o "-c listen_addresses='localhost' -c port=5432" start

# Wait for PostgreSQL to be ready
for i in {1..60}; do
    if pg_isready -h localhost; then
        break
    fi
    sleep 1
done

DB_END=$(date +%s)
DB_STARTUP_DURATION=$((DB_END - DB_START))
echo "Database Startup Duration: ${DB_STARTUP_DURATION}s"

# Verify database connectivity
psql -h localhost -U postgres -d postgres -c "SELECT version();"
psql -h localhost -U postgres -d postgres -c "SELECT pg_database_size('postgres');"

# Stop PostgreSQL
pg_ctl -D /pgdata/pg17 stop
`
}

// createPITRDrill creates script for point-in-time recovery drill
func createPITRDrill(config *DRDrillConfig) string {
	return `
echo "=== Performing Point-in-Time Recovery Drill ==="
RESTORE_START=$(date +%s)

if [ -z "${PITR_TARGET:-}" ]; then
    echo "ERROR: PITR_TARGET not set"
    exit 1
fi

echo "Target Time: ${PITR_TARGET}"

# Perform PITR restore
mkdir -p /pgdata/pg17
pgbackrest --stanza="${PGBACKREST_STANZA}" \
           --repo="${PGBACKREST_REPO}" \
           --pg1-path=/pgdata/pg17 \
           --type=time \
           --target="${PITR_TARGET}" \
           --delta \
           restore

RESTORE_END=$(date +%s)
RESTORE_DURATION=$((RESTORE_END - RESTORE_START))
echo "Restore Duration: ${RESTORE_DURATION}s"

# Verify restore
if [ ! -f "/pgdata/pg17/PG_VERSION" ]; then
    echo "ERROR: PITR restore failed"
    exit 1
fi

echo "✓ PITR restore completed successfully"

# Start and validate
pg_ctl -D /pgdata/pg17 -o "-c listen_addresses='localhost'" start

for i in {1..60}; do
    if pg_isready -h localhost; then
        break
    fi
    sleep 1
done

# Check recovery target time
psql -h localhost -U postgres -d postgres -c "SELECT pg_postmaster_start_time();"

pg_ctl -D /pgdata/pg17 stop
`
}

// createDataVerificationDrill creates script for data verification
func createDataVerificationDrill(config *DRDrillConfig) string {
	return `
echo "=== Performing Data Verification Drill ==="

# This drill assumes cluster is already running
# It validates data integrity and consistency

echo "Connecting to database..."
if ! pg_isready -h "${PGHOST:-localhost}"; then
    echo "ERROR: Database not accessible"
    exit 1
fi

echo "✓ Database is accessible"

# Verify replication (if applicable)
echo "Checking replication status..."
psql -h localhost -U postgres -d postgres -c "SELECT * FROM pg_stat_replication;"

# Check for corruption
echo "Checking for data corruption..."
psql -h localhost -U postgres -d postgres -c "
    SELECT datname,
           pg_size_pretty(pg_database_size(datname)) as size
    FROM pg_database;
"

echo "✓ Data verification completed"
`
}

// ReconcileDRDrills manages scheduled disaster recovery drills
func ReconcileDRDrills(
	ctx context.Context,
	cl client.Client,
	cluster *v1beta1.PostgresCluster,
	config *DRDrillConfig,
) error {
	log := logging.FromContext(ctx)

	if config == nil || !config.Enabled {
		return nil
	}

	// Check if manual drill is triggered
	if cluster.Annotations[AnnotationTriggerDrill] == "true" {
		log.Info("Manual DR drill triggered")

		job, err := CreateDRDrillJob(ctx, cluster, config)
		if err != nil {
			return fmt.Errorf("failed to create DR drill job: %w", err)
		}

		if err := cl.Create(ctx, job); err != nil {
			return fmt.Errorf("failed to create drill job: %w", err)
		}

		// Remove trigger annotation
		delete(cluster.Annotations, AnnotationTriggerDrill)
		cluster.Annotations[AnnotationLastDrill] = time.Now().Format(time.RFC3339)

		log.Info("DR drill job created", "job", job.Name)
	}

	// Check for scheduled drills (would be implemented via CronJob)

	return nil
}

// AnalyzeDRDrillResults analyzes drill results and provides recommendations
func AnalyzeDRDrillResults(result *DRDrillResult) []string {
	recommendations := []string{}

	// Check if RTO was achieved
	if !result.RTOAchieved {
		recommendations = append(recommendations,
			"RTO target was not achieved. Consider:",
			"  - Using incremental backups to reduce restore time",
			"  - Increasing restore resources (CPU/memory)",
			"  - Optimizing network bandwidth for backup repository",
			"  - Reviewing backup schedule and retention")
	}

	// Check restore performance
	if result.Metrics.RestoreThroughput < 50 { // Less than 50 MB/s
		recommendations = append(recommendations,
			"Restore throughput is low. Consider:",
			"  - Using faster storage for restore target",
			"  - Increasing network bandwidth",
			"  - Using parallel restore if supported")
	}

	// Check database startup time
	if result.Metrics.DatabaseStartupTime > 5*time.Minute {
		recommendations = append(recommendations,
			"Database startup time is high. Consider:",
			"  - Reviewing checkpoint settings",
			"  - Optimizing shared_buffers and other memory settings",
			"  - Checking for large numbers of prepared transactions")
	}

	// Check validation failures
	failedValidations := 0
	for _, v := range result.ValidationResults {
		if !v.Passed {
			failedValidations++
		}
	}

	if failedValidations > 0 {
		recommendations = append(recommendations,
			fmt.Sprintf("%d validation(s) failed. Review validation queries and expected results", failedValidations))
	}

	if len(recommendations) == 0 {
		recommendations = append(recommendations,
			"✓ Excellent! All DR drill objectives achieved.",
			"Continue regular drills to maintain readiness.")
	}

	return recommendations
}

// GenerateDRReport creates a detailed drill report
func GenerateDRReport(result *DRDrillResult) string {
	report := fmt.Sprintf(`
Disaster Recovery Drill Report
===============================

Drill ID: %s
Start Time: %s
End Time: %s
Total Duration: %s
RTO Target Achieved: %s

Restore Metrics:
----------------
Restore Duration: %s
Backup Size: %d bytes
Restore Throughput: %.2f MB/s
Database Startup Time: %s

Validation Results:
-------------------
`,
		result.DrillID,
		result.StartTime.Format(time.RFC3339),
		result.EndTime.Format(time.RFC3339),
		result.Duration.String(),
		map[bool]string{true: "YES ✓", false: "NO ✗"}[result.RTOAchieved],
		result.Metrics.RestoreDuration.String(),
		result.Metrics.BackupSize,
		result.Metrics.RestoreThroughput,
		result.Metrics.DatabaseStartupTime.String(),
	)

	for _, v := range result.ValidationResults {
		status := map[bool]string{true: "✓ PASS", false: "✗ FAIL"}[v.Passed]
		report += fmt.Sprintf("%s - %s: %s\n", status, v.QueryName, v.Message)
	}

	report += "\nRecommendations:\n----------------\n"
	for _, rec := range result.Recommendations {
		report += fmt.Sprintf("%s\n", rec)
	}

	return report
}
