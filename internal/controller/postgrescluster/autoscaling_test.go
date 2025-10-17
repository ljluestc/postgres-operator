// Copyright 2025 Crunchy Data Solutions, Inc.
//
// SPDX-License-Identifier: Apache-2.0

package postgrescluster

import (
	"testing"
	"time"

	"gotest.tools/v3/assert"
	autoscalingv2 "k8s.io/api/autoscaling/v2"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/crunchydata/postgres-operator/pkg/apis/postgres-operator.crunchydata.com/v1beta1"
)

func TestGetAutoscalingConfig(t *testing.T) {
	t.Run("NoAnnotations", func(t *testing.T) {
		cluster := &v1beta1.PostgresCluster{
			ObjectMeta: metav1.ObjectMeta{
				Annotations: nil,
			},
		}

		config := GetAutoscalingConfig(cluster)
		assert.Assert(t, config == nil)
	})

	t.Run("DisabledAutoscaling", func(t *testing.T) {
		cluster := &v1beta1.PostgresCluster{
			ObjectMeta: metav1.ObjectMeta{
				Annotations: map[string]string{
					AnnotationAutoscaleEnabled: "false",
				},
			},
		}

		config := GetAutoscalingConfig(cluster)
		assert.Assert(t, config == nil)
	})

	t.Run("EnabledWithDefaults", func(t *testing.T) {
		cluster := &v1beta1.PostgresCluster{
			ObjectMeta: metav1.ObjectMeta{
				Annotations: map[string]string{
					AnnotationAutoscaleEnabled: "true",
				},
			},
		}

		config := GetAutoscalingConfig(cluster)
		assert.Assert(t, config != nil)
		assert.Assert(t, config.Enabled)
		assert.Equal(t, config.MinReplicas, int32(DefaultMinReplicas))
		assert.Equal(t, config.MaxReplicas, int32(DefaultMaxReplicas))
	})

	t.Run("CustomMinMax", func(t *testing.T) {
		cluster := &v1beta1.PostgresCluster{
			ObjectMeta: metav1.ObjectMeta{
				Annotations: map[string]string{
					AnnotationAutoscaleEnabled: "true",
					AnnotationAutoscaleMin:     "2",
					AnnotationAutoscaleMax:     "20",
				},
			},
		}

		config := GetAutoscalingConfig(cluster)
		assert.Assert(t, config != nil)
		assert.Equal(t, config.MinReplicas, int32(2))
		assert.Equal(t, config.MaxReplicas, int32(20))
	})

	t.Run("WithCPUMetric", func(t *testing.T) {
		cluster := &v1beta1.PostgresCluster{
			ObjectMeta: metav1.ObjectMeta{
				Annotations: map[string]string{
					AnnotationAutoscaleEnabled:   "true",
					AnnotationAutoscaleTargetCPU: "80",
				},
			},
		}

		config := GetAutoscalingConfig(cluster)
		assert.Assert(t, config != nil)
		assert.Assert(t, len(config.Metrics) > 0)

		found := false
		for _, metric := range config.Metrics {
			if metric.MetricName == "cpu" {
				found = true
				assert.Equal(t, metric.TargetValue, int64(80))
			}
		}
		assert.Assert(t, found)
	})

	t.Run("WithMemoryMetric", func(t *testing.T) {
		cluster := &v1beta1.PostgresCluster{
			ObjectMeta: metav1.ObjectMeta{
				Annotations: map[string]string{
					AnnotationAutoscaleEnabled:      "true",
					AnnotationAutoscaleTargetMemory: "85",
				},
			},
		}

		config := GetAutoscalingConfig(cluster)
		assert.Assert(t, config != nil)

		found := false
		for _, metric := range config.Metrics {
			if metric.MetricName == "memory" {
				found = true
				assert.Equal(t, metric.TargetValue, int64(85))
			}
		}
		assert.Assert(t, found)
	})

	t.Run("WithConnectionsMetric", func(t *testing.T) {
		cluster := &v1beta1.PostgresCluster{
			ObjectMeta: metav1.ObjectMeta{
				Annotations: map[string]string{
					AnnotationAutoscaleEnabled:            "true",
					AnnotationAutoscaleTargetConnections:  "150",
				},
			},
		}

		config := GetAutoscalingConfig(cluster)
		assert.Assert(t, config != nil)

		found := false
		for _, metric := range config.Metrics {
			if metric.MetricName == "postgresql_connections" {
				found = true
				assert.Equal(t, metric.TargetValue, int64(150))
			}
		}
		assert.Assert(t, found)
	})

	t.Run("WithReplicationLagMetric", func(t *testing.T) {
		cluster := &v1beta1.PostgresCluster{
			ObjectMeta: metav1.ObjectMeta{
				Annotations: map[string]string{
					AnnotationAutoscaleEnabled:              "true",
					AnnotationAutoscaleTargetReplicationLag: "5",
				},
			},
		}

		config := GetAutoscalingConfig(cluster)
		assert.Assert(t, config != nil)

		found := false
		for _, metric := range config.Metrics {
			if metric.MetricName == "postgresql_replication_lag_seconds" {
				found = true
				assert.Equal(t, metric.TargetValue, int64(5))
			}
		}
		assert.Assert(t, found)
	})

	t.Run("WithAllMetrics", func(t *testing.T) {
		cluster := &v1beta1.PostgresCluster{
			ObjectMeta: metav1.ObjectMeta{
				Annotations: map[string]string{
					AnnotationAutoscaleEnabled:              "true",
					AnnotationAutoscaleTargetCPU:            "70",
					AnnotationAutoscaleTargetMemory:         "80",
					AnnotationAutoscaleTargetConnections:    "100",
					AnnotationAutoscaleTargetReplicationLag: "10",
				},
			},
		}

		config := GetAutoscalingConfig(cluster)
		assert.Assert(t, config != nil)
		assert.Equal(t, len(config.Metrics), 4)
	})

	t.Run("DefaultBehavior", func(t *testing.T) {
		cluster := &v1beta1.PostgresCluster{
			ObjectMeta: metav1.ObjectMeta{
				Annotations: map[string]string{
					AnnotationAutoscaleEnabled: "true",
				},
			},
		}

		config := GetAutoscalingConfig(cluster)
		assert.Assert(t, config != nil)
		assert.Assert(t, config.Behavior != nil)
		assert.Assert(t, config.Behavior.ScaleUp != nil)
		assert.Assert(t, config.Behavior.ScaleDown != nil)
	})
}

func TestCreateHPA(t *testing.T) {
	t.Run("BasicHPA", func(t *testing.T) {
		cluster := &v1beta1.PostgresCluster{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-cluster",
				Namespace: "test-ns",
			},
			Spec: v1beta1.PostgresClusterSpec{},
		}

		config := &AutoscalingConfig{
			Enabled:     true,
			MinReplicas: 2,
			MaxReplicas: 10,
			Metrics: []ScalingMetric{
				{
					Type:        "Resource",
					TargetType:  "Utilization",
					TargetValue: 70,
					MetricName:  "cpu",
				},
			},
		}

		hpa, err := createHPA(cluster, config)
		assert.NilError(t, err)
		assert.Assert(t, hpa != nil)
		assert.Equal(t, *hpa.Spec.MinReplicas, int32(2))
		assert.Equal(t, hpa.Spec.MaxReplicas, int32(10))
	})

	t.Run("WithResourceMetrics", func(t *testing.T) {
		cluster := &v1beta1.PostgresCluster{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-cluster",
				Namespace: "test-ns",
			},
		}

		config := &AutoscalingConfig{
			MinReplicas: 1,
			MaxReplicas: 5,
			Metrics: []ScalingMetric{
				{Type: "Resource", TargetType: "Utilization", TargetValue: 70, MetricName: "cpu"},
				{Type: "Resource", TargetType: "Utilization", TargetValue: 80, MetricName: "memory"},
			},
		}

		hpa, err := createHPA(cluster, config)
		assert.NilError(t, err)
		assert.Equal(t, len(hpa.Spec.Metrics), 2)
	})

	t.Run("WithPodsMetrics", func(t *testing.T) {
		cluster := &v1beta1.PostgresCluster{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-cluster",
				Namespace: "test-ns",
			},
		}

		config := &AutoscalingConfig{
			MinReplicas: 1,
			MaxReplicas: 5,
			Metrics: []ScalingMetric{
				{
					Type:        "Pods",
					TargetType:  "AverageValue",
					TargetValue: 100,
					MetricName:  "postgresql_connections",
				},
			},
		}

		hpa, err := createHPA(cluster, config)
		assert.NilError(t, err)
		assert.Equal(t, len(hpa.Spec.Metrics), 1)
		assert.Equal(t, hpa.Spec.Metrics[0].Type, autoscalingv2.PodsMetricSourceType)
	})

	t.Run("WithBehavior", func(t *testing.T) {
		cluster := &v1beta1.PostgresCluster{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-cluster",
				Namespace: "test-ns",
			},
		}

		config := &AutoscalingConfig{
			MinReplicas: 1,
			MaxReplicas: 10,
			Metrics: []ScalingMetric{
				{Type: "Resource", MetricName: "cpu", TargetValue: 70},
			},
			Behavior: &ScalingBehavior{
				ScaleUp: &ScalingPolicy{
					StabilizationWindow: 0,
					Policies: []ScalingPolicyRule{
						{Type: "Pods", Value: 2, Period: 60 * time.Second},
					},
				},
				ScaleDown: &ScalingPolicy{
					StabilizationWindow: 300 * time.Second,
					Policies: []ScalingPolicyRule{
						{Type: "Pods", Value: 1, Period: 120 * time.Second},
					},
				},
			},
		}

		hpa, err := createHPA(cluster, config)
		assert.NilError(t, err)
		assert.Assert(t, hpa.Spec.Behavior != nil)
		assert.Assert(t, hpa.Spec.Behavior.ScaleUp != nil)
		assert.Assert(t, hpa.Spec.Behavior.ScaleDown != nil)
	})
}

func TestAutoscalingStatus(t *testing.T) {
	t.Run("BasicStatus", func(t *testing.T) {
		now := metav1.Now()
		status := &AutoscalingStatus{
			Enabled:         true,
			CurrentReplicas: 3,
			DesiredReplicas: 5,
			MinReplicas:     2,
			MaxReplicas:     10,
			LastScaleTime:   &now,
		}

		assert.Assert(t, status.Enabled)
		assert.Equal(t, status.CurrentReplicas, int32(3))
		assert.Equal(t, status.DesiredReplicas, int32(5))
	})

	t.Run("WithMetrics", func(t *testing.T) {
		status := &AutoscalingStatus{
			Enabled: true,
			CurrentMetrics: []MetricStatus{
				{Type: "Resource", Name: "cpu", CurrentValue: 75, TargetValue: 70},
				{Type: "Resource", Name: "memory", CurrentValue: 82, TargetValue: 80},
			},
		}

		assert.Equal(t, len(status.CurrentMetrics), 2)
	})

	t.Run("WithLimitedReasons", func(t *testing.T) {
		status := &AutoscalingStatus{
			Enabled:               true,
			ScalingLimitedReasons: []string{"TooManyReplicas", "BackoffLimit"},
		}

		assert.Equal(t, len(status.ScalingLimitedReasons), 2)
	})
}

func TestGenerateAutoscalingReport(t *testing.T) {
	t.Run("DisabledAutoscaling", func(t *testing.T) {
		status := &AutoscalingStatus{Enabled: false}
		config := &AutoscalingConfig{}

		report := GenerateAutoscalingReport(status, config)
		assert.Assert(t, len(report) > 0)
	})

	t.Run("EnabledWithStatus", func(t *testing.T) {
		now := metav1.Now()
		status := &AutoscalingStatus{
			Enabled:         true,
			CurrentReplicas: 5,
			DesiredReplicas: 5,
			MinReplicas:     2,
			MaxReplicas:     10,
			LastScaleTime:   &now,
			CurrentMetrics: []MetricStatus{
				{Type: "Resource", Name: "cpu", CurrentValue: 65},
			},
		}
		config := &AutoscalingConfig{}

		report := GenerateAutoscalingReport(status, config)
		assert.Assert(t, len(report) > 0)
	})

	t.Run("AtMaxReplicas", func(t *testing.T) {
		status := &AutoscalingStatus{
			Enabled:         true,
			CurrentReplicas: 10,
			DesiredReplicas: 10,
			MinReplicas:     2,
			MaxReplicas:     10,
		}
		config := &AutoscalingConfig{}

		report := GenerateAutoscalingReport(status, config)
		assert.Assert(t, len(report) > 0)
	})

	t.Run("AtMinReplicas", func(t *testing.T) {
		status := &AutoscalingStatus{
			Enabled:         true,
			CurrentReplicas: 2,
			DesiredReplicas: 5,
			MinReplicas:     2,
			MaxReplicas:     10,
		}
		config := &AutoscalingConfig{}

		report := GenerateAutoscalingReport(status, config)
		assert.Assert(t, len(report) > 0)
	})

	t.Run("ScalingDown", func(t *testing.T) {
		status := &AutoscalingStatus{
			Enabled:         true,
			CurrentReplicas: 8,
			DesiredReplicas: 5,
			MinReplicas:     2,
			MaxReplicas:     10,
		}
		config := &AutoscalingConfig{}

		report := GenerateAutoscalingReport(status, config)
		assert.Assert(t, len(report) > 0)
	})

	t.Run("WithScalingLimitations", func(t *testing.T) {
		status := &AutoscalingStatus{
			Enabled:               true,
			CurrentReplicas:       5,
			DesiredReplicas:       5,
			MinReplicas:           2,
			MaxReplicas:           10,
			ScalingLimitedReasons: []string{"DesiredWithinRange", "ReadyForNewScale"},
		}
		config := &AutoscalingConfig{}

		report := GenerateAutoscalingReport(status, config)
		assert.Assert(t, len(report) > 0)
	})
}

func TestParseInt32(t *testing.T) {
	t.Run("ValidInt", func(t *testing.T) {
		result, err := parseInt32("42")
		assert.NilError(t, err)
		assert.Equal(t, result, int32(42))
	})

	t.Run("InvalidInt", func(t *testing.T) {
		_, err := parseInt32("not-a-number")
		assert.Assert(t, err != nil)
	})

	t.Run("NegativeInt", func(t *testing.T) {
		result, err := parseInt32("-10")
		assert.NilError(t, err)
		assert.Equal(t, result, int32(-10))
	})
}

func TestParseInt64(t *testing.T) {
	t.Run("ValidInt", func(t *testing.T) {
		result, err := parseInt64("1000")
		assert.NilError(t, err)
		assert.Equal(t, result, int64(1000))
	})

	t.Run("InvalidInt", func(t *testing.T) {
		_, err := parseInt64("invalid")
		assert.Assert(t, err != nil)
	})
}

func TestMetricStatus(t *testing.T) {
	t.Run("ResourceMetric", func(t *testing.T) {
		metric := MetricStatus{
			Type:         "Resource",
			Name:         "cpu",
			CurrentValue: 75,
			TargetValue:  70,
		}

		assert.Equal(t, metric.Type, "Resource")
		assert.Equal(t, metric.Name, "cpu")
		assert.Assert(t, metric.CurrentValue > metric.TargetValue)
	})

	t.Run("PodsMetric", func(t *testing.T) {
		metric := MetricStatus{
			Type:         "Pods",
			Name:         "postgresql_connections",
			CurrentValue: 120,
			TargetValue:  100,
		}

		assert.Equal(t, metric.Type, "Pods")
	})
}

func TestScalingMetric(t *testing.T) {
	t.Run("CPUMetric", func(t *testing.T) {
		metric := ScalingMetric{
			Type:        "Resource",
			TargetType:  "Utilization",
			TargetValue: 70,
			MetricName:  "cpu",
		}

		assert.Equal(t, metric.Type, "Resource")
		assert.Equal(t, metric.MetricName, "cpu")
	})

	t.Run("CustomMetric", func(t *testing.T) {
		metric := ScalingMetric{
			Type:        "External",
			TargetType:  "Value",
			TargetValue: 1000,
			MetricName:  "custom_metric",
		}

		assert.Equal(t, metric.Type, "External")
	})
}

func TestScalingBehavior(t *testing.T) {
	t.Run("WithPolicies", func(t *testing.T) {
		behavior := &ScalingBehavior{
			ScaleUp: &ScalingPolicy{
				StabilizationWindow: 0,
				Policies: []ScalingPolicyRule{
					{Type: "Pods", Value: 2, Period: 60 * time.Second},
					{Type: "Percent", Value: 50, Period: 60 * time.Second},
				},
			},
			ScaleDown: &ScalingPolicy{
				StabilizationWindow: 300 * time.Second,
				Policies: []ScalingPolicyRule{
					{Type: "Pods", Value: 1, Period: 120 * time.Second},
				},
			},
		}

		assert.Assert(t, behavior.ScaleUp != nil)
		assert.Assert(t, behavior.ScaleDown != nil)
		assert.Equal(t, len(behavior.ScaleUp.Policies), 2)
		assert.Equal(t, len(behavior.ScaleDown.Policies), 1)
	})
}
