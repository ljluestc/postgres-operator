// Copyright 2025 Crunchy Data Solutions, Inc.
//
// SPDX-License-Identifier: Apache-2.0

package pgbackrest

import (
	"context"
	"testing"

	"gotest.tools/v3/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/crunchydata/postgres-operator/pkg/apis/postgres-operator.crunchydata.com/v1beta1"
)

func TestGetEncryptionConfig(t *testing.T) {
	t.Run("NoAnnotations", func(t *testing.T) {
		cluster := &v1beta1.PostgresCluster{
			ObjectMeta: metav1.ObjectMeta{
				Annotations: nil,
			},
		}

		config := GetEncryptionConfig(cluster)
		assert.Assert(t, config == nil)
	})

	t.Run("DisabledEncryption", func(t *testing.T) {
		cluster := &v1beta1.PostgresCluster{
			ObjectMeta: metav1.ObjectMeta{
				Annotations: map[string]string{
					AnnotationEncryptionEnabled: "false",
				},
			},
		}

		config := GetEncryptionConfig(cluster)
		assert.Assert(t, config == nil)
	})

	t.Run("EnabledWithDefaults", func(t *testing.T) {
		cluster := &v1beta1.PostgresCluster{
			ObjectMeta: metav1.ObjectMeta{
				Annotations: map[string]string{
					AnnotationEncryptionEnabled: "true",
				},
			},
		}

		config := GetEncryptionConfig(cluster)
		assert.Assert(t, config != nil)
		assert.Assert(t, config.Enabled)
		assert.Equal(t, config.Type, DefaultEncryptionType)
		assert.Equal(t, config.Provider, EncryptionProviderSecret)
	})

	t.Run("WithCustomType", func(t *testing.T) {
		cluster := &v1beta1.PostgresCluster{
			ObjectMeta: metav1.ObjectMeta{
				Annotations: map[string]string{
					AnnotationEncryptionEnabled: "true",
					AnnotationEncryptionType:    EncryptionTypeAES256,
				},
			},
		}

		config := GetEncryptionConfig(cluster)
		assert.Equal(t, config.Type, EncryptionTypeAES256)
	})

	t.Run("WithKMSProvider", func(t *testing.T) {
		cluster := &v1beta1.PostgresCluster{
			ObjectMeta: metav1.ObjectMeta{
				Annotations: map[string]string{
					AnnotationEncryptionEnabled:  "true",
					AnnotationEncryptionProvider: EncryptionProviderKMS,
					AnnotationEncryptionKeyID:    "key-12345",
				},
			},
		}

		config := GetEncryptionConfig(cluster)
		assert.Equal(t, config.Provider, EncryptionProviderKMS)
		assert.Equal(t, config.KeyID, "key-12345")
	})

	t.Run("WithVaultProvider", func(t *testing.T) {
		cluster := &v1beta1.PostgresCluster{
			ObjectMeta: metav1.ObjectMeta{
				Annotations: map[string]string{
					AnnotationEncryptionEnabled:  "true",
					AnnotationEncryptionProvider: EncryptionProviderVault,
				},
			},
		}

		config := GetEncryptionConfig(cluster)
		assert.Equal(t, config.Provider, EncryptionProviderVault)
	})
}

func TestGenerateEncryptionKey(t *testing.T) {
	t.Run("Generate64ByteKey", func(t *testing.T) {
		key, err := generateEncryptionKey(64)
		assert.NilError(t, err)
		assert.Assert(t, len(key) > 0)
	})

	t.Run("Generate32ByteKey", func(t *testing.T) {
		key, err := generateEncryptionKey(32)
		assert.NilError(t, err)
		assert.Assert(t, len(key) > 0)
	})
}

func TestGetKMSKey(t *testing.T) {
	ctx := context.Background()

	t.Run("NilConfig", func(t *testing.T) {
		_, err := getKMSKey(ctx, nil)
		assert.Assert(t, err != nil)
	})

	t.Run("AWSProvider", func(t *testing.T) {
		config := &KMSConfig{
			Provider:  "aws",
			AWSKeyID:  "key-123",
			AWSRegion: "us-east-1",
		}

		key, err := getKMSKey(ctx, config)
		assert.NilError(t, err)
		assert.Assert(t, len(key) > 0)
	})

	t.Run("GCPProvider", func(t *testing.T) {
		config := &KMSConfig{
			Provider:     "gcp",
			GCPProject:   "my-project",
			GCPLocation:  "us-central1",
			GCPCryptoKey: "my-key",
		}

		key, err := getKMSKey(ctx, config)
		assert.NilError(t, err)
		assert.Assert(t, len(key) > 0)
	})

	t.Run("AzureProvider", func(t *testing.T) {
		config := &KMSConfig{
			Provider:      "azure",
			AzureVaultURL: "https://myvault.vault.azure.net",
			AzureKeyName:  "my-key",
		}

		key, err := getKMSKey(ctx, config)
		assert.NilError(t, err)
		assert.Assert(t, len(key) > 0)
	})

	t.Run("UnknownProvider", func(t *testing.T) {
		config := &KMSConfig{
			Provider: "unknown",
		}

		_, err := getKMSKey(ctx, config)
		assert.Assert(t, err != nil)
	})
}

func TestGetVaultKey(t *testing.T) {
	ctx := context.Background()

	t.Run("NilConfig", func(t *testing.T) {
		_, err := getVaultKey(ctx, nil)
		assert.Assert(t, err != nil)
	})

	t.Run("ValidConfig", func(t *testing.T) {
		config := &VaultConfig{
			Address:   "https://vault.example.com",
			MountPath: "transit",
			KeyName:   "postgres-key",
		}

		key, err := getVaultKey(ctx, config)
		assert.NilError(t, err)
		assert.Assert(t, len(key) > 0)
	})
}

func TestApplyPGBackRestEncryption(t *testing.T) {
	t.Run("SingleRepo", func(t *testing.T) {
		cluster := &v1beta1.PostgresCluster{
			Spec: v1beta1.PostgresClusterSpec{
				Backups: v1beta1.Backups{
					PGBackRest: v1beta1.PGBackRestArchive{
						Repos: []v1beta1.PGBackRestRepo{
							{Name: "repo1"},
						},
					},
				},
			},
		}

		config := &EncryptionConfig{
			Type: EncryptionTypeAES256,
		}

		err := applyPGBackRestEncryption(cluster, config, "test-key")
		assert.NilError(t, err)
		assert.Assert(t, cluster.Spec.Backups.PGBackRest.Global != nil)
	})

	t.Run("MultipleRepos", func(t *testing.T) {
		cluster := &v1beta1.PostgresCluster{
			Spec: v1beta1.PostgresClusterSpec{
				Backups: v1beta1.Backups{
					PGBackRest: v1beta1.PGBackRestArchive{
						Repos: []v1beta1.PGBackRestRepo{
							{Name: "repo1"},
							{Name: "repo2"},
						},
					},
				},
			},
		}

		config := &EncryptionConfig{
			Type: EncryptionTypeAES256,
		}

		err := applyPGBackRestEncryption(cluster, config, "test-key")
		assert.NilError(t, err)
	})

	t.Run("ExistingGlobalConfig", func(t *testing.T) {
		cluster := &v1beta1.PostgresCluster{
			Spec: v1beta1.PostgresClusterSpec{
				Backups: v1beta1.Backups{
					PGBackRest: v1beta1.PGBackRestArchive{
						Global: map[string]string{
							"existing-key": "existing-value",
						},
						Repos: []v1beta1.PGBackRestRepo{
							{Name: "repo1"},
						},
					},
				},
			},
		}

		config := &EncryptionConfig{
			Type: EncryptionTypeAES256,
		}

		err := applyPGBackRestEncryption(cluster, config, "test-key")
		assert.NilError(t, err)
		// Should preserve existing config
		assert.Equal(t, cluster.Spec.Backups.PGBackRest.Global["existing-key"], "existing-value")
	})
}

func TestValidateEncryption(t *testing.T) {
	t.Run("NilConfig", func(t *testing.T) {
		issues := ValidateEncryption(nil)
		assert.Equal(t, len(issues), 0)
	})

	t.Run("DisabledConfig", func(t *testing.T) {
		config := &EncryptionConfig{Enabled: false}
		issues := ValidateEncryption(config)
		assert.Equal(t, len(issues), 0)
	})

	t.Run("ValidConfig", func(t *testing.T) {
		config := &EncryptionConfig{
			Enabled:  true,
			Type:     EncryptionTypeAES256,
			Provider: EncryptionProviderSecret,
		}

		issues := ValidateEncryption(config)
		assert.Equal(t, len(issues), 0)
	})

	t.Run("InvalidType", func(t *testing.T) {
		config := &EncryptionConfig{
			Enabled:  true,
			Type:     "invalid-type",
			Provider: EncryptionProviderSecret,
		}

		issues := ValidateEncryption(config)
		assert.Assert(t, len(issues) > 0)
	})

	t.Run("InvalidProvider", func(t *testing.T) {
		config := &EncryptionConfig{
			Enabled:  true,
			Type:     EncryptionTypeAES256,
			Provider: "invalid-provider",
		}

		issues := ValidateEncryption(config)
		assert.Assert(t, len(issues) > 0)
	})

	t.Run("KMSProviderWithoutConfig", func(t *testing.T) {
		config := &EncryptionConfig{
			Enabled:   true,
			Type:      EncryptionTypeAES256,
			Provider:  EncryptionProviderKMS,
			KMSConfig: nil,
		}

		issues := ValidateEncryption(config)
		assert.Assert(t, len(issues) > 0)
	})

	t.Run("KMSProviderWithIncompleteConfig", func(t *testing.T) {
		config := &EncryptionConfig{
			Enabled:   true,
			Type:      EncryptionTypeAES256,
			Provider:  EncryptionProviderKMS,
			KMSConfig: &KMSConfig{},
		}

		issues := ValidateEncryption(config)
		assert.Assert(t, len(issues) > 0)
	})

	t.Run("VaultProviderWithoutConfig", func(t *testing.T) {
		config := &EncryptionConfig{
			Enabled:     true,
			Type:        EncryptionTypeAES256,
			Provider:    EncryptionProviderVault,
			VaultConfig: nil,
		}

		issues := ValidateEncryption(config)
		assert.Assert(t, len(issues) > 0)
	})

	t.Run("VaultProviderWithIncompleteConfig", func(t *testing.T) {
		config := &EncryptionConfig{
			Enabled:     true,
			Type:        EncryptionTypeAES256,
			Provider:    EncryptionProviderVault,
			VaultConfig: &VaultConfig{},
		}

		issues := ValidateEncryption(config)
		assert.Assert(t, len(issues) > 0)
	})

	t.Run("VaultProviderWithValidConfig", func(t *testing.T) {
		config := &EncryptionConfig{
			Enabled:  true,
			Type:     EncryptionTypeAES256,
			Provider: EncryptionProviderVault,
			VaultConfig: &VaultConfig{
				Address: "https://vault.example.com",
				KeyName: "my-key",
			},
		}

		issues := ValidateEncryption(config)
		assert.Equal(t, len(issues), 0)
	})
}

func TestGenerateEncryptionReport(t *testing.T) {
	ctx := context.Background()

	t.Run("DisabledEncryption", func(t *testing.T) {
		cluster := &v1beta1.PostgresCluster{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-cluster",
				Namespace: "test-ns",
			},
		}

		report := GenerateEncryptionReport(ctx, nil, cluster, nil)
		assert.Assert(t, len(report) > 0)
	})

	t.Run("EnabledWithSecretProvider", func(t *testing.T) {
		cluster := &v1beta1.PostgresCluster{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-cluster",
				Namespace: "test-ns",
			},
		}

		config := &EncryptionConfig{
			Enabled:  true,
			Type:     EncryptionTypeAES256,
			Provider: EncryptionProviderSecret,
			KeyID:    "key-123",
		}

		report := GenerateEncryptionReport(ctx, nil, cluster, config)
		assert.Assert(t, len(report) > 0)
	})

	t.Run("EnabledWithKMSProvider", func(t *testing.T) {
		cluster := &v1beta1.PostgresCluster{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-cluster",
				Namespace: "test-ns",
			},
		}

		config := &EncryptionConfig{
			Enabled:  true,
			Type:     EncryptionTypeAES256,
			Provider: EncryptionProviderKMS,
			KMSConfig: &KMSConfig{
				Provider:  "aws",
				AWSRegion: "us-east-1",
				AWSKeyID:  "key-123",
			},
		}

		report := GenerateEncryptionReport(ctx, nil, cluster, config)
		assert.Assert(t, len(report) > 0)
	})

	t.Run("EnabledWithVaultProvider", func(t *testing.T) {
		cluster := &v1beta1.PostgresCluster{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-cluster",
				Namespace: "test-ns",
			},
		}

		config := &EncryptionConfig{
			Enabled:  true,
			Type:     EncryptionTypeAES256,
			Provider: EncryptionProviderVault,
			VaultConfig: &VaultConfig{
				Address:    "https://vault.example.com",
				MountPath:  "transit",
				KeyName:    "postgres-key",
				AuthMethod: "kubernetes",
			},
		}

		report := GenerateEncryptionReport(ctx, nil, cluster, config)
		assert.Assert(t, len(report) > 0)
	})

	t.Run("WithRotationEnabled", func(t *testing.T) {
		cluster := &v1beta1.PostgresCluster{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-cluster",
				Namespace: "test-ns",
			},
		}

		config := &EncryptionConfig{
			Enabled:          true,
			Type:             EncryptionTypeAES256,
			Provider:         EncryptionProviderSecret,
			RotationEnabled:  true,
			RotationInterval: "30d",
		}

		report := GenerateEncryptionReport(ctx, nil, cluster, config)
		assert.Assert(t, len(report) > 0)
	})
}

func TestEncryptionConfig(t *testing.T) {
	t.Run("DefaultConfig", func(t *testing.T) {
		config := &EncryptionConfig{
			Enabled:  true,
			Type:     EncryptionTypeAES256,
			Provider: EncryptionProviderSecret,
		}

		assert.Assert(t, config.Enabled)
		assert.Equal(t, config.Type, EncryptionTypeAES256)
	})

	t.Run("WithRotation", func(t *testing.T) {
		config := &EncryptionConfig{
			RotationEnabled:  true,
			RotationInterval: "30d",
		}

		assert.Assert(t, config.RotationEnabled)
		assert.Equal(t, config.RotationInterval, "30d")
	})
}

func TestKMSConfig(t *testing.T) {
	t.Run("AWSConfig", func(t *testing.T) {
		config := &KMSConfig{
			Provider:   "aws",
			AWSRegion:  "us-west-2",
			AWSKeyID:   "arn:aws:kms:us-west-2:123456789012:key/12345",
			AWSRoleARN: "arn:aws:iam::123456789012:role/MyRole",
		}

		assert.Equal(t, config.Provider, "aws")
		assert.Assert(t, config.AWSKeyID != "")
	})

	t.Run("GCPConfig", func(t *testing.T) {
		config := &KMSConfig{
			Provider:     "gcp",
			GCPProject:   "my-project",
			GCPLocation:  "us-central1",
			GCPKeyRing:   "my-keyring",
			GCPCryptoKey: "my-key",
		}

		assert.Equal(t, config.Provider, "gcp")
	})

	t.Run("AzureConfig", func(t *testing.T) {
		config := &KMSConfig{
			Provider:      "azure",
			AzureVaultURL: "https://myvault.vault.azure.net",
			AzureKeyName:  "my-key",
			AzureTenantID: "tenant-123",
			AzureClientID: "client-123",
		}

		assert.Equal(t, config.Provider, "azure")
	})
}

func TestVaultConfig(t *testing.T) {
	t.Run("KubernetesAuth", func(t *testing.T) {
		config := &VaultConfig{
			Address:      "https://vault.example.com",
			MountPath:    "transit",
			KeyName:      "postgres-key",
			AuthMethod:   "kubernetes",
			K8sRole:      "postgres-role",
			K8sTokenPath: "/var/run/secrets/kubernetes.io/serviceaccount/token",
		}

		assert.Equal(t, config.AuthMethod, "kubernetes")
	})

	t.Run("TokenAuth", func(t *testing.T) {
		config := &VaultConfig{
			Address:    "https://vault.example.com",
			MountPath:  "transit",
			KeyName:    "postgres-key",
			AuthMethod: "token",
			Token:      "s.XXXXXX",
		}

		assert.Equal(t, config.AuthMethod, "token")
	})

	t.Run("AppRoleAuth", func(t *testing.T) {
		config := &VaultConfig{
			Address:    "https://vault.example.com",
			MountPath:  "transit",
			KeyName:    "postgres-key",
			AuthMethod: "approle",
			RoleID:     "role-123",
			SecretID:   "secret-456",
		}

		assert.Equal(t, config.AuthMethod, "approle")
	})
}

func TestRotateKMSKey(t *testing.T) {
	ctx := context.Background()

	t.Run("ValidConfig", func(t *testing.T) {
		config := &KMSConfig{
			Provider:  "aws",
			AWSKeyID:  "key-123",
			AWSRegion: "us-east-1",
		}

		err := rotateKMSKey(ctx, config)
		assert.NilError(t, err)
	})
}

func TestRotateVaultKey(t *testing.T) {
	ctx := context.Background()

	t.Run("ValidConfig", func(t *testing.T) {
		config := &VaultConfig{
			Address: "https://vault.example.com",
			KeyName: "postgres-key",
		}

		err := rotateVaultKey(ctx, config)
		assert.NilError(t, err)
	})
}

func TestContainsHelper(t *testing.T) {
	t.Run("Found", func(t *testing.T) {
		result := contains([]string{"a", "b", "c"}, "b")
		assert.Assert(t, result)
	})

	t.Run("NotFound", func(t *testing.T) {
		result := contains([]string{"a", "b", "c"}, "d")
		assert.Assert(t, !result)
	})
}
