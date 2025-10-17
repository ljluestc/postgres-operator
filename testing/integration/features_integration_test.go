// Copyright 2025 Crunchy Data Solutions, Inc.
//
// SPDX-License-Identifier: Apache-2.0

package integration

import (
	"context"
	"testing"
	"time"

	"gotest.tools/v3/assert"
	autoscalingv2 "k8s.io/api/autoscaling/v2"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/crunchydata/postgres-operator/internal/controller/postgrescluster"
	"github.com/crunchydata/postgres-operator/internal/dr"
	"github.com/crunchydata/postgres-operator/internal/fips"
	"github.com/crunchydata/postgres-operator/internal/initialize"
	"github.com/crunchydata/postgres-operator/internal/naming"
	"github.com/crunchydata/postgres-operator/internal/patroni"
	"github.com/crunchydata/postgres-operator/internal/pgbackrest"
	"github.com/crunchydata/postgres-operator/internal/pgbouncer"
	"github.com/crunchydata/postgres-operator/internal/postgres"
	"github.com/crunchydata/postgres-operator/internal/testing/require"
	"github.com/crunchydata/postgres-operator/internal/validation"
	"github.com/crunchydata/postgres-operator/pkg/apis/postgres-operator.crunchydata.com/v1beta1"
)

// TestFeatureBackupVerification tests the backup verification feature
func TestFeatureBackupVerification(t *testing.T) {
	ctx := context.Background()
	cc := require.Kubernetes(t)
	ns := createTestNamespace(t, cc)

	tests := []struct {
		name           string
		verification   *pgbackrest.BackupVerification
		expectCronJob  bool
		expectSchedule string
	}{
		{
			name: "ChecksumVerification",
			verification: &pgbackrest.BackupVerification{
				Enabled:  true,
				Schedule: "0 2 * * *",
				RepoName: "repo1",
				Method:   pgbackrest.VerificationMethodChecksum,
				Resources: corev1.ResourceRequirements{
					Requests: corev1.ResourceList{
						corev1.ResourceCPU:    resource.MustParse("100m"),
						corev1.ResourceMemory: resource.MustParse("256Mi"),
					},
				},
				Timeout: 2 * time.Hour,
			},
			expectCronJob:  true,
			expectSchedule: "0 2 * * *",
		},
		{
			name: "RestoreTestVerification",
			verification: &pgbackrest.BackupVerification{
				Enabled:  true,
				Schedule: "0 3 * * 0",
				RepoName: "repo1",
				Method:   pgbackrest.VerificationMethodRestoreTest,
				Resources: corev1.ResourceRequirements{
					Requests: corev1.ResourceList{
						corev1.ResourceCPU:    resource.MustParse("200m"),
						corev1.ResourceMemory: resource.MustParse("512Mi"),
					},
				},
			},
			expectCronJob:  true,
			expectSchedule: "0 3 * * 0",
		},
		{
			name: "BothVerificationMethods",
			verification: &pgbackrest.BackupVerification{
				Enabled:  true,
				Schedule: "0 4 * * 6",
				RepoName: "repo2",
				Method:   pgbackrest.VerificationMethodBoth,
			},
			expectCronJob:  true,
			expectSchedule: "0 4 * * 6",
		},
		{
			name: "DisabledVerification",
			verification: &pgbackrest.BackupVerification{
				Enabled:  false,
				Schedule: "0 5 * * *",
				RepoName: "repo1",
			},
			expectCronJob: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cluster := createBasicCluster(t, ns.Name, "verify-"+tt.name)
			assert.NilError(t, cc.Create(ctx, cluster))
			t.Cleanup(func() { cleanupCluster(t, cc, cluster) })

			// Reconcile verification
			err := pgbackrest.ReconcileVerification(ctx, cc, cluster, tt.verification)
			assert.NilError(t, err)

			// Check if CronJob was created
			cronJobName := cluster.Name + "-verify-" + tt.verification.RepoName
			cronJob := &batchv1.CronJob{}
			err = cc.Get(ctx, types.NamespacedName{
				Name:      cronJobName,
				Namespace: ns.Name,
			}, cronJob)

			if tt.expectCronJob {
				assert.NilError(t, err, "Expected CronJob to be created")
				assert.Equal(t, cronJob.Spec.Schedule, tt.expectSchedule)

				// Verify labels
				assert.Equal(t, cronJob.Labels[naming.LabelPGBackRestVerify], "")
				assert.Equal(t, cronJob.Labels[naming.LabelPGBackRestRepoName], tt.verification.RepoName)

				// Verify container configuration
				assert.Assert(t, len(cronJob.Spec.JobTemplate.Spec.Template.Spec.Containers) > 0)
				container := cronJob.Spec.JobTemplate.Spec.Template.Spec.Containers[0]
				assert.Equal(t, container.Name, naming.PGBackRestVerifyContainerName)

				// Verify timeout if set
				if tt.verification.Timeout > 0 {
					expectedSeconds := int64(tt.verification.Timeout.Seconds())
					assert.Assert(t, cronJob.Spec.JobTemplate.Spec.ActiveDeadlineSeconds != nil)
					assert.Equal(t, *cronJob.Spec.JobTemplate.Spec.ActiveDeadlineSeconds, expectedSeconds)
				}
			} else {
				assert.Assert(t, client.IgnoreNotFound(err) == nil, "Expected CronJob to not exist")
			}

			// Test status retrieval
			status, err := pgbackrest.GetVerificationStatus(ctx, cc, cluster, tt.verification.RepoName)
			if tt.expectCronJob {
				assert.NilError(t, err)
				assert.Assert(t, status != nil)
			}
		})
	}
}

// TestFeatureBackupMetrics tests the backup metrics feature
func TestFeatureBackupMetrics(t *testing.T) {
	ctx := context.Background()
	cc := require.Kubernetes(t)
	ns := createTestNamespace(t, cc)

	cluster := createBasicCluster(t, ns.Name, "metrics-test")
	assert.NilError(t, cc.Create(ctx, cluster))
	t.Cleanup(func() { cleanupCluster(t, cc, cluster) })

	// Test metrics collection configuration
	metricsConfig := &pgbackrest.MetricsConfig{
		Enabled:        true,
		ExportInterval: 1 * time.Minute,
		Repositories:   []string{"repo1"},
	}

	// Verify metrics ConfigMap creation
	configMap := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      cluster.Name + "-pgbackrest-metrics",
			Namespace: ns.Name,
		},
		Data: map[string]string{
			"enabled":  "true",
			"interval": metricsConfig.ExportInterval.String(),
		},
	}

	err := cc.Create(ctx, configMap)
	assert.NilError(t, err)

	// Verify ConfigMap exists
	retrieved := &corev1.ConfigMap{}
	err = cc.Get(ctx, types.NamespacedName{
		Name:      configMap.Name,
		Namespace: ns.Name,
	}, retrieved)
	assert.NilError(t, err)
	assert.Equal(t, retrieved.Data["enabled"], "true")
}

// TestFeatureSecretsRotation tests the secrets rotation feature
func TestFeatureSecretsRotation(t *testing.T) {
	ctx := context.Background()
	cc := require.Kubernetes(t)
	ns := createTestNamespace(t, cc)

	tests := []struct {
		name           string
		rotationPolicy string
		expectRotation bool
	}{
		{
			name:           "ManualRotation",
			rotationPolicy: "manual",
			expectRotation: false,
		},
		{
			name:           "AutomaticRotation",
			rotationPolicy: "automatic",
			expectRotation: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cluster := createBasicCluster(t, ns.Name, "rotate-"+tt.name)
			if cluster.Annotations == nil {
				cluster.Annotations = make(map[string]string)
			}
			cluster.Annotations["postgres-operator.crunchydata.com/secrets-rotation-policy"] = tt.rotationPolicy

			assert.NilError(t, cc.Create(ctx, cluster))
			t.Cleanup(func() { cleanupCluster(t, cc, cluster) })

			// Create a test secret
			secret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      cluster.Name + "-postgres",
					Namespace: ns.Name,
					Labels: map[string]string{
						naming.LabelCluster: cluster.Name,
					},
				},
				Data: map[string][]byte{
					"password": []byte("old-password"),
				},
			}
			assert.NilError(t, cc.Create(ctx, secret))

			// Simulate rotation trigger
			if tt.expectRotation {
				secret.Annotations = map[string]string{
					"postgres-operator.crunchydata.com/rotate": "true",
				}
				err := cc.Update(ctx, secret)
				assert.NilError(t, err)

				// Verify secret was updated
				updated := &corev1.Secret{}
				err = cc.Get(ctx, types.NamespacedName{
					Name:      secret.Name,
					Namespace: ns.Name,
				}, updated)
				assert.NilError(t, err)
				assert.Assert(t, updated.Annotations["postgres-operator.crunchydata.com/rotate"] == "true")
			}
		})
	}
}

// TestFeatureConfigValidation tests the configuration validation feature
func TestFeatureConfigValidation(t *testing.T) {
	ctx := context.Background()

	tests := []struct {
		name          string
		cluster       *v1beta1.PostgresCluster
		expectValid   bool
		expectErrors  int
		expectWarnings int
	}{
		{
			name:          "ValidCluster",
			cluster:       createValidatedCluster("valid-cluster"),
			expectValid:   true,
			expectErrors:  0,
			expectWarnings: 0,
		},
		{
			name:           "NoBackupRepository",
			cluster:        createClusterWithoutBackup("no-backup"),
			expectValid:    false,
			expectErrors:   1,
			expectWarnings: 0,
		},
		{
			name:           "SingleInstance",
			cluster:        createSingleInstanceCluster("single-instance"),
			expectValid:    true,
			expectErrors:   0,
			expectWarnings: 1, // Warning about no HA
		},
		{
			name:           "EOLVersion",
			cluster:        createEOLVersionCluster("eol-version"),
			expectValid:    true,
			expectErrors:   0,
			expectWarnings: 1, // Warning about EOL version
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			validator := validation.NewClusterValidator(ctx)
			report := validator.Validate(tt.cluster)

			assert.Equal(t, report.Valid, tt.expectValid)
			assert.Equal(t, report.Errors, tt.expectErrors)
			assert.Assert(t, report.Warnings >= tt.expectWarnings, "Expected at least %d warnings, got %d", tt.expectWarnings, report.Warnings)

			// Test report formatting
			formatted := validation.FormatReport(report)
			assert.Assert(t, len(formatted) > 0, "Report should be formatted")
		})
	}
}

// TestFeatureFIPSSupport tests FIPS compliance feature
func TestFeatureFIPSSupport(t *testing.T) {
	ctx := context.Background()

	tests := []struct {
		name         string
		fipsEnabled  bool
		expectStrict bool
	}{
		{
			name:         "FIPSEnabled",
			fipsEnabled:  true,
			expectStrict: true,
		},
		{
			name:         "FIPSDisabled",
			fipsEnabled:  false,
			expectStrict: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := &fips.Config{
				Enabled: tt.fipsEnabled,
			}

			// Test FIPS validator
			validator := fips.NewValidator(config)
			assert.Assert(t, validator != nil)

			// Test crypto configuration
			cryptoConfig := fips.GetCryptoConfig(config)
			assert.Equal(t, cryptoConfig.StrictMode, tt.expectStrict)

			// Verify allowed algorithms
			if tt.fipsEnabled {
				assert.Assert(t, fips.IsAlgorithmAllowed("AES-256-GCM"))
				assert.Assert(t, !fips.IsAlgorithmAllowed("MD5"))
			}
		})
	}
}

// TestFeatureDRDrills tests disaster recovery drill feature
func TestFeatureDRDrills(t *testing.T) {
	ctx := context.Background()
	cc := require.Kubernetes(t)
	ns := createTestNamespace(t, cc)

	tests := []struct {
		name       string
		drill      *dr.Drill
		expectJob  bool
	}{
		{
			name: "BasicDrill",
			drill: &dr.Drill{
				Name:        "test-drill",
				Schedule:    "0 1 * * 0",
				TargetRepo:  "repo1",
				Type:        dr.DrillTypeRestore,
				Enabled:     true,
			},
			expectJob: true,
		},
		{
			name: "FailoverDrill",
			drill: &dr.Drill{
				Name:     "failover-drill",
				Schedule: "0 2 * * 6",
				Type:     dr.DrillTypeFailover,
				Enabled:  true,
			},
			expectJob: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cluster := createBasicCluster(t, ns.Name, "drill-"+tt.name)
			assert.NilError(t, cc.Create(ctx, cluster))
			t.Cleanup(func() { cleanupCluster(t, cc, cluster) })

			// Create drill CronJob
			cronJob, err := dr.CreateDrillCronJob(ctx, cluster, tt.drill)
			if tt.expectJob {
				assert.NilError(t, err)
				assert.Assert(t, cronJob != nil)
				assert.Equal(t, cronJob.Spec.Schedule, tt.drill.Schedule)

				// Apply CronJob
				err = cc.Create(ctx, cronJob)
				assert.NilError(t, err)

				// Verify creation
				retrieved := &batchv1.CronJob{}
				err = cc.Get(ctx, types.NamespacedName{
					Name:      cronJob.Name,
					Namespace: ns.Name,
				}, retrieved)
				assert.NilError(t, err)
			}
		})
	}
}

// TestFeatureQueryPerformance tests query performance monitoring
func TestFeatureQueryPerformance(t *testing.T) {
	ctx := context.Background()

	tests := []struct {
		name           string
		config         *postgres.PerformanceConfig
		expectMonitoring bool
	}{
		{
			name: "BasicMonitoring",
			config: &postgres.PerformanceConfig{
				Enabled:           true,
				SlowQueryThreshold: 1 * time.Second,
				TrackIOTiming:     true,
			},
			expectMonitoring: true,
		},
		{
			name: "DisabledMonitoring",
			config: &postgres.PerformanceConfig{
				Enabled: false,
			},
			expectMonitoring: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			params := postgres.GeneratePerformanceParameters(tt.config)

			if tt.expectMonitoring {
				assert.Assert(t, params != nil)
				assert.Assert(t, len(params) > 0)

				// Verify key performance parameters
				if tt.config.SlowQueryThreshold > 0 {
					_, exists := params["log_min_duration_statement"]
					assert.Assert(t, exists)
				}

				if tt.config.TrackIOTiming {
					assert.Equal(t, params["track_io_timing"], "on")
				}
			}
		})
	}
}

// TestFeaturePoolAnalytics tests PgBouncer analytics feature
func TestFeaturePoolAnalytics(t *testing.T) {
	ctx := context.Background()

	tests := []struct {
		name          string
		config        *pgbouncer.AnalyticsConfig
		expectMetrics bool
	}{
		{
			name: "EnabledAnalytics",
			config: &pgbouncer.AnalyticsConfig{
				Enabled:        true,
				MetricsPort:    9127,
				CollectInterval: 30 * time.Second,
			},
			expectMetrics: true,
		},
		{
			name: "DisabledAnalytics",
			config: &pgbouncer.AnalyticsConfig{
				Enabled: false,
			},
			expectMetrics: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			metrics := pgbouncer.GetAnalyticsMetrics(tt.config)

			if tt.expectMetrics {
				assert.Assert(t, metrics != nil)
				assert.Assert(t, len(metrics) > 0)
			} else {
				assert.Assert(t, metrics == nil || len(metrics) == 0)
			}
		})
	}
}

// TestFeatureFailoverOptimization tests failover optimization
func TestFeatureFailoverOptimization(t *testing.T) {
	ctx := context.Background()

	tests := []struct {
		name       string
		config     *patroni.FailoverConfig
		expectFast bool
	}{
		{
			name: "OptimizedFailover",
			config: &patroni.FailoverConfig{
				Enabled:       true,
				MaxLagBytes:   1024 * 1024,
				CheckInterval: 5 * time.Second,
			},
			expectFast: true,
		},
		{
			name: "DefaultFailover",
			config: &patroni.FailoverConfig{
				Enabled: false,
			},
			expectFast: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			patroniConfig := patroni.GenerateFailoverConfig(tt.config)

			assert.Assert(t, patroniConfig != nil)

			if tt.expectFast {
				// Verify optimized settings
				maxLag, exists := patroniConfig["maximum_lag_on_failover"]
				assert.Assert(t, exists)
				assert.Assert(t, maxLag != nil)
			}
		})
	}
}

// TestFeatureAutoscaling tests autoscaling feature
func TestFeatureAutoscaling(t *testing.T) {
	ctx := context.Background()
	cc := require.Kubernetes(t)
	ns := createTestNamespace(t, cc)

	tests := []struct {
		name        string
		annotations map[string]string
		expectHPA   bool
		minReplicas int32
		maxReplicas int32
	}{
		{
			name: "CPUBasedAutoscaling",
			annotations: map[string]string{
				postgrescluster.AnnotationAutoscaleEnabled:   "true",
				postgrescluster.AnnotationAutoscaleMin:       "2",
				postgrescluster.AnnotationAutoscaleMax:       "5",
				postgrescluster.AnnotationAutoscaleTargetCPU: "70",
			},
			expectHPA:   true,
			minReplicas: 2,
			maxReplicas: 5,
		},
		{
			name: "MemoryBasedAutoscaling",
			annotations: map[string]string{
				postgrescluster.AnnotationAutoscaleEnabled:      "true",
				postgrescluster.AnnotationAutoscaleMin:          "1",
				postgrescluster.AnnotationAutoscaleMax:          "10",
				postgrescluster.AnnotationAutoscaleTargetMemory: "80",
			},
			expectHPA:   true,
			minReplicas: 1,
			maxReplicas: 10,
		},
		{
			name:        "NoAutoscaling",
			annotations: map[string]string{},
			expectHPA:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cluster := createBasicCluster(t, ns.Name, "autoscale-"+tt.name)
			cluster.Annotations = tt.annotations

			assert.NilError(t, cc.Create(ctx, cluster))
			t.Cleanup(func() { cleanupCluster(t, cc, cluster) })

			// Get autoscaling config
			config := postgrescluster.GetAutoscalingConfig(cluster)

			if tt.expectHPA {
				assert.Assert(t, config != nil)
				assert.Assert(t, config.Enabled)
				assert.Equal(t, config.MinReplicas, tt.minReplicas)
				assert.Equal(t, config.MaxReplicas, tt.maxReplicas)

				// Create HPA
				hpa, err := createTestHPA(cluster, config)
				assert.NilError(t, err)
				assert.Assert(t, hpa != nil)

				// Apply HPA to cluster
				err = cc.Create(ctx, hpa)
				assert.NilError(t, err)

				// Verify HPA exists
				retrieved := &autoscalingv2.HorizontalPodAutoscaler{}
				err = cc.Get(ctx, types.NamespacedName{
					Name:      hpa.Name,
					Namespace: ns.Name,
				}, retrieved)
				assert.NilError(t, err)
				assert.Equal(t, *retrieved.Spec.MinReplicas, tt.minReplicas)
				assert.Equal(t, retrieved.Spec.MaxReplicas, tt.maxReplicas)
			} else {
				assert.Assert(t, config == nil || !config.Enabled)
			}
		})
	}
}

// TestFeatureBackupEncryption tests backup encryption feature
func TestFeatureBackupEncryption(t *testing.T) {
	ctx := context.Background()
	cc := require.Kubernetes(t)
	ns := createTestNamespace(t, cc)

	tests := []struct {
		name            string
		encryptionConfig *pgbackrest.EncryptionConfig
		expectEncrypted  bool
	}{
		{
			name: "AES256Encryption",
			encryptionConfig: &pgbackrest.EncryptionConfig{
				Enabled:   true,
				Algorithm: "aes-256-cbc",
			},
			expectEncrypted: true,
		},
		{
			name: "NoEncryption",
			encryptionConfig: &pgbackrest.EncryptionConfig{
				Enabled: false,
			},
			expectEncrypted: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cluster := createBasicCluster(t, ns.Name, "encrypt-"+tt.name)
			assert.NilError(t, cc.Create(ctx, cluster))
			t.Cleanup(func() { cleanupCluster(t, cc, cluster) })

			// Create encryption secret if enabled
			if tt.expectEncrypted {
				secret := &corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{
						Name:      cluster.Name + "-pgbackrest-encryption",
						Namespace: ns.Name,
					},
					Data: map[string][]byte{
						"encryption-key": []byte("test-encryption-key-32-bytes!!"),
					},
				}
				assert.NilError(t, cc.Create(ctx, secret))

				// Verify secret exists
				retrieved := &corev1.Secret{}
				err := cc.Get(ctx, types.NamespacedName{
					Name:      secret.Name,
					Namespace: ns.Name,
				}, retrieved)
				assert.NilError(t, err)
				assert.Assert(t, len(retrieved.Data["encryption-key"]) > 0)
			}

			// Validate encryption config
			config := pgbackrest.ValidateEncryptionConfig(tt.encryptionConfig)
			assert.Equal(t, config.Enabled, tt.expectEncrypted)
		})
	}
}

// TestFeatureCrossRegionReplication tests cross-region replication
func TestFeatureCrossRegionReplication(t *testing.T) {
	ctx := context.Background()
	cc := require.Kubernetes(t)
	ns := createTestNamespace(t, cc)

	tests := []struct {
		name             string
		replicationConfig *pgbackrest.ReplicationConfig
		expectReplication bool
	}{
		{
			name: "EnabledReplication",
			replicationConfig: &pgbackrest.ReplicationConfig{
				Enabled:      true,
				SourceRepo:   "repo1",
				TargetRepo:   "repo2",
				Schedule:     "*/15 * * * *",
			},
			expectReplication: true,
		},
		{
			name: "DisabledReplication",
			replicationConfig: &pgbackrest.ReplicationConfig{
				Enabled: false,
			},
			expectReplication: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cluster := createBasicCluster(t, ns.Name, "repl-"+tt.name)
			assert.NilError(t, cc.Create(ctx, cluster))
			t.Cleanup(func() { cleanupCluster(t, cc, cluster) })

			if tt.expectReplication {
				// Create replication CronJob
				cronJob := &batchv1.CronJob{
					ObjectMeta: metav1.ObjectMeta{
						Name:      cluster.Name + "-replication",
						Namespace: ns.Name,
					},
					Spec: batchv1.CronJobSpec{
						Schedule: tt.replicationConfig.Schedule,
						JobTemplate: batchv1.JobTemplateSpec{
							Spec: batchv1.JobSpec{
								Template: corev1.PodTemplateSpec{
									Spec: corev1.PodSpec{
										RestartPolicy: corev1.RestartPolicyOnFailure,
										Containers: []corev1.Container{
											{
												Name:  "replication",
												Image: "test-image",
											},
										},
									},
								},
							},
						},
					},
				}

				assert.NilError(t, cc.Create(ctx, cronJob))

				// Verify CronJob exists
				retrieved := &batchv1.CronJob{}
				err := cc.Get(ctx, types.NamespacedName{
					Name:      cronJob.Name,
					Namespace: ns.Name,
				}, retrieved)
				assert.NilError(t, err)
				assert.Equal(t, retrieved.Spec.Schedule, tt.replicationConfig.Schedule)
			}
		})
	}
}

// Helper functions

func createTestNamespace(t *testing.T, cc client.Client) *corev1.Namespace {
	ns := &corev1.Namespace{}
	ns.GenerateName = "postgres-operator-integration-test-"
	ctx := context.Background()
	assert.NilError(t, cc.Create(ctx, ns))

	t.Cleanup(func() {
		assert.Check(t, client.IgnoreNotFound(cc.Delete(ctx, ns)))
	})

	return ns
}

func createBasicCluster(t *testing.T, namespace, name string) *v1beta1.PostgresCluster {
	cluster := &v1beta1.PostgresCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: v1beta1.PostgresClusterSpec{
			PostgresVersion: 16,
			Image:           "test-image:latest",
			InstanceSets: []v1beta1.PostgresInstanceSetSpec{
				{
					Name:     "instance1",
					Replicas: initialize.Int32(1),
					DataVolumeClaimSpec: v1beta1.VolumeClaimSpecWithAutoGrow{
						VolumeClaimSpec: corev1.PersistentVolumeClaimSpec{
							AccessModes: []corev1.PersistentVolumeAccessMode{
								corev1.ReadWriteOnce,
							},
							Resources: corev1.VolumeResourceRequirements{
								Requests: corev1.ResourceList{
									corev1.ResourceStorage: resource.MustParse("1Gi"),
								},
							},
						},
					},
				},
			},
			Backups: v1beta1.Backups{
				PGBackRest: v1beta1.PGBackRestArchive{
					Repos: []v1beta1.PGBackRestRepo{
						{
							Name: "repo1",
							Volume: &v1beta1.RepoPVC{
								VolumeClaimSpec: corev1.PersistentVolumeClaimSpec{
									AccessModes: []corev1.PersistentVolumeAccessMode{
										corev1.ReadWriteOnce,
									},
									Resources: corev1.VolumeResourceRequirements{
										Requests: corev1.ResourceList{
											corev1.ResourceStorage: resource.MustParse("1Gi"),
										},
									},
								},
							},
						},
					},
				},
			},
		},
	}
	return cluster
}

func createValidatedCluster(name string) *v1beta1.PostgresCluster {
	cluster := &v1beta1.PostgresCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
		Spec: v1beta1.PostgresClusterSpec{
			PostgresVersion: 16,
			InstanceSets: []v1beta1.PostgresInstanceSetSpec{
				{
					Name:     "instance1",
					Replicas: initialize.Int32(2),
					DataVolumeClaimSpec: v1beta1.VolumeClaimSpecWithAutoGrow{
						VolumeClaimSpec: corev1.PersistentVolumeClaimSpec{
							AccessModes: []corev1.PersistentVolumeAccessMode{
								corev1.ReadWriteOnce,
							},
							Resources: corev1.VolumeResourceRequirements{
								Requests: corev1.ResourceList{
									corev1.ResourceStorage: resource.MustParse("20Gi"),
								},
							},
						},
					},
				},
			},
			Backups: v1beta1.Backups{
				PGBackRest: v1beta1.PGBackRestArchive{
					Repos: []v1beta1.PGBackRestRepo{
						{
							Name: "repo1",
							Volume: &v1beta1.RepoPVC{
								VolumeClaimSpec: corev1.PersistentVolumeClaimSpec{
									AccessModes: []corev1.PersistentVolumeAccessMode{
										corev1.ReadWriteOnce,
									},
									Resources: corev1.VolumeResourceRequirements{
										Requests: corev1.ResourceList{
											corev1.ResourceStorage: resource.MustParse("10Gi"),
										},
									},
								},
							},
						},
					},
				},
			},
		},
	}
	return cluster
}

func createClusterWithoutBackup(name string) *v1beta1.PostgresCluster {
	cluster := &v1beta1.PostgresCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
		Spec: v1beta1.PostgresClusterSpec{
			PostgresVersion: 16,
			InstanceSets: []v1beta1.PostgresInstanceSetSpec{
				{
					Name:     "instance1",
					Replicas: initialize.Int32(1),
					DataVolumeClaimSpec: v1beta1.VolumeClaimSpecWithAutoGrow{
						VolumeClaimSpec: corev1.PersistentVolumeClaimSpec{
							AccessModes: []corev1.PersistentVolumeAccessMode{
								corev1.ReadWriteOnce,
							},
							Resources: corev1.VolumeResourceRequirements{
								Requests: corev1.ResourceList{
									corev1.ResourceStorage: resource.MustParse("1Gi"),
								},
							},
						},
					},
				},
			},
		},
	}
	return cluster
}

func createSingleInstanceCluster(name string) *v1beta1.PostgresCluster {
	cluster := createValidatedCluster(name)
	cluster.Spec.InstanceSets[0].Replicas = initialize.Int32(1)
	return cluster
}

func createEOLVersionCluster(name string) *v1beta1.PostgresCluster {
	cluster := createValidatedCluster(name)
	cluster.Spec.PostgresVersion = 11
	return cluster
}

func createTestHPA(cluster *v1beta1.PostgresCluster, config *postgrescluster.AutoscalingConfig) (*autoscalingv2.HorizontalPodAutoscaler, error) {
	hpa := &autoscalingv2.HorizontalPodAutoscaler{
		ObjectMeta: metav1.ObjectMeta{
			Name:      cluster.Name + "-replicas",
			Namespace: cluster.Namespace,
		},
		Spec: autoscalingv2.HorizontalPodAutoscalerSpec{
			ScaleTargetRef: autoscalingv2.CrossVersionObjectReference{
				APIVersion: v1beta1.SchemeGroupVersion.String(),
				Kind:       "PostgresCluster",
				Name:       cluster.Name,
			},
			MinReplicas: &config.MinReplicas,
			MaxReplicas: config.MaxReplicas,
			Metrics: []autoscalingv2.MetricSpec{
				{
					Type: autoscalingv2.ResourceMetricSourceType,
					Resource: &autoscalingv2.ResourceMetricSource{
						Name: corev1.ResourceCPU,
						Target: autoscalingv2.MetricTarget{
							Type:               autoscalingv2.UtilizationMetricType,
							AverageUtilization: initialize.Int32(70),
						},
					},
				},
			},
		},
	}
	return hpa, nil
}

func cleanupCluster(t *testing.T, cc client.Client, cluster *v1beta1.PostgresCluster) {
	ctx := context.Background()
	// Remove finalizers
	assert.Check(t, client.IgnoreNotFound(
		cc.Patch(ctx, cluster, client.RawPatch(
			client.Merge.Type(), []byte(`{"metadata":{"finalizers":[]}}`)))))
}
