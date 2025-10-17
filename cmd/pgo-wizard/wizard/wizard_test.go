// Copyright 2025 Crunchy Data Solutions, Inc.
//
// SPDX-License-Identifier: Apache-2.0

package wizard

import (
	"testing"

	"gotest.tools/v3/assert"
)

func TestPresetConfig(t *testing.T) {
	t.Run("Development", func(t *testing.T) {
		preset := presets["development"]

		assert.Equal(t, preset.Name, "Development")
		assert.Equal(t, preset.Replicas, 1)
		assert.Assert(t, !preset.EnableHA)
		assert.Assert(t, !preset.EnableBackup)
		assert.Assert(t, !preset.EnableMonitoring)
	})

	t.Run("Staging", func(t *testing.T) {
		preset := presets["staging"]

		assert.Equal(t, preset.Name, "Staging")
		assert.Equal(t, preset.Replicas, 2)
		assert.Assert(t, preset.EnableHA)
		assert.Assert(t, preset.EnableBackup)
		assert.Assert(t, preset.EnableMonitoring)
		assert.Equal(t, preset.BackupSchedule, "0 2 * * *")
	})

	t.Run("Production", func(t *testing.T) {
		preset := presets["production"]

		assert.Equal(t, preset.Name, "Production")
		assert.Equal(t, preset.Replicas, 3)
		assert.Assert(t, preset.EnableHA)
		assert.Assert(t, preset.EnableBackup)
		assert.Assert(t, preset.EnableMonitoring)
		assert.Equal(t, preset.BackupSchedule, "0 */6 * * *")
	})
}

func TestApplyPreset(t *testing.T) {
	t.Run("ValidPreset", func(t *testing.T) {
		config := &ClusterConfig{}

		err := applyPreset(config, "development")
		assert.NilError(t, err)
		assert.Equal(t, config.Environment, "development")
		assert.Equal(t, config.Replicas, 1)
	})

	t.Run("InvalidPreset", func(t *testing.T) {
		config := &ClusterConfig{}

		err := applyPreset(config, "nonexistent")
		assert.Assert(t, err != nil)
	})

	t.Run("ProductionPreset", func(t *testing.T) {
		config := &ClusterConfig{}

		err := applyPreset(config, "production")
		assert.NilError(t, err)
		assert.Equal(t, config.Replicas, 3)
		assert.Assert(t, config.EnableHA)
		assert.Assert(t, config.EnableBackup)
	})
}

func TestSetDefaults(t *testing.T) {
	config := &ClusterConfig{}

	setDefaults(config)

	assert.Equal(t, config.Name, "postgres")
	assert.Equal(t, config.PostgresVersion, 16)
	assert.Equal(t, config.Environment, "development")
	assert.Equal(t, config.Replicas, 1)
	assert.Assert(t, !config.EnableHA)
	assert.Assert(t, config.EnableTLS)
}

func TestValidateClusterName(t *testing.T) {
	t.Run("ValidName", func(t *testing.T) {
		err := validateClusterName("my-cluster")
		assert.NilError(t, err)
	})

	t.Run("ValidNameWithNumbers", func(t *testing.T) {
		err := validateClusterName("cluster123")
		assert.NilError(t, err)
	})

	t.Run("InvalidUppercase", func(t *testing.T) {
		err := validateClusterName("MyCluster")
		assert.Assert(t, err != nil)
	})

	t.Run("InvalidUnderscore", func(t *testing.T) {
		err := validateClusterName("my_cluster")
		assert.Assert(t, err != nil)
	})

	t.Run("InvalidStartsWithHyphen", func(t *testing.T) {
		err := validateClusterName("-cluster")
		assert.Assert(t, err != nil)
	})

	t.Run("InvalidEndsWithHyphen", func(t *testing.T) {
		err := validateClusterName("cluster-")
		assert.Assert(t, err != nil)
	})

	t.Run("TooLong", func(t *testing.T) {
		longName := "this-is-a-very-long-cluster-name-that-exceeds-the-fifty-character-limit"
		err := validateClusterName(longName)
		assert.Assert(t, err != nil)
	})
}

func TestValidateResource(t *testing.T) {
	t.Run("ValidCPU", func(t *testing.T) {
		err := validateResource("500m")
		assert.NilError(t, err)
	})

	t.Run("ValidMemory", func(t *testing.T) {
		err := validateResource("2Gi")
		assert.NilError(t, err)
	})

	t.Run("ValidStorage", func(t *testing.T) {
		err := validateResource("100Gi")
		assert.NilError(t, err)
	})

	t.Run("InvalidResource", func(t *testing.T) {
		err := validateResource("invalid")
		assert.Assert(t, err != nil)
	})
}

func TestValidatePositiveInt(t *testing.T) {
	t.Run("ValidPositive", func(t *testing.T) {
		err := validatePositiveInt("5")
		assert.NilError(t, err)
	})

	t.Run("InvalidZero", func(t *testing.T) {
		err := validatePositiveInt("0")
		assert.Assert(t, err != nil)
	})

	t.Run("InvalidNegative", func(t *testing.T) {
		err := validatePositiveInt("-5")
		assert.Assert(t, err != nil)
	})

	t.Run("InvalidString", func(t *testing.T) {
		err := validatePositiveInt("abc")
		assert.Assert(t, err != nil)
	})
}

func TestExtractVersion(t *testing.T) {
	t.Run("Version16", func(t *testing.T) {
		version := extractVersion("16 (recommended)")
		assert.Equal(t, version, 16)
	})

	t.Run("Version17", func(t *testing.T) {
		version := extractVersion("17")
		assert.Equal(t, version, 17)
	})

	t.Run("Version18", func(t *testing.T) {
		version := extractVersion("18")
		assert.Equal(t, version, 18)
	})
}

func TestContains(t *testing.T) {
	t.Run("Found", func(t *testing.T) {
		slice := []string{"apple", "banana", "cherry"}
		result := contains(slice, "banana")
		assert.Assert(t, result)
	})

	t.Run("NotFound", func(t *testing.T) {
		slice := []string{"apple", "banana", "cherry"}
		result := contains(slice, "orange")
		assert.Assert(t, !result)
	})

	t.Run("EmptySlice", func(t *testing.T) {
		slice := []string{}
		result := contains(slice, "apple")
		assert.Assert(t, !result)
	})
}

func TestGetDefaultFeatures(t *testing.T) {
	t.Run("AllEnabled", func(t *testing.T) {
		config := &ClusterConfig{
			EnableHA:         true,
			EnableBackup:     true,
			EnableMonitoring: true,
			EnablePgBouncer:  true,
			EnableTLS:        true,
		}

		features := getDefaultFeatures(config)
		assert.Equal(t, len(features), 5)
	})

	t.Run("NoneEnabled", func(t *testing.T) {
		config := &ClusterConfig{}

		features := getDefaultFeatures(config)
		assert.Equal(t, len(features), 0)
	})

	t.Run("SomeEnabled", func(t *testing.T) {
		config := &ClusterConfig{
			EnableHA:     true,
			EnableBackup: true,
		}

		features := getDefaultFeatures(config)
		assert.Equal(t, len(features), 2)
	})
}

func TestGenerateClusterSpec(t *testing.T) {
	t.Run("BasicCluster", func(t *testing.T) {
		config := &ClusterConfig{
			Name:            "test-cluster",
			Namespace:       "test-ns",
			PostgresVersion: 16,
			Environment:     "development",
			Replicas:        1,
			CPURequest:      "500m",
			CPULimit:        "1000m",
			MemoryRequest:   "1Gi",
			MemoryLimit:     "2Gi",
			StorageSize:     "10Gi",
		}

		cluster, err := generateClusterSpec(config)
		assert.NilError(t, err)
		assert.Assert(t, cluster != nil)
		assert.Equal(t, cluster.Name, "test-cluster")
		assert.Equal(t, cluster.Namespace, "test-ns")
		assert.Equal(t, cluster.Spec.PostgresVersion, 16)
	})

	t.Run("WithBackups", func(t *testing.T) {
		config := &ClusterConfig{
			Name:            "test-cluster",
			Namespace:       "test-ns",
			PostgresVersion: 16,
			Replicas:        2,
			CPURequest:      "1000m",
			CPULimit:        "2000m",
			MemoryRequest:   "4Gi",
			MemoryLimit:     "8Gi",
			StorageSize:     "50Gi",
			EnableBackup:    true,
			BackupSchedule:  "0 2 * * *",
		}

		cluster, err := generateClusterSpec(config)
		assert.NilError(t, err)
		assert.Assert(t, cluster.Spec.Backups.PGBackRest.Repos != nil)
		assert.Assert(t, len(cluster.Spec.Backups.PGBackRest.Repos) > 0)
	})

	t.Run("WithMonitoring", func(t *testing.T) {
		config := &ClusterConfig{
			Name:             "test-cluster",
			Namespace:        "test-ns",
			PostgresVersion:  16,
			Replicas:         2,
			CPURequest:       "1000m",
			CPULimit:         "2000m",
			MemoryRequest:    "4Gi",
			MemoryLimit:      "8Gi",
			StorageSize:      "50Gi",
			EnableMonitoring: true,
		}

		cluster, err := generateClusterSpec(config)
		assert.NilError(t, err)
		assert.Assert(t, cluster.Spec.Monitoring != nil)
		assert.Assert(t, cluster.Spec.Monitoring.PGMonitor != nil)
	})

	t.Run("WithPgBouncer", func(t *testing.T) {
		config := &ClusterConfig{
			Name:            "test-cluster",
			Namespace:       "test-ns",
			PostgresVersion: 16,
			Replicas:        3,
			CPURequest:      "2000m",
			CPULimit:        "4000m",
			MemoryRequest:   "8Gi",
			MemoryLimit:     "16Gi",
			StorageSize:     "100Gi",
			EnablePgBouncer: true,
		}

		cluster, err := generateClusterSpec(config)
		assert.NilError(t, err)
		assert.Assert(t, cluster.Spec.Proxy != nil)
		assert.Assert(t, cluster.Spec.Proxy.PGBouncer != nil)
		assert.Equal(t, *cluster.Spec.Proxy.PGBouncer.Replicas, int32(2))
	})

	t.Run("ProductionCluster", func(t *testing.T) {
		config := &ClusterConfig{
			Name:             "prod-cluster",
			Namespace:        "production",
			PostgresVersion:  16,
			Environment:      "production",
			Replicas:         3,
			CPURequest:       "2000m",
			CPULimit:         "4000m",
			MemoryRequest:    "8Gi",
			MemoryLimit:      "16Gi",
			StorageSize:      "100Gi",
			EnableHA:         true,
			EnableBackup:     true,
			EnableMonitoring: true,
			EnablePgBouncer:  true,
			BackupSchedule:   "0 */6 * * *",
		}

		cluster, err := generateClusterSpec(config)
		assert.NilError(t, err)
		assert.Assert(t, cluster != nil)
	})
}

func TestClusterConfig(t *testing.T) {
	t.Run("DefaultConfig", func(t *testing.T) {
		config := &ClusterConfig{
			Name:          "test",
			Namespace:     "default",
			PostgresVersion: 16,
		}

		assert.Equal(t, config.Name, "test")
		assert.Equal(t, config.PostgresVersion, 16)
	})

	t.Run("WithFeatures", func(t *testing.T) {
		config := &ClusterConfig{
			EnableHA:         true,
			EnableBackup:     true,
			EnableMonitoring: true,
			EnablePgBouncer:  true,
			EnableTLS:        true,
		}

		assert.Assert(t, config.EnableHA)
		assert.Assert(t, config.EnableBackup)
		assert.Assert(t, config.EnableMonitoring)
		assert.Assert(t, config.EnablePgBouncer)
		assert.Assert(t, config.EnableTLS)
	})
}

func TestInt32Ptr(t *testing.T) {
	ptr := int32Ptr(42)
	assert.Assert(t, ptr != nil)
	assert.Equal(t, *ptr, int32(42))
}

func TestStrPtr(t *testing.T) {
	ptr := strPtr("test")
	assert.Assert(t, ptr != nil)
	assert.Equal(t, *ptr, "test")
}
