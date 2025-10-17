// Copyright 2025 Crunchy Data Solutions, Inc.
//
// SPDX-License-Identifier: Apache-2.0

package validation

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/util/validation/field"

	"github.com/crunchydata/postgres-operator/pkg/apis/postgres-operator.crunchydata.com/v1beta1"
)

// ValidationLevel represents the severity of a validation issue
type ValidationLevel string

const (
	// ValidationLevelError blocks cluster creation/update
	ValidationLevelError ValidationLevel = "error"

	// ValidationLevelWarning allows operation but warns user
	ValidationLevelWarning ValidationLevel = "warning"

	// ValidationLevelInfo provides informational feedback
	ValidationLevelInfo ValidationLevel = "info"
)

// ValidationResult represents a single validation finding
type ValidationResult struct {
	// Level indicates severity
	Level ValidationLevel

	// Field is the JSON path to the problematic field
	Field string

	// Message describes the issue
	Message string

	// Recommendation suggests how to fix
	Recommendation string

	// Rule identifies which validation rule triggered
	Rule string
}

// ValidationReport contains all validation results
type ValidationReport struct {
	// Valid indicates if cluster passed all error-level validations
	Valid bool

	// Results contains all validation findings
	Results []ValidationResult

	// Errors counts error-level issues
	Errors int

	// Warnings counts warning-level issues
	Warnings int

	// Infos counts info-level issues
	Infos int
}

// ClusterValidator performs comprehensive cluster validation
type ClusterValidator struct {
	// Rules to apply
	Rules []ValidationRule

	// Context for validation
	Context context.Context
}

// ValidationRule defines a validation check
type ValidationRule interface {
	// Name returns the rule identifier
	Name() string

	// Validate performs the check and returns results
	Validate(cluster *v1beta1.PostgresCluster) []ValidationResult

	// Level returns the severity for violations
	Level() ValidationLevel
}

// NewClusterValidator creates a validator with default rules
func NewClusterValidator(ctx context.Context) *ClusterValidator {
	return &ClusterValidator{
		Context: ctx,
		Rules: []ValidationRule{
			&ResourceLimitsRule{},
			&BackupConfigurationRule{},
			&HighAvailabilityRule{},
			&PostgreSQLVersionRule{},
			&StorageClassRule{},
			&NetworkPolicyRule{},
			&SecurityRule{},
			&PerformanceRule{},
			&NamingConventionRule{},
			&BestPracticesRule{},
		},
	}
}

// Validate performs comprehensive validation
func (v *ClusterValidator) Validate(cluster *v1beta1.PostgresCluster) *ValidationReport {
	report := &ValidationReport{
		Valid:   true,
		Results: []ValidationResult{},
	}

	for _, rule := range v.Rules {
		results := rule.Validate(cluster)
		for _, result := range results {
			report.Results = append(report.Results, result)

			switch result.Level {
			case ValidationLevelError:
				report.Errors++
				report.Valid = false
			case ValidationLevelWarning:
				report.Warnings++
			case ValidationLevelInfo:
				report.Infos++
			}
		}
	}

	return report
}

// ResourceLimitsRule validates resource requests and limits
type ResourceLimitsRule struct{}

func (r *ResourceLimitsRule) Name() string { return "resource-limits" }
func (r *ResourceLimitsRule) Level() ValidationLevel { return ValidationLevelWarning }

func (r *ResourceLimitsRule) Validate(cluster *v1beta1.PostgresCluster) []ValidationResult {
	results := []ValidationResult{}

	// Check if resource limits are set
	for i, instance := range cluster.Spec.InstanceSets {
		if instance.Resources.Limits == nil || len(instance.Resources.Limits) == 0 {
			results = append(results, ValidationResult{
				Level:   ValidationLevelWarning,
				Field:   fmt.Sprintf("spec.instanceSets[%d].resources.limits", i),
				Message: "No resource limits specified",
				Recommendation: "Set memory and CPU limits to prevent resource contention: " +
					"memory: 4Gi, cpu: 2000m",
				Rule: r.Name(),
			})
		}

		// Check if requests equal limits (QoS guaranteed)
		if instance.Resources.Limits != nil && instance.Resources.Requests != nil {
			memLimit := instance.Resources.Limits[corev1.ResourceMemory]
			memRequest := instance.Resources.Requests[corev1.ResourceMemory]
			if !memLimit.IsZero() && !memRequest.IsZero() && memLimit.Cmp(memRequest) != 0 {
				results = append(results, ValidationResult{
					Level:   ValidationLevelInfo,
					Field:   fmt.Sprintf("spec.instanceSets[%d].resources", i),
					Message: "Resource requests and limits differ (QoS: Burstable)",
					Recommendation: "Consider setting requests equal to limits for guaranteed QoS and predictable performance",
					Rule:            r.Name(),
				})
			}
		}

		// Validate memory:CPU ratio
		if instance.Resources.Requests != nil {
			memRequest := instance.Resources.Requests[corev1.ResourceMemory]
			cpuRequest := instance.Resources.Requests[corev1.ResourceCPU]

			if !memRequest.IsZero() && !cpuRequest.IsZero() {
				memGiB := float64(memRequest.Value()) / (1024 * 1024 * 1024)
				cpuCores := float64(cpuRequest.MilliValue()) / 1000

				ratio := memGiB / cpuCores
				// PostgreSQL typically needs 2-4 GB per core
				if ratio < 1.5 || ratio > 6 {
					results = append(results, ValidationResult{
						Level:   ValidationLevelWarning,
						Field:   fmt.Sprintf("spec.instanceSets[%d].resources", i),
						Message: fmt.Sprintf("Unusual memory:CPU ratio (%.1f GB per core)", ratio),
						Recommendation: "PostgreSQL typically performs best with 2-4 GB of memory per CPU core",
						Rule:            r.Name(),
					})
				}
			}
		}
	}

	return results
}

// BackupConfigurationRule validates backup configuration
type BackupConfigurationRule struct{}

func (r *BackupConfigurationRule) Name() string { return "backup-configuration" }
func (r *BackupConfigurationRule) Level() ValidationLevel { return ValidationLevelError }

func (r *BackupConfigurationRule) Validate(cluster *v1beta1.PostgresCluster) []ValidationResult {
	results := []ValidationResult{}

	if cluster.Spec.Backups.PGBackRest.Repos == nil || len(cluster.Spec.Backups.PGBackRest.Repos) == 0 {
		results = append(results, ValidationResult{
			Level:          ValidationLevelError,
			Field:          "spec.backups.pgbackrest.repos",
			Message:        "No backup repositories configured",
			Recommendation: "Configure at least one pgBackRest repository for backups",
			Rule:           r.Name(),
		})
		return results
	}

	// Check backup schedules
	hasFullBackup := false
	for i, repo := range cluster.Spec.Backups.PGBackRest.Repos {
		if repo.BackupSchedules == nil {
			results = append(results, ValidationResult{
				Level:   ValidationLevelWarning,
				Field:   fmt.Sprintf("spec.backups.pgbackrest.repos[%d].backupSchedules", i),
				Message: "No backup schedules configured",
				Recommendation: "Configure automated backup schedules: full backups weekly, " +
					"incremental backups daily",
				Rule: r.Name(),
			})
			continue
		}

		if repo.BackupSchedules.Full != nil {
			hasFullBackup = true

			// Validate cron expression
			if err := validateCronExpression(*repo.BackupSchedules.Full); err != nil {
				results = append(results, ValidationResult{
					Level:          ValidationLevelError,
					Field:          fmt.Sprintf("spec.backups.pgbackrest.repos[%d].backupSchedules.full", i),
					Message:        fmt.Sprintf("Invalid cron expression: %v", err),
					Recommendation: "Use valid cron format, e.g., '0 2 * * 0' for weekly at 2 AM",
					Rule:           r.Name(),
				})
			}
		}

		// Check for retention policy
		if repo.RetentionPolicy == nil || *repo.RetentionPolicy == "" {
			results = append(results, ValidationResult{
				Level:   ValidationLevelWarning,
				Field:   fmt.Sprintf("spec.backups.pgbackrest.repos[%d].retentionPolicy", i),
				Message: "No retention policy configured",
				Recommendation: "Set retention policy to prevent unlimited backup growth: " +
					"e.g., '--repo1-retention-full=14'",
				Rule: r.Name(),
			})
		}
	}

	if !hasFullBackup {
		results = append(results, ValidationResult{
			Level:   ValidationLevelWarning,
			Field:   "spec.backups.pgbackrest.repos",
			Message: "No full backup schedule configured",
			Recommendation: "Configure at least one full backup schedule for disaster recovery",
			Rule:            r.Name(),
		})
	}

	return results
}

// HighAvailabilityRule validates HA configuration
type HighAvailabilityRule struct{}

func (r *HighAvailabilityRule) Name() string { return "high-availability" }
func (r *HighAvailabilityRule) Level() ValidationLevel { return ValidationLevelWarning }

func (r *HighAvailabilityRule) Validate(cluster *v1beta1.PostgresCluster) []ValidationResult {
	results := []ValidationResult{}

	totalReplicas := int32(0)
	for _, instance := range cluster.Spec.InstanceSets {
		if instance.Replicas != nil {
			totalReplicas += *instance.Replicas
		}
	}

	// Check for HA
	if totalReplicas < 2 {
		results = append(results, ValidationResult{
			Level:   ValidationLevelWarning,
			Field:   "spec.instanceSets[].replicas",
			Message: "Single instance configuration - no high availability",
			Recommendation: "For production use, configure at least 2-3 replicas for high availability",
			Rule:            r.Name(),
		})
	}

	// Check Patroni configuration
	if cluster.Spec.Patroni == nil {
		results = append(results, ValidationResult{
			Level:   ValidationLevelInfo,
			Field:   "spec.patroni",
			Message: "Using default Patroni configuration",
			Recommendation: "Consider customizing Patroni settings for your HA requirements " +
				"(e.g., ttl, loop_wait, retry_timeout)",
			Rule: r.Name(),
		})
	}

	// Check pod disruption budget
	if cluster.Spec.DisruptionBudget == nil || cluster.Spec.DisruptionBudget.MinAvailable == nil {
		results = append(results, ValidationResult{
			Level:   ValidationLevelWarning,
			Field:   "spec.disruptionBudget",
			Message: "No pod disruption budget configured",
			Recommendation: "Configure minAvailable to protect against simultaneous pod evictions during cluster maintenance",
			Rule:            r.Name(),
		})
	}

	// Check affinity rules
	for i, instance := range cluster.Spec.InstanceSets {
		if instance.Affinity == nil {
			results = append(results, ValidationResult{
				Level:   ValidationLevelInfo,
				Field:   fmt.Sprintf("spec.instanceSets[%d].affinity", i),
				Message: "No pod affinity/anti-affinity configured",
				Recommendation: "Consider pod anti-affinity rules to spread instances across nodes/zones for better fault tolerance",
				Rule:            r.Name(),
			})
		}
	}

	return results
}

// PostgreSQLVersionRule validates PostgreSQL version
type PostgreSQLVersionRule struct{}

func (r *PostgreSQLVersionRule) Name() string { return "postgresql-version" }
func (r *PostgreSQLVersionRule) Level() ValidationLevel { return ValidationLevelWarning }

func (r *PostgreSQLVersionRule) Validate(cluster *v1beta1.PostgresCluster) []ValidationResult {
	results := []ValidationResult{}

	if cluster.Spec.PostgresVersion == 0 {
		results = append(results, ValidationResult{
			Level:          ValidationLevelError,
			Field:          "spec.postgresVersion",
			Message:        "PostgreSQL version not specified",
			Recommendation: "Specify a PostgreSQL major version (e.g., 16, 17, 18)",
			Rule:           r.Name(),
		})
		return results
	}

	// Warn about EOL versions
	eolVersions := map[int]string{
		11: "EOL November 9, 2023",
		12: "EOL November 14, 2024",
	}

	if eolDate, isEOL := eolVersions[cluster.Spec.PostgresVersion]; isEOL {
		results = append(results, ValidationResult{
			Level:   ValidationLevelWarning,
			Field:   "spec.postgresVersion",
			Message: fmt.Sprintf("PostgreSQL %d is end-of-life (%s)", cluster.Spec.PostgresVersion, eolDate),
			Recommendation: fmt.Sprintf("Upgrade to a supported version (16, 17, or 18) for security updates and bug fixes"),
			Rule:            r.Name(),
		})
	}

	// Check image
	if cluster.Spec.Image == "" {
		results = append(results, ValidationResult{
			Level:   ValidationLevelInfo,
			Field:   "spec.image",
			Message: "Using default PostgreSQL image",
			Recommendation: "Explicitly specify image to ensure consistent deployments across environments",
			Rule:            r.Name(),
		})
	}

	return results
}

// StorageClassRule validates storage configuration
type StorageClassRule struct{}

func (r *StorageClassRule) Name() string { return "storage-class" }
func (r *StorageClassRule) Level() ValidationLevel { return ValidationLevelWarning }

func (r *StorageClassRule) Validate(cluster *v1beta1.PostgresCluster) []ValidationResult {
	results := []ValidationResult{}

	for i, instance := range cluster.Spec.InstanceSets {
		for j, volume := range instance.DataVolumeClaimSpec.Resources.Requests {
			// Check storage size
			if j == corev1.ResourceStorage {
				minStorage := resource.MustParse("10Gi")
				if volume.Cmp(minStorage) < 0 {
					results = append(results, ValidationResult{
						Level:   ValidationLevelWarning,
						Field:   fmt.Sprintf("spec.instanceSets[%d].dataVolumeClaimSpec.resources.requests.storage", i),
						Message: fmt.Sprintf("Storage size (%s) is very small", volume.String()),
						Recommendation: "For production use, allocate at least 20-50 GB for data volume",
						Rule:            r.Name(),
					})
				}
			}
		}

		// Check storage class
		if instance.DataVolumeClaimSpec.StorageClassName == nil {
			results = append(results, ValidationResult{
				Level:   ValidationLevelWarning,
				Field:   fmt.Sprintf("spec.instanceSets[%d].dataVolumeClaimSpec.storageClassName", i),
				Message: "No storage class specified - using cluster default",
				Recommendation: "Explicitly specify storageClassName to ensure predictable storage performance",
				Rule:            r.Name(),
			})
		}

		// Check access mode
		hasRWO := false
		for _, mode := range instance.DataVolumeClaimSpec.AccessModes {
			if mode == corev1.ReadWriteOnce {
				hasRWO = true
			}
		}
		if !hasRWO {
			results = append(results, ValidationResult{
				Level:   ValidationLevelError,
				Field:   fmt.Sprintf("spec.instanceSets[%d].dataVolumeClaimSpec.accessModes", i),
				Message: "ReadWriteOnce access mode required for PostgreSQL data volumes",
				Recommendation: "Add ReadWriteOnce to accessModes",
				Rule:            r.Name(),
			})
		}
	}

	// Check WAL volume
	for i, instance := range cluster.Spec.InstanceSets {
		if instance.WALVolumeClaimSpec != nil {
			results = append(results, ValidationResult{
				Level:   ValidationLevelInfo,
				Field:   fmt.Sprintf("spec.instanceSets[%d].walVolumeClaimSpec", i),
				Message: "Separate WAL volume configured",
				Recommendation: "Excellent! Separate WAL volumes improve write performance",
				Rule:            r.Name(),
			})
		} else {
			results = append(results, ValidationResult{
				Level:   ValidationLevelInfo,
				Field:   fmt.Sprintf("spec.instanceSets[%d].walVolumeClaimSpec", i),
				Message: "WAL and data on same volume",
				Recommendation: "For better performance, consider using a separate volume for WAL (Write-Ahead Logs)",
				Rule:            r.Name(),
			})
		}
	}

	return results
}

// NetworkPolicyRule validates network configuration
type NetworkPolicyRule struct{}

func (r *NetworkPolicyRule) Name() string { return "network-policy" }
func (r *NetworkPolicyRule) Level() ValidationLevel { return ValidationLevelInfo }

func (r *NetworkPolicyRule) Validate(cluster *v1beta1.PostgresCluster) []ValidationResult {
	results := []ValidationResult{}

	// Check service exposure
	if cluster.Spec.Service != nil && cluster.Spec.Service.Type != nil {
		if *cluster.Spec.Service.Type == corev1.ServiceTypeLoadBalancer {
			results = append(results, ValidationResult{
				Level:   ValidationLevelWarning,
				Field:   "spec.service.type",
				Message: "Service exposed via LoadBalancer",
				Recommendation: "Ensure proper firewall rules and authentication are configured for public exposure",
				Rule:            r.Name(),
			})
		}
	}

	return results
}

// SecurityRule validates security configuration
type SecurityRule struct{}

func (r *SecurityRule) Name() string { return "security" }
func (r *SecurityRule) Level() ValidationLevel { return ValidationLevelWarning }

func (r *SecurityRule) Validate(cluster *v1beta1.PostgresCluster) []ValidationResult {
	results := []ValidationResult{}

	// Check custom TLS
	if cluster.Spec.CustomTLSSecret == nil {
		results = append(results, ValidationResult{
			Level:   ValidationLevelInfo,
			Field:   "spec.customTLSSecret",
			Message: "Using operator-generated TLS certificates",
			Recommendation: "For production, consider providing custom TLS certificates from your PKI",
			Rule:            r.Name(),
		})
	}

	// Check pgBouncer configuration
	if cluster.Spec.Proxy != nil && cluster.Spec.Proxy.PGBouncer != nil {
		if cluster.Spec.Proxy.PGBouncer.CustomTLSSecret == nil {
			results = append(results, ValidationResult{
				Level:   ValidationLevelInfo,
				Field:   "spec.proxy.pgbouncer.customTLSSecret",
				Message: "pgBouncer using operator-generated TLS",
				Recommendation: "Consider providing custom TLS for pgBouncer in production",
				Rule:            r.Name(),
			})
		}
	}

	return results
}

// PerformanceRule validates performance-related configuration
type PerformanceRule struct{}

func (r *PerformanceRule) Name() string { return "performance" }
func (r *PerformanceRule) Level() ValidationLevel { return ValidationLevelInfo }

func (r *PerformanceRule) Validate(cluster *v1beta1.PostgresCluster) []ValidationResult {
	results := []ValidationResult{}

	// Check PostgreSQL parameters
	if cluster.Spec.PostgresConfiguration != nil {
		params := cluster.Spec.PostgresConfiguration.Parameters

		// Check shared_buffers
		if sharedBuffers, ok := params["shared_buffers"]; ok {
			if !strings.Contains(sharedBuffers, "MB") && !strings.Contains(sharedBuffers, "GB") {
				results = append(results, ValidationResult{
					Level:          ValidationLevelWarning,
					Field:          "spec.postgresConfiguration.parameters.shared_buffers",
					Message:        "shared_buffers should include unit (MB or GB)",
					Recommendation: "Use format like '2GB' or '2048MB'",
					Rule:           r.Name(),
				})
			}
		} else {
			results = append(results, ValidationResult{
				Level:   ValidationLevelInfo,
				Field:   "spec.postgresConfiguration.parameters.shared_buffers",
				Message: "shared_buffers not explicitly set",
				Recommendation: "Consider setting shared_buffers to ~25% of instance memory for optimal performance",
				Rule:            r.Name(),
			})
		}

		// Check max_connections
		if _, ok := params["max_connections"]; !ok {
			results = append(results, ValidationResult{
				Level:   ValidationLevelInfo,
				Field:   "spec.postgresConfiguration.parameters.max_connections",
				Message: "max_connections not explicitly set",
				Recommendation: "Set max_connections based on your application's connection requirements",
				Rule:            r.Name(),
			})
		}
	}

	return results
}

// NamingConventionRule validates naming conventions
type NamingConventionRule struct{}

func (r *NamingConventionRule) Name() string { return "naming-convention" }
func (r *NamingConventionRule) Level() ValidationLevel { return ValidationLevelInfo }

func (r *NamingConventionRule) Validate(cluster *v1beta1.PostgresCluster) []ValidationResult {
	results := []ValidationResult{}

	// Validate cluster name
	namePattern := regexp.MustCompile(`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`)
	if !namePattern.MatchString(cluster.Name) {
		results = append(results, ValidationResult{
			Level:          ValidationLevelError,
			Field:          "metadata.name",
			Message:        "Invalid cluster name format",
			Recommendation: "Use lowercase alphanumeric characters and hyphens only",
			Rule:           r.Name(),
		})
	}

	// Check name length
	if len(cluster.Name) > 50 {
		results = append(results, ValidationResult{
			Level:   ValidationLevelWarning,
			Field:   "metadata.name",
			Message: "Cluster name is very long",
			Recommendation: "Shorter names are easier to work with in kubectl and logs",
			Rule:            r.Name(),
		})
	}

	return results
}

// BestPracticesRule validates general best practices
type BestPracticesRule struct{}

func (r *BestPracticesRule) Name() string { return "best-practices" }
func (r *BestPracticesRule) Level() ValidationLevel { return ValidationLevelInfo }

func (r *BestPracticesRule) Validate(cluster *v1beta1.PostgresCluster) []ValidationResult {
	results := []ValidationResult{}

	// Check for metadata labels
	if cluster.Labels == nil || len(cluster.Labels) == 0 {
		results = append(results, ValidationResult{
			Level:   ValidationLevelInfo,
			Field:   "metadata.labels",
			Message: "No custom labels defined",
			Recommendation: "Add labels for organization, cost tracking, and management (e.g., env, team, app)",
			Rule:            r.Name(),
		})
	}

	// Check for monitoring
	if cluster.Spec.Monitoring == nil {
		results = append(results, ValidationResult{
			Level:   ValidationLevelWarning,
			Field:   "spec.monitoring",
			Message: "Monitoring not configured",
			Recommendation: "Enable pgMonitor for comprehensive PostgreSQL monitoring",
			Rule:            r.Name(),
		})
	}

	// Check for connection pooling in production
	if cluster.Spec.Proxy == nil || cluster.Spec.Proxy.PGBouncer == nil {
		results = append(results, ValidationResult{
			Level:   ValidationLevelInfo,
			Field:   "spec.proxy.pgbouncer",
			Message: "pgBouncer connection pooling not enabled",
			Recommendation: "For applications with many connections, enable pgBouncer to reduce database overhead",
			Rule:            r.Name(),
		})
	}

	return results
}

// validateCronExpression validates a cron expression format
func validateCronExpression(expr string) error {
	parts := strings.Fields(expr)
	if len(parts) != 5 {
		return fmt.Errorf("cron expression must have 5 fields (minute hour day month weekday)")
	}
	return nil
}

// FormatReport converts validation report to human-readable string
func FormatReport(report *ValidationReport) string {
	var sb strings.Builder

	sb.WriteString(fmt.Sprintf("Validation Report: %d errors, %d warnings, %d infos\n",
		report.Errors, report.Warnings, report.Infos))
	sb.WriteString(fmt.Sprintf("Overall Status: %s\n\n",
		map[bool]string{true: "VALID", false: "INVALID"}[report.Valid]))

	for _, result := range report.Results {
		levelStr := strings.ToUpper(string(result.Level))
		sb.WriteString(fmt.Sprintf("[%s] %s\n", levelStr, result.Field))
		sb.WriteString(fmt.Sprintf("  Message: %s\n", result.Message))
		sb.WriteString(fmt.Sprintf("  Recommendation: %s\n", result.Recommendation))
		sb.WriteString(fmt.Sprintf("  Rule: %s\n\n", result.Rule))
	}

	return sb.String()
}
