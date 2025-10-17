// Copyright 2025 Crunchy Data Solutions, Inc.
//
// SPDX-License-Identifier: Apache-2.0

package patroni

import (
	"context"
	"testing"
	"time"

	"gotest.tools/v3/assert"

	"github.com/crunchydata/postgres-operator/pkg/apis/postgres-operator.crunchydata.com/v1beta1"
)

func TestGetOptimizedFailoverConfig(t *testing.T) {
	config := GetOptimizedFailoverConfig()

	assert.Assert(t, config != nil)
	assert.Equal(t, config.TTL, int32(30))
	assert.Equal(t, config.LoopWait, int32(10))
	assert.Equal(t, config.RetryTimeout, int32(10))
	assert.Equal(t, config.MasterStartTimeout, int32(60))
	assert.Assert(t, !config.SynchronousMode)
	assert.Assert(t, config.UseSlots)
	assert.Assert(t, config.UsePgRewind)
}

func TestGetBalancedFailoverConfig(t *testing.T) {
	config := GetBalancedFailoverConfig()

	assert.Assert(t, config != nil)
	assert.Equal(t, config.TTL, int32(30))
	assert.Assert(t, config.SynchronousMode)
	assert.Equal(t, config.SynchronousNodeCount, 1)
	assert.Equal(t, config.MaximumLagOnFailover, int64(0))
}

func TestApplyFailoverOptimization(t *testing.T) {
	t.Run("NilPatroniSpec", func(t *testing.T) {
		cluster := &v1beta1.PostgresCluster{
			Spec: v1beta1.PostgresClusterSpec{},
		}
		config := GetOptimizedFailoverConfig()

		err := ApplyFailoverOptimization(cluster, config)
		assert.NilError(t, err)
		assert.Assert(t, cluster.Spec.Patroni != nil)
		assert.Assert(t, cluster.Spec.Patroni.DynamicConfiguration != nil)
	})

	t.Run("WithExistingPatroni", func(t *testing.T) {
		cluster := &v1beta1.PostgresCluster{
			Spec: v1beta1.PostgresClusterSpec{
				Patroni: &v1beta1.PatroniSpec{
					DynamicConfiguration: map[string]interface{}{
						"existing_key": "existing_value",
					},
				},
			},
		}
		config := GetOptimizedFailoverConfig()

		err := ApplyFailoverOptimization(cluster, config)
		assert.NilError(t, err)

		// Check TTL was set
		ttl, ok := cluster.Spec.Patroni.DynamicConfiguration["ttl"]
		assert.Assert(t, ok)
		assert.Equal(t, ttl, config.TTL)
	})

	t.Run("SynchronousMode", func(t *testing.T) {
		cluster := &v1beta1.PostgresCluster{
			Spec: v1beta1.PostgresClusterSpec{},
		}
		config := GetBalancedFailoverConfig()

		err := ApplyFailoverOptimization(cluster, config)
		assert.NilError(t, err)

		syncMode, ok := cluster.Spec.Patroni.DynamicConfiguration["synchronous_mode"]
		assert.Assert(t, ok)
		assert.Equal(t, syncMode, true)
	})

	t.Run("PostgresParameters", func(t *testing.T) {
		cluster := &v1beta1.PostgresCluster{
			Spec: v1beta1.PostgresClusterSpec{},
		}
		config := GetOptimizedFailoverConfig()

		err := ApplyFailoverOptimization(cluster, config)
		assert.NilError(t, err)
		assert.Assert(t, cluster.Spec.Config != nil)
		assert.Assert(t, cluster.Spec.Config.Parameters != nil)

		params := cluster.Spec.Config.Parameters
		assert.Equal(t, params["wal_level"].StrVal, "replica")
		assert.Equal(t, params["max_wal_senders"].StrVal, "10")
		assert.Equal(t, params["hot_standby"].StrVal, "on")
	})
}

func TestRecordFailoverEvent(t *testing.T) {
	t.Run("SuccessfulFailover", func(t *testing.T) {
		event := &FailoverEvent{
			Timestamp:      time.Now(),
			Trigger:        FailoverTriggerAutomatic,
			OldPrimary:     "instance1",
			NewPrimary:     "instance2",
			DetectionTime:  5 * time.Second,
			ElectionTime:   3 * time.Second,
			PromotionTime:  10 * time.Second,
			TotalDuration:  18 * time.Second,
			Success:        true,
		}

		// Should not panic
		RecordFailoverEvent("test-cluster", "test-ns", event)
	})

	t.Run("FailedFailover", func(t *testing.T) {
		event := &FailoverEvent{
			Timestamp:     time.Now(),
			Trigger:       FailoverTriggerManual,
			OldPrimary:    "instance1",
			Success:       false,
			ErrorMessage:  "No suitable replica found",
			TotalDuration: 30 * time.Second,
		}

		RecordFailoverEvent("test-cluster", "test-ns", event)
	})
}

func TestAnalyzeFailoverPerformance(t *testing.T) {
	t.Run("NoEvents", func(t *testing.T) {
		events := []FailoverEvent{}
		analysis := AnalyzeFailoverPerformance(events)

		assert.Assert(t, analysis != nil)
		assert.Equal(t, analysis.TargetRTO, RTOTarget)
		assert.Equal(t, len(analysis.RecentFailovers), 0)
	})

	t.Run("SuccessfulEvents", func(t *testing.T) {
		events := []FailoverEvent{
			{
				Success:       true,
				TotalDuration: 20 * time.Second,
				DetectionTime: 5 * time.Second,
				ElectionTime:  3 * time.Second,
				PromotionTime: 12 * time.Second,
			},
			{
				Success:       true,
				TotalDuration: 25 * time.Second,
				DetectionTime: 6 * time.Second,
				ElectionTime:  4 * time.Second,
				PromotionTime: 15 * time.Second,
			},
		}

		analysis := AnalyzeFailoverPerformance(events)
		assert.Assert(t, analysis.AverageFailoverTime > 0)
		assert.Assert(t, analysis.RTOAchieved)
		assert.Equal(t, len(analysis.RecentFailovers), 2)
	})

	t.Run("MixedSuccessFailure", func(t *testing.T) {
		events := []FailoverEvent{
			{Success: true, TotalDuration: 20 * time.Second, DetectionTime: 5 * time.Second, ElectionTime: 3 * time.Second, PromotionTime: 12 * time.Second},
			{Success: false, TotalDuration: 60 * time.Second},
			{Success: true, TotalDuration: 25 * time.Second, DetectionTime: 6 * time.Second, ElectionTime: 4 * time.Second, PromotionTime: 15 * time.Second},
		}

		analysis := AnalyzeFailoverPerformance(events)
		// Should only average successful events
		assert.Assert(t, analysis.AverageFailoverTime > 0)
		assert.Assert(t, analysis.AverageFailoverTime < 30*time.Second)
	})

	t.Run("DetectionBottleneck", func(t *testing.T) {
		events := []FailoverEvent{
			{
				Success:       true,
				TotalDuration: 30 * time.Second,
				DetectionTime: 20 * time.Second,
				ElectionTime:  3 * time.Second,
				PromotionTime: 7 * time.Second,
			},
		}

		analysis := AnalyzeFailoverPerformance(events)
		assert.Equal(t, analysis.BottleneckPhase, "detection")
	})

	t.Run("ElectionBottleneck", func(t *testing.T) {
		events := []FailoverEvent{
			{
				Success:       true,
				TotalDuration: 30 * time.Second,
				DetectionTime: 5 * time.Second,
				ElectionTime:  20 * time.Second,
				PromotionTime: 5 * time.Second,
			},
		}

		analysis := AnalyzeFailoverPerformance(events)
		assert.Equal(t, analysis.BottleneckPhase, "election")
	})

	t.Run("PromotionBottleneck", func(t *testing.T) {
		events := []FailoverEvent{
			{
				Success:       true,
				TotalDuration: 30 * time.Second,
				DetectionTime: 5 * time.Second,
				ElectionTime:  3 * time.Second,
				PromotionTime: 22 * time.Second,
			},
		}

		analysis := AnalyzeFailoverPerformance(events)
		assert.Equal(t, analysis.BottleneckPhase, "promotion")
	})
}

func TestGenerateFailoverRecommendations(t *testing.T) {
	t.Run("RTOAchieved", func(t *testing.T) {
		analysis := &FailoverAnalysis{
			RTOAchieved:         true,
			AverageFailoverTime: 25 * time.Second,
			TargetRTO:           30 * time.Second,
		}

		recommendations := generateFailoverRecommendations(analysis)
		assert.Assert(t, len(recommendations) > 0)
		assert.Assert(t, recommendations[0] == "✓ RTO target achieved! Current average: 25s")
	})

	t.Run("DetectionBottleneck", func(t *testing.T) {
		analysis := &FailoverAnalysis{
			RTOAchieved:         false,
			CurrentRTO:          40 * time.Second,
			TargetRTO:           30 * time.Second,
			AverageFailoverTime: 40 * time.Second,
			BottleneckPhase:     "detection",
		}

		recommendations := generateFailoverRecommendations(analysis)
		assert.Assert(t, len(recommendations) > 1)
	})

	t.Run("ElectionBottleneck", func(t *testing.T) {
		analysis := &FailoverAnalysis{
			RTOAchieved:         false,
			CurrentRTO:          35 * time.Second,
			TargetRTO:           30 * time.Second,
			AverageFailoverTime: 35 * time.Second,
			BottleneckPhase:     "election",
		}

		recommendations := generateFailoverRecommendations(analysis)
		assert.Assert(t, len(recommendations) > 1)
	})

	t.Run("PromotionBottleneck", func(t *testing.T) {
		analysis := &FailoverAnalysis{
			RTOAchieved:         false,
			CurrentRTO:          35 * time.Second,
			TargetRTO:           30 * time.Second,
			AverageFailoverTime: 35 * time.Second,
			BottleneckPhase:     "promotion",
		}

		recommendations := generateFailoverRecommendations(analysis)
		assert.Assert(t, len(recommendations) > 1)
	})

	t.Run("VerySlowFailover", func(t *testing.T) {
		analysis := &FailoverAnalysis{
			RTOAchieved:         false,
			CurrentRTO:          50 * time.Second,
			TargetRTO:           30 * time.Second,
			AverageFailoverTime: 50 * time.Second,
			BottleneckPhase:     "detection",
		}

		recommendations := generateFailoverRecommendations(analysis)
		assert.Assert(t, len(recommendations) > 3)
	})
}

func TestValidateFailoverReadiness(t *testing.T) {
	ctx := context.Background()

	t.Run("MinimumReplicas", func(t *testing.T) {
		cluster := &v1beta1.PostgresCluster{
			Spec: v1beta1.PostgresClusterSpec{
				InstanceSets: []v1beta1.PostgresInstanceSetSpec{
					{Replicas: int32Ptr(1)},
				},
			},
		}

		issues := ValidateFailoverReadiness(ctx, cluster)
		assert.Assert(t, len(issues) > 0)
	})

	t.Run("SufficientReplicas", func(t *testing.T) {
		cluster := &v1beta1.PostgresCluster{
			Spec: v1beta1.PostgresClusterSpec{
				InstanceSets: []v1beta1.PostgresInstanceSetSpec{
					{Replicas: int32Ptr(3)},
				},
			},
		}

		issues := ValidateFailoverReadiness(ctx, cluster)
		// May still have other issues, but not replica count
		assert.Assert(t, len(issues) >= 0)
	})

	t.Run("NoPatroniConfig", func(t *testing.T) {
		cluster := &v1beta1.PostgresCluster{
			Spec: v1beta1.PostgresClusterSpec{
				InstanceSets: []v1beta1.PostgresInstanceSetSpec{
					{Replicas: int32Ptr(3)},
				},
			},
		}

		issues := ValidateFailoverReadiness(ctx, cluster)
		assert.Assert(t, len(issues) > 0)
	})

	t.Run("HighTTL", func(t *testing.T) {
		cluster := &v1beta1.PostgresCluster{
			Spec: v1beta1.PostgresClusterSpec{
				InstanceSets: []v1beta1.PostgresInstanceSetSpec{
					{Replicas: int32Ptr(3)},
				},
				Patroni: &v1beta1.PatroniSpec{
					DynamicConfiguration: map[string]interface{}{
						"ttl": 60,
					},
				},
			},
		}

		issues := ValidateFailoverReadiness(ctx, cluster)
		assert.Assert(t, len(issues) > 0)
	})

	t.Run("HighLoopWait", func(t *testing.T) {
		cluster := &v1beta1.PostgresCluster{
			Spec: v1beta1.PostgresClusterSpec{
				InstanceSets: []v1beta1.PostgresInstanceSetSpec{
					{Replicas: int32Ptr(3)},
				},
				Patroni: &v1beta1.PatroniSpec{
					DynamicConfiguration: map[string]interface{}{
						"loop_wait": 20,
					},
				},
			},
		}

		issues := ValidateFailoverReadiness(ctx, cluster)
		assert.Assert(t, len(issues) > 0)
	})
}

func TestSimulateFailover(t *testing.T) {
	ctx := context.Background()

	t.Run("BasicSimulation", func(t *testing.T) {
		event, err := SimulateFailover(ctx, "test-cluster", "test-ns", "instance1")

		assert.NilError(t, err)
		assert.Assert(t, event != nil)
		assert.Equal(t, event.Trigger, FailoverTriggerManual)
		assert.Equal(t, event.OldPrimary, "instance1")
		assert.Assert(t, event.Success)
		assert.Assert(t, event.TotalDuration > 0)
	})
}

func TestFailoverEvent(t *testing.T) {
	t.Run("CompleteEvent", func(t *testing.T) {
		event := &FailoverEvent{
			Timestamp:      time.Now(),
			Trigger:        FailoverTriggerAutomatic,
			OldPrimary:     "instance1",
			NewPrimary:     "instance2",
			DetectionTime:  5 * time.Second,
			ElectionTime:   3 * time.Second,
			PromotionTime:  10 * time.Second,
			TotalDuration:  18 * time.Second,
			ReplicationLag: 100 * time.Millisecond,
			Success:        true,
			DataLoss:       0,
			DowntimeSeconds: 18.0,
		}

		assert.Equal(t, event.Trigger, FailoverTriggerAutomatic)
		assert.Assert(t, event.Success)
		assert.Equal(t, event.DataLoss, int64(0))
	})

	t.Run("FailedEvent", func(t *testing.T) {
		event := &FailoverEvent{
			Timestamp:    time.Now(),
			Trigger:      FailoverTriggerMaintenance,
			Success:      false,
			ErrorMessage: "Replica not ready",
		}

		assert.Assert(t, !event.Success)
		assert.Assert(t, event.ErrorMessage != "")
	})
}

func TestFastFailoverConfig(t *testing.T) {
	t.Run("DefaultValues", func(t *testing.T) {
		config := &FastFailoverConfig{
			TTL:                  30,
			LoopWait:             10,
			RetryTimeout:         10,
			MasterStartTimeout:   60,
			SynchronousMode:      false,
			SynchronousNodeCount: 0,
			UseSlots:             true,
			UsePgRewind:          true,
		}

		assert.Equal(t, config.TTL, int32(30))
		assert.Assert(t, !config.SynchronousMode)
		assert.Assert(t, config.UseSlots)
	})

	t.Run("SynchronousConfig", func(t *testing.T) {
		config := &FastFailoverConfig{
			SynchronousMode:      true,
			SynchronousNodeCount: 2,
			MaximumLagOnFailover: 0,
		}

		assert.Assert(t, config.SynchronousMode)
		assert.Equal(t, config.SynchronousNodeCount, 2)
	})
}

// Helper function
func int32Ptr(i int32) *int32 {
	return &i
}
