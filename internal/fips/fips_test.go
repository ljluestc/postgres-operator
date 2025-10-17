// Copyright 2025 Crunchy Data Solutions, Inc.
//
// SPDX-License-Identifier: Apache-2.0

package fips

import (
	"context"
	"os"
	"testing"

	"gotest.tools/v3/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"

	"github.com/crunchydata/postgres-operator/pkg/apis/postgres-operator.crunchydata.com/v1beta1"
)

func TestIsFIPSEnabled(t *testing.T) {
	t.Run("EnvironmentVariable", func(t *testing.T) {
		os.Setenv(EnvFIPSEnabled, "true")
		defer os.Unsetenv(EnvFIPSEnabled)

		enabled := IsFIPSEnabled()
		assert.Assert(t, enabled)
	})

	t.Run("NotEnabled", func(t *testing.T) {
		os.Unsetenv(EnvFIPSEnabled)
		// This test may vary based on the system
		_ = IsFIPSEnabled()
	})
}

func TestGetFIPSConfig(t *testing.T) {
	t.Run("AnnotationEnabled", func(t *testing.T) {
		cluster := &v1beta1.PostgresCluster{
			ObjectMeta: metav1.ObjectMeta{
				Annotations: map[string]string{
					AnnotationFIPSMode: FIPSModeEnabled,
				},
			},
		}

		config := GetFIPSConfig(cluster)
		assert.Assert(t, config != nil)
		assert.Assert(t, config.Enabled)
		assert.Assert(t, config.EnforceValidation)
	})

	t.Run("AnnotationDisabled", func(t *testing.T) {
		cluster := &v1beta1.PostgresCluster{
			ObjectMeta: metav1.ObjectMeta{
				Annotations: map[string]string{
					AnnotationFIPSMode: FIPSModeDisabled,
				},
			},
		}

		config := GetFIPSConfig(cluster)
		assert.Assert(t, config != nil)
		assert.Assert(t, !config.Enabled)
	})

	t.Run("AutoDetect", func(t *testing.T) {
		cluster := &v1beta1.PostgresCluster{}

		config := GetFIPSConfig(cluster)
		assert.Assert(t, config != nil)
		// Enabled state depends on environment
	})

	t.Run("ConfigurationValues", func(t *testing.T) {
		cluster := &v1beta1.PostgresCluster{
			ObjectMeta: metav1.ObjectMeta{
				Annotations: map[string]string{
					AnnotationFIPSMode: FIPSModeEnabled,
				},
			},
		}

		config := GetFIPSConfig(cluster)
		assert.Equal(t, config.PostgreSQLConfig.SSLCiphers, PostgreSQLFIPSSSLCiphers)
		assert.Equal(t, config.PostgreSQLConfig.SSLMinProtocolVersion, FIPSTLSMinVersion)
		assert.Equal(t, config.PostgreSQLConfig.PasswordEncryption, "scram-sha-256")
	})
}

func TestApplyFIPSConfiguration(t *testing.T) {
	ctx := context.Background()

	t.Run("FIPSDisabled", func(t *testing.T) {
		cluster := &v1beta1.PostgresCluster{}
		config := &FIPSConfig{Enabled: false}

		err := ApplyFIPSConfiguration(ctx, cluster, config)
		assert.NilError(t, err)
	})

	t.Run("FIPSEnabled", func(t *testing.T) {
		cluster := &v1beta1.PostgresCluster{
			Spec: v1beta1.PostgresClusterSpec{},
		}
		config := &FIPSConfig{
			Enabled: true,
			PostgreSQLConfig: PostgreSQLFIPSConfig{
				SSLCiphers:            PostgreSQLFIPSSSLCiphers,
				SSLMinProtocolVersion: FIPSTLSMinVersion,
				PasswordEncryption:    "scram-sha-256",
			},
		}

		err := ApplyFIPSConfiguration(ctx, cluster, config)
		assert.NilError(t, err)

		// Verify PostgreSQL parameters were set
		assert.Assert(t, cluster.Spec.Config != nil)
		assert.Equal(t, cluster.Spec.Config.Parameters["password_encryption"].StrVal, "scram-sha-256")
		assert.Equal(t, cluster.Spec.Config.Parameters["ssl"].StrVal, "on")
	})
}

func TestConfigureFIPSContainer(t *testing.T) {
	t.Run("FIPSDisabled", func(t *testing.T) {
		container := &corev1.Container{}
		config := &FIPSConfig{Enabled: false}

		err := ConfigureFIPSContainer(container, config)
		assert.NilError(t, err)
		assert.Equal(t, len(container.Env), 0)
	})

	t.Run("FIPSEnabled", func(t *testing.T) {
		container := &corev1.Container{}
		config := &FIPSConfig{
			Enabled: true,
			OpenSSLConfig: OpenSSLFIPSConfig{
				FIPSModulePath: "/usr/lib64/ossl-modules/fips.so",
				FIPSConfigPath: "/etc/pki/tls/openssl.cnf",
			},
		}

		err := ConfigureFIPSContainer(container, config)
		assert.NilError(t, err)

		// Verify environment variables
		assert.Assert(t, len(container.Env) >= 3)

		hasFIPSEnv := false
		hasOpenSSLConf := false
		hasOpenSSLFIPS := false

		for _, env := range container.Env {
			if env.Name == EnvFIPSEnabled && env.Value == "true" {
				hasFIPSEnv = true
			}
			if env.Name == "OPENSSL_CONF" {
				hasOpenSSLConf = true
			}
			if env.Name == "OPENSSL_FIPS" && env.Value == "1" {
				hasOpenSSLFIPS = true
			}
		}

		assert.Assert(t, hasFIPSEnv)
		assert.Assert(t, hasOpenSSLConf)
		assert.Assert(t, hasOpenSSLFIPS)
	})
}

func TestValidateFIPSCompliance(t *testing.T) {
	t.Run("FIPSDisabled", func(t *testing.T) {
		cluster := &v1beta1.PostgresCluster{}
		violations := ValidateFIPSCompliance(cluster)
		assert.Equal(t, len(violations), 0)
	})

	t.Run("CompliantConfiguration", func(t *testing.T) {
		cluster := &v1beta1.PostgresCluster{
			ObjectMeta: metav1.ObjectMeta{
				Annotations: map[string]string{
					AnnotationFIPSMode: FIPSModeEnabled,
				},
			},
			Spec: v1beta1.PostgresClusterSpec{
				PostgresVersion: 16,
				Image:          "registry.developers.crunchydata.com/crunchydata/crunchy-postgres:ubi8-16.6-0-fips",
				Config: &v1beta1.PostgresConfigSpec{
					Parameters: map[string]intstr.IntOrString{
						"password_encryption": intstr.FromString("scram-sha-256"),
						"ssl":                 intstr.FromString("on"),
					},
				},
			},
		}

		violations := ValidateFIPSCompliance(cluster)
		assert.Equal(t, len(violations), 0)
	})

	t.Run("NonCompliantPassword", func(t *testing.T) {
		cluster := &v1beta1.PostgresCluster{
			ObjectMeta: metav1.ObjectMeta{
				Annotations: map[string]string{
					AnnotationFIPSMode: FIPSModeEnabled,
				},
			},
			Spec: v1beta1.PostgresClusterSpec{
				PostgresVersion: 16,
				Config: &v1beta1.PostgresConfigSpec{
					Parameters: map[string]intstr.IntOrString{
						"password_encryption": intstr.FromString("md5"),
					},
				},
			},
		}

		violations := ValidateFIPSCompliance(cluster)
		assert.Assert(t, len(violations) > 0)
		hasPasswordViolation := false
		for _, v := range violations {
			if v == "MD5 password encryption is not FIPS compliant" {
				hasPasswordViolation = true
			}
		}
		assert.Assert(t, hasPasswordViolation)
	})

	t.Run("OldPostgreSQLVersion", func(t *testing.T) {
		cluster := &v1beta1.PostgresCluster{
			ObjectMeta: metav1.ObjectMeta{
				Annotations: map[string]string{
					AnnotationFIPSMode: FIPSModeEnabled,
				},
			},
			Spec: v1beta1.PostgresClusterSpec{
				PostgresVersion: 11,
			},
		}

		violations := ValidateFIPSCompliance(cluster)
		hasVersionViolation := false
		for _, v := range violations {
			if v == "PostgreSQL 13 or higher recommended for full FIPS support" {
				hasVersionViolation = true
			}
		}
		assert.Assert(t, hasVersionViolation)
	})

	t.Run("NonFIPSImage", func(t *testing.T) {
		cluster := &v1beta1.PostgresCluster{
			ObjectMeta: metav1.ObjectMeta{
				Annotations: map[string]string{
					AnnotationFIPSMode: FIPSModeEnabled,
				},
			},
			Spec: v1beta1.PostgresClusterSpec{
				PostgresVersion: 16,
				Image:          "postgres:16",
			},
		}

		violations := ValidateFIPSCompliance(cluster)
		hasImageViolation := false
		for _, v := range violations {
			if v == "Image should be FIPS-validated build (look for 'fips' in image name)" {
				hasImageViolation = true
			}
		}
		assert.Assert(t, hasImageViolation)
	})
}

func TestCreateFIPSConfigMap(t *testing.T) {
	t.Run("FIPSDisabled", func(t *testing.T) {
		cluster := &v1beta1.PostgresCluster{}
		config := &FIPSConfig{Enabled: false}

		cm, err := CreateFIPSConfigMap(cluster, config)
		assert.NilError(t, err)
		assert.Assert(t, cm == nil)
	})

	t.Run("FIPSEnabled", func(t *testing.T) {
		cluster := &v1beta1.PostgresCluster{}
		config := &FIPSConfig{Enabled: true}

		cm, err := CreateFIPSConfigMap(cluster, config)
		assert.NilError(t, err)
		assert.Assert(t, cm != nil)
		assert.Assert(t, cm.Data != nil)
		assert.Assert(t, cm.Data["openssl.cnf"] != "")
		assert.Assert(t, cm.Data["pg_hba_fips.conf"] != "")
	})
}

func TestGenerateFIPSReport(t *testing.T) {
	t.Run("FIPSDisabled", func(t *testing.T) {
		cluster := &v1beta1.PostgresCluster{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-cluster",
				Namespace: "test-namespace",
			},
		}

		report := GenerateFIPSReport(cluster)
		assert.Assert(t, report != "")
		assert.Assert(t, len(report) > 0)
	})

	t.Run("FIPSEnabled", func(t *testing.T) {
		cluster := &v1beta1.PostgresCluster{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-cluster",
				Namespace: "test-namespace",
				Annotations: map[string]string{
					AnnotationFIPSMode: FIPSModeEnabled,
				},
			},
			Spec: v1beta1.PostgresClusterSpec{
				PostgresVersion: 16,
				Image:          "registry.developers.crunchydata.com/crunchydata/crunchy-postgres:ubi8-16.6-0-fips",
				Config: &v1beta1.PostgresConfigSpec{
					Parameters: map[string]intstr.IntOrString{
						"password_encryption": intstr.FromString("scram-sha-256"),
						"ssl":                 intstr.FromString("on"),
					},
				},
			},
		}

		report := GenerateFIPSReport(cluster)
		assert.Assert(t, report != "")
		assert.Assert(t, len(report) > 0)
	})
}

func TestGetFIPSDocumentation(t *testing.T) {
	docs := GetFIPSDocumentation()
	assert.Assert(t, docs != "")
	assert.Assert(t, len(docs) > 100)
}

func TestFIPSConstants(t *testing.T) {
	assert.Equal(t, FIPSModeEnabled, "enabled")
	assert.Equal(t, FIPSModeDisabled, "disabled")
	assert.Equal(t, AnnotationFIPSMode, "postgres-operator.crunchydata.com/fips-mode")
	assert.Equal(t, EnvFIPSEnabled, "FIPS_ENABLED")
	assert.Equal(t, FIPSTLSMinVersion, "TLSv1.2")
}

func TestFIPSConfig(t *testing.T) {
	config := FIPSConfig{
		Enabled:           true,
		EnforceValidation: true,
		PostgreSQLConfig: PostgreSQLFIPSConfig{
			SSLCiphers:            PostgreSQLFIPSSSLCiphers,
			SSLMinProtocolVersion: FIPSTLSMinVersion,
			PasswordEncryption:    "scram-sha-256",
		},
		OpenSSLConfig: OpenSSLFIPSConfig{
			FIPSModuleLoaded: true,
			FIPSModulePath:   "/usr/lib64/ossl-modules/fips.so",
			FIPSConfigPath:   "/etc/pki/tls/openssl.cnf",
		},
	}

	assert.Assert(t, config.Enabled)
	assert.Assert(t, config.EnforceValidation)
	assert.Equal(t, config.PostgreSQLConfig.PasswordEncryption, "scram-sha-256")
	assert.Assert(t, config.OpenSSLConfig.FIPSModuleLoaded)
}
