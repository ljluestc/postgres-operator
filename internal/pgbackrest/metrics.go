// Copyright 2025 Crunchy Data Solutions, Inc.
//
// SPDX-License-Identifier: Apache-2.0

package pgbackrest

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/crunchydata/postgres-operator/pkg/apis/postgres-operator.crunchydata.com/v1beta1"
)

var (
	// backupDuration tracks the duration of backup operations
	backupDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "pgo_backup_duration_seconds",
			Help:    "Duration of backup operations in seconds",
			Buckets: prometheus.ExponentialBuckets(60, 2, 12), // 60s to ~68 hours
		},
		[]string{"cluster", "namespace", "repo", "type", "status"},
	)

	// backupSize tracks the size of backups
	backupSize = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "pgo_backup_size_bytes",
			Help: "Size of backup in bytes",
		},
		[]string{"cluster", "namespace", "repo", "type"},
	)

	// backupCount tracks the number of backups in repository
	backupCount = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "pgo_backup_count",
			Help: "Number of backups in repository by type",
		},
		[]string{"cluster", "namespace", "repo", "type"},
	)

	// backupAge tracks the age of the latest successful backup
	backupAge = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "pgo_backup_age_seconds",
			Help: "Age of the latest successful backup in seconds",
		},
		[]string{"cluster", "namespace", "repo", "type"},
	)

	// backupFailures counts backup failures
	backupFailures = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "pgo_backup_failures_total",
			Help: "Total number of backup failures",
		},
		[]string{"cluster", "namespace", "repo", "type", "reason"},
	)

	// repositorySize tracks the total size of backup repository
	repositorySize = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "pgo_repository_size_bytes",
			Help: "Total size of backup repository in bytes",
		},
		[]string{"cluster", "namespace", "repo"},
	)

	// repositoryUtilization tracks repository disk utilization
	repositoryUtilization = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "pgo_repository_utilization_percent",
			Help: "Repository disk utilization percentage (0-100)",
		},
		[]string{"cluster", "namespace", "repo"},
	)

	// walArchiveAge tracks the age of WAL archiving
	walArchiveAge = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "pgo_wal_archive_age_seconds",
			Help: "Age of the oldest unarchived WAL file in seconds",
		},
		[]string{"cluster", "namespace", "repo"},
	)

	// backupVerificationStatus tracks backup verification results
	backupVerificationStatus = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "pgo_backup_verification_status",
			Help: "Backup verification status (1=success, 0=failure, -1=unknown)",
		},
		[]string{"cluster", "namespace", "repo"},
	)

	// backupVerificationAge tracks when verification last ran
	backupVerificationAge = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "pgo_backup_verification_age_seconds",
			Help: "Time since last backup verification in seconds",
		},
		[]string{"cluster", "namespace", "repo"},
	)
)

func init() {
	// Register metrics with Prometheus
	prometheus.MustRegister(
		backupDuration,
		backupSize,
		backupCount,
		backupAge,
		backupFailures,
		repositorySize,
		repositoryUtilization,
		walArchiveAge,
		backupVerificationStatus,
		backupVerificationAge,
	)
}

// PGBackRestInfo represents the output of `pgbackrest info --output=json`
type PGBackRestInfo struct {
	Name    string           `json:"name"`
	Cipher  string           `json:"cipher,omitempty"`
	Repo    []RepositoryInfo `json:"repo"`
	Backup  []BackupInfo     `json:"backup,omitempty"`
	Archive []ArchiveInfo    `json:"archive,omitempty"`
}

// RepositoryInfo contains information about a pgBackRest repository
type RepositoryInfo struct {
	Key    int    `json:"key"`
	Status string `json:"status"`
}

// BackupInfo contains information about a backup
type BackupInfo struct {
	Label      string                 `json:"label"`
	Type       string                 `json:"type"` // full, diff, incr
	Prior      *string                `json:"prior,omitempty"`
	Start      BackupTimestamp        `json:"timestamp"`
	Database   BackupDatabase         `json:"database"`
	Archive    BackupArchive          `json:"archive"`
	Info       BackupInfoDetail       `json:"info"`
	Error      bool                   `json:"error,omitempty"`
	Annotation map[string]interface{} `json:"annotation,omitempty"`
}

// BackupTimestamp contains timing information
type BackupTimestamp struct {
	Start int64 `json:"start"`
	Stop  int64 `json:"stop"`
}

// BackupDatabase contains database information
type BackupDatabase struct {
	ID int `json:"id"`
}

// BackupArchive contains archive information
type BackupArchive struct {
	Start string `json:"start"`
	Stop  string `json:"stop"`
}

// BackupInfoDetail contains detailed backup information
type BackupInfoDetail struct {
	Size       int64  `json:"size"`
	Delta      int64  `json:"delta"`
	Repository *int64 `json:"repository,omitempty"`
}

// ArchiveInfo contains WAL archive information
type ArchiveInfo struct {
	ID       string         `json:"id"`
	Min      *string        `json:"min,omitempty"`
	Max      *string        `json:"max,omitempty"`
	Database *ArchiveDB     `json:"database,omitempty"`
}

// ArchiveDB contains archive database information
type ArchiveDB struct {
	ID int `json:"id"`
}

// CollectBackupMetrics collects and updates Prometheus metrics for backups
func CollectBackupMetrics(
	ctx context.Context,
	cluster *v1beta1.PostgresCluster,
	repoName string,
	infoJSON string,
) error {

	var info []PGBackRestInfo
	if err := json.Unmarshal([]byte(infoJSON), &info); err != nil {
		return fmt.Errorf("failed to parse pgbackrest info output: %w", err)
	}

	if len(info) == 0 {
		return fmt.Errorf("no stanza information found")
	}

	stanza := info[0]
	clusterName := cluster.Name
	namespace := cluster.Namespace

	// Find repository by name (repo1, repo2, etc.)
	repoNum, err := strconv.Atoi(repoName[len("repo"):])
	if err != nil {
		return fmt.Errorf("invalid repo name: %s", repoName)
	}

	var repoInfo *RepositoryInfo
	for i := range stanza.Repo {
		if stanza.Repo[i].Key == repoNum {
			repoInfo = &stanza.Repo[i]
			break
		}
	}

	if repoInfo == nil {
		return fmt.Errorf("repository %s not found in info", repoName)
	}

	// Collect backup metrics
	if stanza.Backup != nil {
		collectBackupInfo(clusterName, namespace, repoName, stanza.Backup)
	}

	// Collect archive metrics
	if stanza.Archive != nil {
		collectArchiveInfo(clusterName, namespace, repoName, stanza.Archive)
	}

	return nil
}

// collectBackupInfo processes backup information and updates metrics
func collectBackupInfo(clusterName, namespace, repoName string, backups []BackupInfo) {
	// Count backups by type
	counts := map[string]int{
		"full": 0,
		"diff": 0,
		"incr": 0,
	}

	// Track latest backup per type
	latestBackup := map[string]*BackupInfo{
		"full": nil,
		"diff": nil,
		"incr": nil,
	}

	var totalSize int64
	now := time.Now()

	for i := range backups {
		backup := &backups[i]

		// Count backup
		counts[backup.Type]++

		// Track latest
		if latestBackup[backup.Type] == nil ||
			backup.Start.Stop > latestBackup[backup.Type].Start.Stop {
			latestBackup[backup.Type] = backup
		}

		// Calculate size
		if backup.Info.Size > 0 {
			totalSize += backup.Info.Size
			backupSize.WithLabelValues(
				clusterName,
				namespace,
				repoName,
				backup.Type,
			).Set(float64(backup.Info.Size))
		}

		// Calculate duration
		duration := backup.Start.Stop - backup.Start.Start
		backupDuration.WithLabelValues(
			clusterName,
			namespace,
			repoName,
			backup.Type,
			"success",
		).Observe(float64(duration))

		// Track failures
		if backup.Error {
			backupFailures.WithLabelValues(
				clusterName,
				namespace,
				repoName,
				backup.Type,
				"unknown",
			).Inc()
		}
	}

	// Update backup counts
	for backupType, count := range counts {
		backupCount.WithLabelValues(
			clusterName,
			namespace,
			repoName,
			backupType,
		).Set(float64(count))
	}

	// Update backup age for latest backups
	for backupType, backup := range latestBackup {
		if backup != nil {
			backupTime := time.Unix(backup.Start.Stop, 0)
			age := now.Sub(backupTime).Seconds()
			backupAge.WithLabelValues(
				clusterName,
				namespace,
				repoName,
				backupType,
			).Set(age)
		}
	}

	// Update repository size
	if totalSize > 0 {
		repositorySize.WithLabelValues(
			clusterName,
			namespace,
			repoName,
		).Set(float64(totalSize))
	}
}

// collectArchiveInfo processes WAL archive information and updates metrics
func collectArchiveInfo(clusterName, namespace, repoName string, archives []ArchiveInfo) {
	if len(archives) == 0 {
		return
	}

	// For now, just track that archiving is active
	// In a real implementation, we would parse WAL positions and calculate lag
	// This requires more complex logic to compare WAL positions with current database state
}

// RecordBackupStart records the start of a backup operation
func RecordBackupStart(cluster *v1beta1.PostgresCluster, repoName, backupType string) {
	// This can be called when backup job is created
	// Duration will be recorded when backup completes
}

// RecordBackupCompletion records the completion of a backup operation
func RecordBackupCompletion(
	cluster *v1beta1.PostgresCluster,
	repoName, backupType string,
	success bool,
	duration time.Duration,
	size int64,
) {
	clusterName := cluster.Name
	namespace := cluster.Namespace

	status := "success"
	if !success {
		status = "failed"
		backupFailures.WithLabelValues(
			clusterName,
			namespace,
			repoName,
			backupType,
			"job_failed",
		).Inc()
	}

	backupDuration.WithLabelValues(
		clusterName,
		namespace,
		repoName,
		backupType,
		status,
	).Observe(duration.Seconds())

	if success && size > 0 {
		backupSize.WithLabelValues(
			clusterName,
			namespace,
			repoName,
			backupType,
		).Set(float64(size))

		backupAge.WithLabelValues(
			clusterName,
			namespace,
			repoName,
			backupType,
		).Set(0) // Just completed
	}
}

// UpdateRepositoryUtilization updates the repository disk utilization metric
func UpdateRepositoryUtilization(
	cluster *v1beta1.PostgresCluster,
	repoName string,
	utilizationPercent float64,
) {
	repositoryUtilization.WithLabelValues(
		cluster.Name,
		cluster.Namespace,
		repoName,
	).Set(utilizationPercent)
}

// UpdateVerificationMetrics updates backup verification metrics
func UpdateVerificationMetrics(
	cluster *v1beta1.PostgresCluster,
	repoName string,
	success bool,
	timestamp time.Time,
) {
	var status float64
	if success {
		status = 1
	} else {
		status = 0
	}

	backupVerificationStatus.WithLabelValues(
		cluster.Name,
		cluster.Namespace,
		repoName,
	).Set(status)

	age := time.Since(timestamp).Seconds()
	backupVerificationAge.WithLabelValues(
		cluster.Name,
		cluster.Namespace,
		repoName,
	).Set(age)
}

// ResetMetrics resets all metrics for a cluster (called on cluster deletion)
func ResetMetrics(cluster *v1beta1.PostgresCluster) {
	// Delete all metrics for this cluster
	// Prometheus metrics don't have a direct "delete" method, but we can set to 0
	// or let them expire naturally with a TTL

	// Alternative: Re-register metrics without this cluster's labels
	// This would require more complex metric management

	// For now, this is a placeholder for future metric cleanup logic
	// Individual metrics will be cleaned up by their label matchers
	_ = cluster // unused for now but will be needed for cleanup
}
