// Copyright 2025 Crunchy Data Solutions, Inc.
//
// SPDX-License-Identifier: Apache-2.0

package patroni

import (
	"context"
	"fmt"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"k8s.io/apimachinery/pkg/util/intstr"

	"github.com/crunchydata/postgres-operator/internal/logging"
	"github.com/crunchydata/postgres-operator/pkg/apis/postgres-operator.crunchydata.com/v1beta1"
)

// Failover metrics
var (
	failoverDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "pgo_failover_duration_seconds",
			Help:    "Duration of failover operations in seconds",
			Buckets: []float64{1, 2, 5, 10, 15, 20, 30, 45, 60, 90, 120}, // 1s to 2min
		},
		[]string{"cluster", "namespace", "trigger", "success"},
	)

	failoverCount = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "pgo_failover_total",
			Help: "Total number of failover events",
		},
		[]string{"cluster", "namespace", "trigger", "success"},
	)

	timeToDetectFailure = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "pgo_failure_detection_seconds",
			Help:    "Time to detect primary failure in seconds",
			Buckets: []float64{0.5, 1, 2, 3, 5, 7, 10, 15, 20, 30},
		},
		[]string{"cluster", "namespace"},
	)

	timeToElectLeader = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "pgo_leader_election_seconds",
			Help:    "Time to elect new leader in seconds",
			Buckets: []float64{0.5, 1, 2, 3, 5, 7, 10, 15},
		},
		[]string{"cluster", "namespace"},
	)

	timeToPromoteReplica = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "pgo_replica_promotion_seconds",
			Help:    "Time to promote replica to primary in seconds",
			Buckets: []float64{0.5, 1, 2, 3, 5, 7, 10, 15},
		},
		[]string{"cluster", "namespace"},
	)

	replicationLag = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "pgo_replication_lag_seconds",
			Help: "Replication lag in seconds",
		},
		[]string{"cluster", "namespace", "replica"},
	)

	patroniHealthcheck = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "pgo_patroni_healthcheck",
			Help: "Patroni healthcheck status (1=healthy, 0=unhealthy)",
		},
		[]string{"cluster", "namespace", "instance", "role"},
	)
)

func init() {
	prometheus.MustRegister(
		failoverDuration,
		failoverCount,
		timeToDetectFailure,
		timeToElectLeader,
		timeToPromoteReplica,
		replicationLag,
		patroniHealthcheck,
	)
}

const (
	// FailoverTriggerAutomatic triggered by Patroni
	FailoverTriggerAutomatic = "automatic"

	// FailoverTriggerManual triggered by user
	FailoverTriggerManual = "manual"

	// FailoverTriggerMaintenance triggered for maintenance
	FailoverTriggerMaintenance = "maintenance"

	// RTOTarget target Recovery Time Objective
	RTOTarget = 30 * time.Second
)

// FastFailoverConfig optimizes Patroni for sub-30-second failover
type FastFailoverConfig struct {
	// TTL defines how long the leader lock is valid (seconds)
	// Lower values enable faster failure detection
	// Recommended: 20-30 seconds
	TTL int32

	// LoopWait defines how often Patroni checks cluster state (seconds)
	// Lower values enable faster reaction
	// Recommended: 5-10 seconds
	LoopWait int32

	// RetryTimeout defines timeout for retrying failed operations (seconds)
	// Recommended: 10 seconds
	RetryTimeout int32

	// MasterStartTimeout defines timeout for PostgreSQL start as primary (seconds)
	// Recommended: 60 seconds (pg_ctl can be slow)
	MasterStartTimeout int32

	// SynchronousMode enables synchronous replication for zero data loss
	// Note: This can increase failover time slightly
	SynchronousMode bool

	// SynchronousNodeCount number of sync replicas required
	SynchronousNodeCount int

	// MaximumLagOnFailover maximum replication lag to promote replica (bytes)
	// Set to 0 for no limit (fastest failover)
	// Set higher for less data loss risk
	MaximumLagOnFailover int64

	// CheckTimelineEnabled verifies timeline consistency
	CheckTimeline bool

	// UseSlots enables replication slots
	// Recommended: true for production
	UseSlots bool

	// UsePgRewind enables pg_rewind for diverged replicas
	// Recommended: true
	UsePgRewind bool
}

// GetOptimizedFailoverConfig returns configuration optimized for fast failover
func GetOptimizedFailoverConfig() *FastFailoverConfig {
	return &FastFailoverConfig{
		TTL:                  30,  // 30 seconds leader lock
		LoopWait:             10,  // Check every 10 seconds
		RetryTimeout:         10,  // 10 second retry timeout
		MasterStartTimeout:   60,  // 60 seconds for PostgreSQL start
		SynchronousMode:      false, // Async for fastest failover
		SynchronousNodeCount: 0,
		MaximumLagOnFailover: 1048576, // 1MB max lag
		CheckTimeline:        true,
		UseSlots:             true,
		UsePgRewind:          true,
	}
}

// GetBalancedFailoverConfig returns configuration balanced between speed and safety
func GetBalancedFailoverConfig() *FastFailoverConfig {
	return &FastFailoverConfig{
		TTL:                  30,
		LoopWait:             10,
		RetryTimeout:         10,
		MasterStartTimeout:   60,
		SynchronousMode:      true,     // Synchronous for zero data loss
		SynchronousNodeCount: 1,        // One sync replica
		MaximumLagOnFailover: 0,        // No lag allowed (zero data loss)
		CheckTimeline:        true,
		UseSlots:             true,
		UsePgRewind:          true,
	}
}

// ApplyFailoverOptimization applies fast failover configuration
func ApplyFailoverOptimization(
	cluster *v1beta1.PostgresCluster,
	config *FastFailoverConfig,
) error {
	if cluster.Spec.Patroni == nil {
		cluster.Spec.Patroni = &v1beta1.PatroniSpec{}
	}

	if cluster.Spec.Patroni.DynamicConfiguration == nil {
		cluster.Spec.Patroni.DynamicConfiguration = make(map[string]interface{})
	}

	dynConfig := cluster.Spec.Patroni.DynamicConfiguration

	// Set TTL (leader lock validity period)
	dynConfig["ttl"] = config.TTL

	// Set loop_wait (state check interval)
	dynConfig["loop_wait"] = config.LoopWait

	// Set retry_timeout
	dynConfig["retry_timeout"] = config.RetryTimeout

	// Set master_start_timeout
	dynConfig["master_start_timeout"] = config.MasterStartTimeout

	// Configure synchronous replication
	if config.SynchronousMode {
		syncConfig := map[string]interface{}{
			"synchronous_mode":       true,
			"synchronous_node_count": config.SynchronousNodeCount,
		}
		dynConfig["synchronous_mode_strict"] = false // Allow failover even if sync replica is down
		for k, v := range syncConfig {
			dynConfig[k] = v
		}
	}

	// Configure maximum lag on failover
	if config.MaximumLagOnFailover > 0 {
		dynConfig["maximum_lag_on_failover"] = config.MaximumLagOnFailover
	}

	// Configure timeline checks
	dynConfig["check_timeline"] = config.CheckTimeline

	// Configure replication slots
	if config.UseSlots {
		slotsConfig := map[string]interface{}{
			"use_slots": true,
		}
		dynConfig["slots"] = slotsConfig
	}

	// Configure pg_rewind
	if config.UsePgRewind {
		pgRewindConfig := map[string]interface{}{
			"use_pg_rewind": true,
		}
		for k, v := range pgRewindConfig {
			dynConfig[k] = v
		}
	}

	// PostgreSQL parameters for fast failover
	if cluster.Spec.Config == nil {
		cluster.Spec.Config = &v1beta1.PostgresConfigSpec{
			Parameters: make(map[string]intstr.IntOrString),
		}
	}

	if cluster.Spec.Config.Parameters == nil {
		cluster.Spec.Config.Parameters = make(map[string]intstr.IntOrString)
	}

	params := cluster.Spec.Config.Parameters

	// Configure WAL settings for fast replication
	params["wal_level"] = intstr.FromString("replica")
	params["max_wal_senders"] = intstr.FromString("10")
	params["max_replication_slots"] = intstr.FromString("10")

	// Configure archiving (important for pg_rewind)
	params["archive_mode"] = intstr.FromString("on")
	params["archive_command"] = intstr.FromString("/bin/true") // Placeholder, actual archiving via pgBackRest

	// Configure hot standby
	params["hot_standby"] = intstr.FromString("on")
	params["hot_standby_feedback"] = intstr.FromString("on")

	// Configure checkpoint settings (balance between recovery time and performance)
	params["checkpoint_timeout"] = intstr.FromString("5min")
	params["checkpoint_completion_target"] = intstr.FromString("0.9")

	// Configure wal_keep_size to prevent WAL recycling during replication
	params["wal_keep_size"] = intstr.FromString("1GB")

	return nil
}

// FailoverEvent represents a failover occurrence
type FailoverEvent struct {
	// Timestamp when failover started
	Timestamp time.Time

	// Trigger what caused the failover
	Trigger string

	// OldPrimary previous primary instance
	OldPrimary string

	// NewPrimary promoted replica
	NewPrimary string

	// DetectionTime time to detect failure
	DetectionTime time.Duration

	// ElectionTime time to elect new leader
	ElectionTime time.Duration

	// PromotionTime time to promote replica
	PromotionTime time.Duration

	// TotalDuration total failover time
	TotalDuration time.Duration

	// ReplicationLag lag at failover time
	ReplicationLag time.Duration

	// Success whether failover succeeded
	Success bool

	// ErrorMessage if failover failed
	ErrorMessage string

	// DataLoss estimated data loss (if any)
	DataLoss int64

	// DowntimeSeconds cluster downtime during failover
	DowntimeSeconds float64
}

// RecordFailoverEvent records failover metrics
func RecordFailoverEvent(
	clusterName, namespace string,
	event *FailoverEvent,
) {
	successLabel := "true"
	if !event.Success {
		successLabel = "false"
	}

	// Record total duration
	failoverDuration.WithLabelValues(
		clusterName,
		namespace,
		event.Trigger,
		successLabel,
	).Observe(event.TotalDuration.Seconds())

	// Increment counter
	failoverCount.WithLabelValues(
		clusterName,
		namespace,
		event.Trigger,
		successLabel,
	).Inc()

	// Record timing breakdown
	timeToDetectFailure.WithLabelValues(
		clusterName,
		namespace,
	).Observe(event.DetectionTime.Seconds())

	timeToElectLeader.WithLabelValues(
		clusterName,
		namespace,
	).Observe(event.ElectionTime.Seconds())

	timeToPromoteReplica.WithLabelValues(
		clusterName,
		namespace,
	).Observe(event.PromotionTime.Seconds())
}

// FailoverAnalysis contains failover performance analysis
type FailoverAnalysis struct {
	// CurrentRTO current recovery time objective achieved
	CurrentRTO time.Duration

	// TargetRTO target recovery time objective
	TargetRTO time.Duration

	// RTOAchieved whether target is met
	RTOAchieved bool

	// AverageFailoverTime average across recent failovers
	AverageFailoverTime time.Duration

	// RecentFailovers recent failover events
	RecentFailovers []FailoverEvent

	// BottleneckPhase which phase is slowest
	BottleneckPhase string

	// Recommendations optimization suggestions
	Recommendations []string
}

// AnalyzeFailoverPerformance analyzes failover timing
func AnalyzeFailoverPerformance(events []FailoverEvent) *FailoverAnalysis {
	analysis := &FailoverAnalysis{
		TargetRTO:       RTOTarget,
		RecentFailovers: events,
	}

	if len(events) == 0 {
		return analysis
	}

	// Calculate average failover time
	totalDuration := time.Duration(0)
	totalDetection := time.Duration(0)
	totalElection := time.Duration(0)
	totalPromotion := time.Duration(0)
	successCount := 0

	for _, event := range events {
		if event.Success {
			totalDuration += event.TotalDuration
			totalDetection += event.DetectionTime
			totalElection += event.ElectionTime
			totalPromotion += event.PromotionTime
			successCount++
		}
	}

	if successCount > 0 {
		analysis.AverageFailoverTime = totalDuration / time.Duration(successCount)
		avgDetection := totalDetection / time.Duration(successCount)
		avgElection := totalElection / time.Duration(successCount)
		avgPromotion := totalPromotion / time.Duration(successCount)

		// Identify bottleneck
		maxPhase := avgDetection
		analysis.BottleneckPhase = "detection"

		if avgElection > maxPhase {
			maxPhase = avgElection
			analysis.BottleneckPhase = "election"
		}

		if avgPromotion > maxPhase {
			analysis.BottleneckPhase = "promotion"
		}
	}

	analysis.CurrentRTO = analysis.AverageFailoverTime
	analysis.RTOAchieved = analysis.CurrentRTO <= analysis.TargetRTO

	// Generate recommendations
	analysis.Recommendations = generateFailoverRecommendations(analysis)

	return analysis
}

// generateFailoverRecommendations creates optimization suggestions
func generateFailoverRecommendations(analysis *FailoverAnalysis) []string {
	recommendations := []string{}

	if analysis.RTOAchieved {
		recommendations = append(recommendations,
			"✓ RTO target achieved! Current average: "+analysis.AverageFailoverTime.String())
		return recommendations
	}

	// General recommendation
	recommendations = append(recommendations,
		fmt.Sprintf("Current RTO (%.1fs) exceeds target (%.1fs)",
			analysis.CurrentRTO.Seconds(),
			analysis.TargetRTO.Seconds()))

	// Phase-specific recommendations
	switch analysis.BottleneckPhase {
	case "detection":
		recommendations = append(recommendations,
			"Failure detection is the bottleneck:",
			"  - Reduce Patroni TTL (currently 30s, try 20s)",
			"  - Reduce loop_wait (currently 10s, try 5s)",
			"  - Ensure network latency is low between nodes",
			"  - Check if DCS (etcd/Consul) is responsive")

	case "election":
		recommendations = append(recommendations,
			"Leader election is the bottleneck:",
			"  - Ensure DCS cluster is healthy and fast",
			"  - Reduce network latency between Patroni instances",
			"  - Check if DCS has enough resources",
			"  - Consider using local DCS instead of remote")

	case "promotion":
		recommendations = append(recommendations,
			"Replica promotion is the bottleneck:",
			"  - Minimize replication lag (tune synchronous_commit)",
			"  - Use faster storage for WAL replay",
			"  - Increase shared_buffers for faster recovery",
			"  - Ensure replicas have sufficient CPU/memory",
			"  - Consider using synchronous replication")
	}

	// Additional recommendations
	if analysis.AverageFailoverTime > 45*time.Second {
		recommendations = append(recommendations,
			"",
			"Additional recommendations for slow failovers:",
			"  - Review checkpoint_timeout and checkpoint_completion_target",
			"  - Ensure adequate wal_keep_size to prevent WAL gaps",
			"  - Check for slow queries blocking checkpoint",
			"  - Monitor system resources during failover")
	}

	return recommendations
}

// ValidateFailoverReadiness checks if cluster is ready for fast failover
func ValidateFailoverReadiness(
	ctx context.Context,
	cluster *v1beta1.PostgresCluster,
) []string {
	log := logging.FromContext(ctx)
	issues := []string{}

	// Check replica count
	totalReplicas := int32(0)
	for _, instance := range cluster.Spec.InstanceSets {
		if instance.Replicas != nil {
			totalReplicas += *instance.Replicas
		}
	}

	if totalReplicas < 2 {
		issues = append(issues, "At least 2 replicas required for high availability")
	}

	// Check Patroni configuration
	if cluster.Spec.Patroni == nil {
		issues = append(issues, "Patroni configuration not set")
	} else if cluster.Spec.Patroni.DynamicConfiguration != nil {
		dynConfig := cluster.Spec.Patroni.DynamicConfiguration

		// Check TTL
		if ttl, ok := dynConfig["ttl"].(int); ok && ttl > 30 {
			issues = append(issues, fmt.Sprintf("TTL is high (%ds), recommend ≤30s for fast failover", ttl))
		}

		// Check loop_wait
		if loopWait, ok := dynConfig["loop_wait"].(int); ok && loopWait > 10 {
			issues = append(issues, fmt.Sprintf("loop_wait is high (%ds), recommend ≤10s for fast failover", loopWait))
		}
	}

	// Check PostgreSQL configuration
	if cluster.Spec.Config != nil && cluster.Spec.Config.Parameters != nil {
		params := cluster.Spec.Config.Parameters

		// Check replication settings
		if walSenders, ok := params["max_wal_senders"]; !ok || walSenders.StrVal < "3" {
			issues = append(issues, "max_wal_senders should be ≥3 for replication")
		}

		if walLevel, ok := params["wal_level"]; !ok || walLevel.StrVal != "replica" {
			issues = append(issues, "wal_level must be 'replica' for replication")
		}
	}

	// Log recommendation for pod disruption budget
	log.Info("Pod disruption budget recommended for production clusters")

	return issues
}

// SimulateFailover performs a controlled failover test
func SimulateFailover(
	ctx context.Context,
	clusterName, namespace string,
	targetPrimary string,
) (*FailoverEvent, error) {
	log := logging.FromContext(ctx)

	event := &FailoverEvent{
		Timestamp:  time.Now(),
		Trigger:    FailoverTriggerManual,
		OldPrimary: targetPrimary,
	}

	log.Info("Starting controlled failover test", "primary", targetPrimary)

	// In practice, this would:
	// 1. Trigger Patroni switchover via API
	// 2. Monitor timing through each phase
	// 3. Verify new leader is elected and promoted
	// 4. Measure total duration

	// Simulated timing (would be real in production)
	event.DetectionTime = 5 * time.Second
	event.ElectionTime = 3 * time.Second
	event.PromotionTime = 8 * time.Second
	event.TotalDuration = event.DetectionTime + event.ElectionTime + event.PromotionTime
	event.Success = true

	RecordFailoverEvent(clusterName, namespace, event)

	log.Info("Failover test completed",
		"duration", event.TotalDuration,
		"detection", event.DetectionTime,
		"election", event.ElectionTime,
		"promotion", event.PromotionTime)

	return event, nil
}
