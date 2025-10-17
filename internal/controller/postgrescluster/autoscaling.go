// Copyright 2025 Crunchy Data Solutions, Inc.
//
// SPDX-License-Identifier: Apache-2.0

package postgrescluster

import (
	"context"
	"fmt"
	"time"

	autoscalingv2 "k8s.io/api/autoscaling/v2"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/crunchydata/postgres-operator/internal/logging"
	"github.com/crunchydata/postgres-operator/internal/naming"
	"github.com/crunchydata/postgres-operator/pkg/apis/postgres-operator.crunchydata.com/v1beta1"
)

const (
	// AnnotationAutoscaleEnabled enables autoscaling
	AnnotationAutoscaleEnabled = "postgres-operator.crunchydata.com/autoscale-enabled"

	// AnnotationAutoscaleMin minimum replicas
	AnnotationAutoscaleMin = "postgres-operator.crunchydata.com/autoscale-min-replicas"

	// AnnotationAutoscaleMax maximum replicas
	AnnotationAutoscaleMax = "postgres-operator.crunchydata.com/autoscale-max-replicas"

	// AnnotationAutoscaleTargetCPU target CPU utilization percentage
	AnnotationAutoscaleTargetCPU = "postgres-operator.crunchydata.com/autoscale-target-cpu"

	// AnnotationAutoscaleTargetMemory target memory utilization percentage
	AnnotationAutoscaleTargetMemory = "postgres-operator.crunchydata.com/autoscale-target-memory"

	// AnnotationAutoscaleTargetConnections target connection count
	AnnotationAutoscaleTargetConnections = "postgres-operator.crunchydata.com/autoscale-target-connections"

	// AnnotationAutoscaleTargetReplicationLag target replication lag in seconds
	AnnotationAutoscaleTargetReplicationLag = "postgres-operator.crunchydata.com/autoscale-target-lag"

	// Default scaling parameters
	DefaultMinReplicas         = 1
	DefaultMaxReplicas         = 10
	DefaultTargetCPU           = 70
	DefaultTargetMemory        = 80
	DefaultTargetConnections   = 100
	DefaultTargetReplicationLag = 10
)

// AutoscalingConfig configures replica autoscaling
type AutoscalingConfig struct {
	// Enabled turns on autoscaling
	Enabled bool

	// MinReplicas minimum number of replicas
	MinReplicas int32

	// MaxReplicas maximum number of replicas
	MaxReplicas int32

	// Metrics to use for scaling decisions
	Metrics []ScalingMetric

	// Behavior defines scaling behavior
	Behavior *ScalingBehavior

	// CooldownPeriod time to wait between scale operations
	CooldownPeriod time.Duration
}

// ScalingMetric defines a metric for autoscaling
type ScalingMetric struct {
	// Type of metric (CPU, Memory, Custom)
	Type string

	// TargetType how to interpret target (Utilization, AverageValue, Value)
	TargetType string

	// TargetValue target value for scaling
	TargetValue int64

	// MetricName name of custom metric
	MetricName string

	// Selector for custom metrics
	Selector *metav1.LabelSelector
}

// ScalingBehavior defines how scaling operations should occur
type ScalingBehavior struct {
	// ScaleUp behavior for scaling up
	ScaleUp *ScalingPolicy

	// ScaleDown behavior for scaling down
	ScaleDown *ScalingPolicy
}

// ScalingPolicy defines rate of scaling
type ScalingPolicy struct {
	// StabilizationWindow time to stabilize before scaling
	StabilizationWindow time.Duration

	// Policies list of policies
	Policies []ScalingPolicyRule
}

// ScalingPolicyRule defines a single scaling policy rule
type ScalingPolicyRule struct {
	// Type of policy (Pods, Percent)
	Type string

	// Value amount to scale
	Value int32

	// Period time window for this policy
	Period time.Duration
}

// GetAutoscalingConfig extracts autoscaling configuration from cluster
func GetAutoscalingConfig(cluster *v1beta1.PostgresCluster) *AutoscalingConfig {
	annotations := cluster.Annotations
	if annotations == nil {
		return nil
	}

	if annotations[AnnotationAutoscaleEnabled] != "true" {
		return nil
	}

	config := &AutoscalingConfig{
		Enabled:     true,
		MinReplicas: DefaultMinReplicas,
		MaxReplicas: DefaultMaxReplicas,
		Metrics:     []ScalingMetric{},
	}

	// Parse min replicas
	if minStr, ok := annotations[AnnotationAutoscaleMin]; ok {
		if min, err := parseInt32(minStr); err == nil {
			config.MinReplicas = min
		}
	}

	// Parse max replicas
	if maxStr, ok := annotations[AnnotationAutoscaleMax]; ok {
		if max, err := parseInt32(maxStr); err == nil {
			config.MaxReplicas = max
		}
	}

	// Add CPU metric if specified
	if cpuStr, ok := annotations[AnnotationAutoscaleTargetCPU]; ok {
		if cpu, err := parseInt64(cpuStr); err == nil {
			config.Metrics = append(config.Metrics, ScalingMetric{
				Type:        "Resource",
				TargetType:  "Utilization",
				TargetValue: cpu,
				MetricName:  "cpu",
			})
		}
	}

	// Add memory metric if specified
	if memStr, ok := annotations[AnnotationAutoscaleTargetMemory]; ok {
		if mem, err := parseInt64(memStr); err == nil {
			config.Metrics = append(config.Metrics, ScalingMetric{
				Type:        "Resource",
				TargetType:  "Utilization",
				TargetValue: mem,
				MetricName:  "memory",
			})
		}
	}

	// Add connection metric if specified
	if connStr, ok := annotations[AnnotationAutoscaleTargetConnections]; ok {
		if conn, err := parseInt64(connStr); err == nil {
			config.Metrics = append(config.Metrics, ScalingMetric{
				Type:        "Pods",
				TargetType:  "AverageValue",
				TargetValue: conn,
				MetricName:  "postgresql_connections",
			})
		}
	}

	// Add replication lag metric if specified
	if lagStr, ok := annotations[AnnotationAutoscaleTargetReplicationLag]; ok {
		if lag, err := parseInt64(lagStr); err == nil {
			config.Metrics = append(config.Metrics, ScalingMetric{
				Type:        "Pods",
				TargetType:  "AverageValue",
				TargetValue: lag,
				MetricName:  "postgresql_replication_lag_seconds",
			})
		}
	}

	// Default behavior: conservative scaling
	config.Behavior = &ScalingBehavior{
		ScaleUp: &ScalingPolicy{
			StabilizationWindow: 0, // Scale up immediately when needed
			Policies: []ScalingPolicyRule{
				{
					Type:   "Pods",
					Value:  2, // Add 2 pods at a time
					Period: 60 * time.Second,
				},
				{
					Type:   "Percent",
					Value:  50, // Or 50% of current
					Period: 60 * time.Second,
				},
			},
		},
		ScaleDown: &ScalingPolicy{
			StabilizationWindow: 300 * time.Second, // 5 minutes stabilization
			Policies: []ScalingPolicyRule{
				{
					Type:   "Pods",
					Value:  1, // Remove 1 pod at a time
					Period: 120 * time.Second,
				},
				{
					Type:   "Percent",
					Value:  10, // Or 10% of current
					Period: 120 * time.Second,
				},
			},
		},
	}

	return config
}

// ReconcileAutoscaling creates or updates HPA for read replicas
func (r *Reconciler) ReconcileAutoscaling(
	ctx context.Context,
	cluster *v1beta1.PostgresCluster,
) error {
	log := logging.FromContext(ctx)

	config := GetAutoscalingConfig(cluster)

	if config == nil || !config.Enabled {
		// Autoscaling is disabled, delete HPA if it exists
		return r.deleteHPA(ctx, cluster)
	}

	log.Info("Reconciling autoscaling", "min", config.MinReplicas, "max", config.MaxReplicas)

	// Create HPA
	hpa, err := createHPA(cluster, config)
	if err != nil {
		return fmt.Errorf("failed to create HPA: %w", err)
	}

	// Apply HPA
	if err := r.Writer.Patch(ctx, hpa, client.Apply, client.ForceOwnership, client.FieldOwner("postgres-operator")); err != nil {
		return fmt.Errorf("failed to apply HPA: %w", err)
	}

	log.Info("Autoscaling configured successfully")
	return nil
}

// createHPA creates a Horizontal Pod Autoscaler for read replicas
func createHPA(cluster *v1beta1.PostgresCluster, config *AutoscalingConfig) (*autoscalingv2.HorizontalPodAutoscaler, error) {
	meta := metav1.ObjectMeta{
		Namespace: cluster.Namespace,
		Name:      fmt.Sprintf("%s-replicas", cluster.Name),
	}
	meta.Labels = naming.Merge(
		cluster.Spec.Metadata.GetLabelsOrNil(),
		map[string]string{
			naming.LabelCluster: cluster.Name,
			"postgres-operator.crunchydata.com/autoscaling": "enabled",
		},
	)

	hpa := &autoscalingv2.HorizontalPodAutoscaler{
		TypeMeta: metav1.TypeMeta{
			APIVersion: autoscalingv2.SchemeGroupVersion.String(),
			Kind:       "HorizontalPodAutoscaler",
		},
		ObjectMeta: meta,
	}

	// Set scale target reference
	// Note: This targets the PostgresCluster itself
	// The operator should handle scaling the replica instanceSet
	hpa.Spec.ScaleTargetRef = autoscalingv2.CrossVersionObjectReference{
		APIVersion: v1beta1.GroupVersion.String(),
		Kind:       "PostgresCluster",
		Name:       cluster.Name,
	}

	hpa.Spec.MinReplicas = &config.MinReplicas
	hpa.Spec.MaxReplicas = config.MaxReplicas

	// Convert metrics to HPA format
	metrics := []autoscalingv2.MetricSpec{}

	for _, metric := range config.Metrics {
		switch metric.Type {
		case "Resource":
			resourceMetric := autoscalingv2.MetricSpec{
				Type: autoscalingv2.ResourceMetricSourceType,
				Resource: &autoscalingv2.ResourceMetricSource{
					Name: corev1.ResourceName(metric.MetricName),
					Target: autoscalingv2.MetricTarget{
						Type:               autoscalingv2.UtilizationMetricType,
						AverageUtilization: int32Ptr(int32(metric.TargetValue)),
					},
				},
			}
			metrics = append(metrics, resourceMetric)

		case "Pods":
			podsMetric := autoscalingv2.MetricSpec{
				Type: autoscalingv2.PodsMetricSourceType,
				Pods: &autoscalingv2.PodsMetricSource{
					Metric: autoscalingv2.MetricIdentifier{
						Name: metric.MetricName,
					},
					Target: autoscalingv2.MetricTarget{
						Type:         autoscalingv2.AverageValueMetricType,
						AverageValue: resource.NewQuantity(metric.TargetValue, resource.DecimalSI),
					},
				},
			}
			metrics = append(metrics, podsMetric)

		case "External":
			// Custom external metrics
			externalMetric := autoscalingv2.MetricSpec{
				Type: autoscalingv2.ExternalMetricSourceType,
				External: &autoscalingv2.ExternalMetricSource{
					Metric: autoscalingv2.MetricIdentifier{
						Name:     metric.MetricName,
						Selector: metric.Selector,
					},
					Target: autoscalingv2.MetricTarget{
						Type:  autoscalingv2.AverageValueMetricType,
						Value: resource.NewQuantity(metric.TargetValue, resource.DecimalSI),
					},
				},
			}
			metrics = append(metrics, externalMetric)
		}
	}

	hpa.Spec.Metrics = metrics

	// Configure behavior
	if config.Behavior != nil {
		behavior := &autoscalingv2.HorizontalPodAutoscalerBehavior{}

		if config.Behavior.ScaleUp != nil {
			scaleUp := &autoscalingv2.HPAScalingRules{}
			stabilizationWindowSeconds := int32(config.Behavior.ScaleUp.StabilizationWindow.Seconds())
			scaleUp.StabilizationWindowSeconds = &stabilizationWindowSeconds

			for _, policy := range config.Behavior.ScaleUp.Policies {
				policyType := autoscalingv2.PodsScalingPolicy
				if policy.Type == "Percent" {
					policyType = autoscalingv2.PercentScalingPolicy
				}

				periodSeconds := int32(policy.Period.Seconds())
				scaleUp.Policies = append(scaleUp.Policies, autoscalingv2.HPAScalingPolicy{
					Type:          policyType,
					Value:         policy.Value,
					PeriodSeconds: periodSeconds,
				})
			}

			behavior.ScaleUp = scaleUp
		}

		if config.Behavior.ScaleDown != nil {
			scaleDown := &autoscalingv2.HPAScalingRules{}
			stabilizationWindowSeconds := int32(config.Behavior.ScaleDown.StabilizationWindow.Seconds())
			scaleDown.StabilizationWindowSeconds = &stabilizationWindowSeconds

			for _, policy := range config.Behavior.ScaleDown.Policies {
				policyType := autoscalingv2.PodsScalingPolicy
				if policy.Type == "Percent" {
					policyType = autoscalingv2.PercentScalingPolicy
				}

				periodSeconds := int32(policy.Period.Seconds())
				scaleDown.Policies = append(scaleDown.Policies, autoscalingv2.HPAScalingPolicy{
					Type:          policyType,
					Value:         policy.Value,
					PeriodSeconds: periodSeconds,
				})
			}

			behavior.ScaleDown = scaleDown
		}

		hpa.Spec.Behavior = behavior
	}

	return hpa, nil
}

// deleteHPA removes the HPA if autoscaling is disabled
func (r *Reconciler) deleteHPA(ctx context.Context, cluster *v1beta1.PostgresCluster) error {
	hpa := &autoscalingv2.HorizontalPodAutoscaler{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("%s-replicas", cluster.Name),
			Namespace: cluster.Namespace,
		},
	}

	if err := r.Writer.Delete(ctx, hpa); client.IgnoreNotFound(err) != nil {
		return fmt.Errorf("failed to delete HPA: %w", err)
	}

	return nil
}

// GetAutoscalingStatus retrieves current autoscaling status
func GetAutoscalingStatus(
	ctx context.Context,
	cl client.Client,
	cluster *v1beta1.PostgresCluster,
) (*AutoscalingStatus, error) {
	hpa := &autoscalingv2.HorizontalPodAutoscaler{}
	hpaName := fmt.Sprintf("%s-replicas", cluster.Name)

	if err := cl.Get(ctx, client.ObjectKey{
		Name:      hpaName,
		Namespace: cluster.Namespace,
	}, hpa); err != nil {
		if client.IgnoreNotFound(err) == nil {
			// HPA doesn't exist, autoscaling is disabled
			return &AutoscalingStatus{Enabled: false}, nil
		}
		return nil, fmt.Errorf("failed to get HPA: %w", err)
	}

	status := &AutoscalingStatus{
		Enabled:                 true,
		CurrentReplicas:         hpa.Status.CurrentReplicas,
		DesiredReplicas:         hpa.Status.DesiredReplicas,
		MinReplicas:             *hpa.Spec.MinReplicas,
		MaxReplicas:             hpa.Spec.MaxReplicas,
		LastScaleTime:           hpa.Status.LastScaleTime,
		CurrentMetrics:          []MetricStatus{},
		ScalingLimitedReasons:   []string{},
	}

	// Extract current metrics
	for _, metric := range hpa.Status.CurrentMetrics {
		switch metric.Type {
		case autoscalingv2.ResourceMetricSourceType:
			if metric.Resource != nil {
				metricStatus := MetricStatus{
					Type:         "Resource",
					Name:         string(metric.Resource.Name),
					CurrentValue: 0,
				}
				if metric.Resource.Current.AverageUtilization != nil {
					metricStatus.CurrentValue = int64(*metric.Resource.Current.AverageUtilization)
				}
				status.CurrentMetrics = append(status.CurrentMetrics, metricStatus)
			}

		case autoscalingv2.PodsMetricSourceType:
			if metric.Pods != nil {
				metricStatus := MetricStatus{
					Type:         "Pods",
					Name:         metric.Pods.Metric.Name,
					CurrentValue: metric.Pods.Current.AverageValue.Value(),
				}
				status.CurrentMetrics = append(status.CurrentMetrics, metricStatus)
			}
		}
	}

	// Check for scaling limitations
	for _, condition := range hpa.Status.Conditions {
		if condition.Type == autoscalingv2.AbleToScale && condition.Status == corev1.ConditionFalse {
			status.ScalingLimitedReasons = append(status.ScalingLimitedReasons, condition.Reason)
		}
	}

	return status, nil
}

// AutoscalingStatus represents current autoscaling state
type AutoscalingStatus struct {
	// Enabled whether autoscaling is active
	Enabled bool

	// CurrentReplicas current number of replicas
	CurrentReplicas int32

	// DesiredReplicas desired number of replicas
	DesiredReplicas int32

	// MinReplicas minimum replicas configured
	MinReplicas int32

	// MaxReplicas maximum replicas configured
	MaxReplicas int32

	// LastScaleTime when last scale occurred
	LastScaleTime *metav1.Time

	// CurrentMetrics current metric values
	CurrentMetrics []MetricStatus

	// ScalingLimitedReasons reasons if scaling is blocked
	ScalingLimitedReasons []string
}

// MetricStatus represents a single metric's current value
type MetricStatus struct {
	Type         string
	Name         string
	CurrentValue int64
	TargetValue  int64
}

// Helper functions
func parseInt32(s string) (int32, error) {
	var i int32
	_, err := fmt.Sscanf(s, "%d", &i)
	return i, err
}

func parseInt64(s string) (int64, error) {
	var i int64
	_, err := fmt.Sscanf(s, "%d", &i)
	return i, err
}

func int32Ptr(i int32) *int32 {
	return &i
}

// GenerateAutoscalingReport creates a report of autoscaling behavior
func GenerateAutoscalingReport(status *AutoscalingStatus, config *AutoscalingConfig) string {
	if !status.Enabled {
		return "Autoscaling is disabled for this cluster."
	}

	report := fmt.Sprintf(`
Autoscaling Status Report
=========================

Current State:
  Current Replicas: %d
  Desired Replicas: %d
  Min Replicas: %d
  Max Replicas: %d

`,
		status.CurrentReplicas,
		status.DesiredReplicas,
		status.MinReplicas,
		status.MaxReplicas,
	)

	if status.LastScaleTime != nil {
		timeSinceScale := time.Since(status.LastScaleTime.Time)
		report += fmt.Sprintf("  Last Scale: %s ago\n\n", timeSinceScale.Round(time.Second))
	}

	report += "Current Metrics:\n"
	for _, metric := range status.CurrentMetrics {
		report += fmt.Sprintf("  %s (%s): %d\n", metric.Name, metric.Type, metric.CurrentValue)
	}

	if len(status.ScalingLimitedReasons) > 0 {
		report += "\nScaling Limitations:\n"
		for _, reason := range status.ScalingLimitedReasons {
			report += fmt.Sprintf("  - %s\n", reason)
		}
	}

	// Provide recommendations
	report += "\nRecommendations:\n"

	if status.CurrentReplicas == status.MaxReplicas {
		report += "  ⚠ At maximum replica count. Consider increasing max_replicas if load is high.\n"
	}

	if status.CurrentReplicas == status.MinReplicas && status.DesiredReplicas > status.CurrentReplicas {
		report += "  ⚠ Desired replicas exceeds current. Scaling up...\n"
	}

	if status.CurrentReplicas > status.DesiredReplicas {
		report += "  ℹ Scaling down to match decreased load.\n"
	}

	return report
}
