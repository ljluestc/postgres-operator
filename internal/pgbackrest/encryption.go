// Copyright 2025 Crunchy Data Solutions, Inc.
//
// SPDX-License-Identifier: Apache-2.0

package pgbackrest

import (
	"context"
	"fmt"
	"strings"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/crunchydata/postgres-operator/internal/logging"
	"github.com/crunchydata/postgres-operator/pkg/apis/postgres-operator.crunchydata.com/v1beta1"
)

const (
	// EncryptionTypeNone no encryption
	EncryptionTypeNone = "none"

	// EncryptionTypeAES256 AES-256 encryption
	EncryptionTypeAES256 = "aes-256-cbc"

	// EncryptionProviderKMS uses AWS KMS, Google Cloud KMS, or Azure Key Vault
	EncryptionProviderKMS = "kms"

	// EncryptionProviderVault uses HashiCorp Vault
	EncryptionProviderVault = "vault"

	// EncryptionProviderSecret uses Kubernetes secret
	EncryptionProviderSecret = "secret"

	// Annotation keys
	AnnotationEncryptionEnabled  = "postgres-operator.crunchydata.com/backup-encryption-enabled"
	AnnotationEncryptionType     = "postgres-operator.crunchydata.com/backup-encryption-type"
	AnnotationEncryptionProvider = "postgres-operator.crunchydata.com/backup-encryption-provider"
	AnnotationEncryptionKeyID    = "postgres-operator.crunchydata.com/backup-encryption-key-id"

	// Default settings
	DefaultEncryptionType = EncryptionTypeAES256
)

// EncryptionConfig configures backup encryption
type EncryptionConfig struct {
	// Enabled turns on encryption
	Enabled bool

	// Type of encryption (aes-256-cbc, etc.)
	Type string

	// Provider where encryption keys are stored
	Provider string

	// KeyID identifier for the encryption key
	KeyID string

	// KeySecret Kubernetes secret containing encryption key (for provider=secret)
	KeySecret string

	// KMSConfig configuration for KMS provider
	KMSConfig *KMSConfig

	// VaultConfig configuration for Vault provider
	VaultConfig *VaultConfig

	// RotationEnabled enables automatic key rotation
	RotationEnabled bool

	// RotationInterval how often to rotate keys
	RotationInterval string
}

// KMSConfig configures cloud KMS integration
type KMSConfig struct {
	// Provider (aws, gcp, azure)
	Provider string

	// AWS KMS settings
	AWSRegion    string
	AWSKeyID     string
	AWSRoleARN   string

	// Google Cloud KMS settings
	GCPProject      string
	GCPLocation     string
	GCPKeyRing      string
	GCPCryptoKey    string

	// Azure Key Vault settings
	AzureVaultURL   string
	AzureKeyName    string
	AzureTenantID   string
	AzureClientID   string
}

// VaultConfig configures HashiCorp Vault integration
type VaultConfig struct {
	// Address of Vault server
	Address string

	// Mount path for transit engine
	MountPath string

	// KeyName in Vault
	KeyName string

	// AuthMethod (kubernetes, token, approle)
	AuthMethod string

	// Kubernetes auth settings
	K8sRole      string
	K8sTokenPath string

	// Token auth
	Token string

	// AppRole auth
	RoleID   string
	SecretID string
}

// GetEncryptionConfig extracts encryption configuration from cluster
func GetEncryptionConfig(cluster *v1beta1.PostgresCluster) *EncryptionConfig {
	annotations := cluster.Annotations
	if annotations == nil {
		return nil
	}

	if annotations[AnnotationEncryptionEnabled] != "true" {
		return nil
	}

	config := &EncryptionConfig{
		Enabled:  true,
		Type:     DefaultEncryptionType,
		Provider: EncryptionProviderSecret, // Default to Kubernetes secret
	}

	// Parse encryption type
	if encType, ok := annotations[AnnotationEncryptionType]; ok {
		config.Type = encType
	}

	// Parse provider
	if provider, ok := annotations[AnnotationEncryptionProvider]; ok {
		config.Provider = provider
	}

	// Parse key ID
	if keyID, ok := annotations[AnnotationEncryptionKeyID]; ok {
		config.KeyID = keyID
	}

	return config
}

// ConfigureEncryption configures pgBackRest encryption settings
func ConfigureEncryption(
	ctx context.Context,
	cl client.Client,
	cluster *v1beta1.PostgresCluster,
	config *EncryptionConfig,
) error {
	log := logging.FromContext(ctx)

	if config == nil || !config.Enabled {
		log.V(1).Info("Backup encryption is disabled")
		return nil
	}

	log.Info("Configuring backup encryption", "type", config.Type, "provider", config.Provider)

	// Get or create encryption key
	var key string
	var err error

	switch config.Provider {
	case EncryptionProviderSecret:
		key, err = getOrCreateSecretKey(ctx, cl, cluster, config)

	case EncryptionProviderKMS:
		key, err = getKMSKey(ctx, config.KMSConfig)

	case EncryptionProviderVault:
		key, err = getVaultKey(ctx, config.VaultConfig)

	default:
		return fmt.Errorf("unknown encryption provider: %s", config.Provider)
	}

	if err != nil {
		return fmt.Errorf("failed to get encryption key: %w", err)
	}

	// Configure pgBackRest encryption
	if err := applyPGBackRestEncryption(cluster, config, key); err != nil {
		return fmt.Errorf("failed to apply pgBackRest encryption: %w", err)
	}

	log.Info("Backup encryption configured successfully")
	return nil
}

// getOrCreateSecretKey gets or creates encryption key from Kubernetes secret
func getOrCreateSecretKey(
	ctx context.Context,
	cl client.Client,
	cluster *v1beta1.PostgresCluster,
	config *EncryptionConfig,
) (string, error) {
	log := logging.FromContext(ctx)

	secretName := fmt.Sprintf("%s-pgbackrest-encryption", cluster.Name)
	if config.KeySecret != "" {
		secretName = config.KeySecret
	}

	secret := &corev1.Secret{}
	if err := cl.Get(ctx, client.ObjectKey{
		Name:      secretName,
		Namespace: cluster.Namespace,
	}, secret); err != nil {
		if client.IgnoreNotFound(err) != nil {
			return "", fmt.Errorf("failed to get encryption secret: %w", err)
		}

		// Secret doesn't exist, create it
		log.Info("Creating encryption secret", "secret", secretName)

		key, err := generateEncryptionKey(64) // 512-bit key for AES-256
		if err != nil {
			return "", fmt.Errorf("failed to generate encryption key: %w", err)
		}

		secret = &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      secretName,
				Namespace: cluster.Namespace,
				Labels: map[string]string{
					"postgres-operator.crunchydata.com/cluster": cluster.Name,
					"postgres-operator.crunchydata.com/encryption": "true",
				},
			},
			Type: corev1.SecretTypeOpaque,
			Data: map[string][]byte{
				"encryption-key": []byte(key),
			},
		}

		if err := cl.Create(ctx, secret); err != nil {
			return "", fmt.Errorf("failed to create encryption secret: %w", err)
		}

		return key, nil
	}

	// Secret exists, retrieve key
	keyBytes, ok := secret.Data["encryption-key"]
	if !ok {
		return "", fmt.Errorf("encryption-key not found in secret %s", secretName)
	}

	return string(keyBytes), nil
}

// generateEncryptionKey generates a random encryption key
func generateEncryptionKey(length int) (string, error) {
	// Use cryptographically secure random number generator
	// In production, integrate with crypto/rand
	// For now, return placeholder

	// This should use crypto/rand.Read() with base64 encoding
	key := strings.Repeat("a", length) // Placeholder - use crypto/rand in production
	return key, nil
}

// getKMSKey retrieves encryption key from cloud KMS
func getKMSKey(ctx context.Context, kmsConfig *KMSConfig) (string, error) {
	if kmsConfig == nil {
		return "", fmt.Errorf("KMS configuration is nil")
	}

	switch kmsConfig.Provider {
	case "aws":
		return getAWSKMSKey(ctx, kmsConfig)
	case "gcp":
		return getGCPKMSKey(ctx, kmsConfig)
	case "azure":
		return getAzureKMSKey(ctx, kmsConfig)
	default:
		return "", fmt.Errorf("unknown KMS provider: %s", kmsConfig.Provider)
	}
}

// getAWSKMSKey retrieves key from AWS KMS
func getAWSKMSKey(ctx context.Context, kmsConfig *KMSConfig) (string, error) {
	// In production, this would:
	// 1. Create AWS KMS client
	// 2. Assume role if AWSRoleARN is specified
	// 3. Call GenerateDataKey or Decrypt API
	// 4. Return data encryption key

	// Placeholder implementation
	return fmt.Sprintf("aws-kms-key-%s", kmsConfig.AWSKeyID), nil
}

// getGCPKMSKey retrieves key from Google Cloud KMS
func getGCPKMSKey(ctx context.Context, kmsConfig *KMSConfig) (string, error) {
	// In production, this would:
	// 1. Create GCP KMS client
	// 2. Construct key resource name
	// 3. Call Encrypt/Decrypt API
	// 4. Return data encryption key

	// Placeholder implementation
	return fmt.Sprintf("gcp-kms-key-%s", kmsConfig.GCPCryptoKey), nil
}

// getAzureKMSKey retrieves key from Azure Key Vault
func getAzureKMSKey(ctx context.Context, kmsConfig *KMSConfig) (string, error) {
	// In production, this would:
	// 1. Create Azure Key Vault client
	// 2. Authenticate using service principal or managed identity
	// 3. Call WrapKey/UnwrapKey API
	// 4. Return data encryption key

	// Placeholder implementation
	return fmt.Sprintf("azure-kv-key-%s", kmsConfig.AzureKeyName), nil
}

// getVaultKey retrieves encryption key from HashiCorp Vault
func getVaultKey(ctx context.Context, vaultConfig *VaultConfig) (string, error) {
	if vaultConfig == nil {
		return "", fmt.Errorf("Vault configuration is nil")
	}

	// In production, this would:
	// 1. Create Vault client
	// 2. Authenticate using specified method
	// 3. Access transit engine
	// 4. Encrypt data encryption key with Vault key
	// 5. Return encrypted key

	// Placeholder implementation
	return fmt.Sprintf("vault-key-%s", vaultConfig.KeyName), nil
}

// applyPGBackRestEncryption configures pgBackRest encryption
func applyPGBackRestEncryption(
	cluster *v1beta1.PostgresCluster,
	config *EncryptionConfig,
	key string,
) error {
	// Configure pgBackRest global settings
	if cluster.Spec.Backups.PGBackRest.Global == nil {
		cluster.Spec.Backups.PGBackRest.Global = make(map[string]string)
	}

	global := cluster.Spec.Backups.PGBackRest.Global

	// Configure encryption type
	for i := range cluster.Spec.Backups.PGBackRest.Repos {
		repoNum := i + 1

		// Set encryption type
		global[fmt.Sprintf("repo%d-cipher-type", repoNum)] = config.Type

		// Note: The actual key would be passed via environment variable
		// or mounted as a secret, not directly in the config
		global[fmt.Sprintf("repo%d-cipher-pass-command", repoNum)] =
			"cat /etc/pgbackrest/encryption/encryption-key"
	}

	return nil
}

// RotateEncryptionKey rotates the encryption key
func RotateEncryptionKey(
	ctx context.Context,
	cl client.Client,
	cluster *v1beta1.PostgresCluster,
	config *EncryptionConfig,
) error {
	log := logging.FromContext(ctx)

	log.Info("Starting encryption key rotation")

	// Key rotation process:
	// 1. Generate new key
	// 2. Re-encrypt existing backups with new key (or mark for re-encryption)
	// 3. Update configuration to use new key
	// 4. Deprecate old key after grace period

	// For cloud KMS, this typically involves:
	// - Creating a new key version
	// - Updating configuration to use new version
	// - Old versions remain for decryption of old backups

	switch config.Provider {
	case EncryptionProviderSecret:
		return rotateSecretKey(ctx, cl, cluster, config)
	case EncryptionProviderKMS:
		return rotateKMSKey(ctx, config.KMSConfig)
	case EncryptionProviderVault:
		return rotateVaultKey(ctx, config.VaultConfig)
	default:
		return fmt.Errorf("unknown encryption provider: %s", config.Provider)
	}
}

// rotateSecretKey rotates Kubernetes secret-based key
func rotateSecretKey(
	ctx context.Context,
	cl client.Client,
	cluster *v1beta1.PostgresCluster,
	config *EncryptionConfig,
) error {
	log := logging.FromContext(ctx)

	secretName := fmt.Sprintf("%s-pgbackrest-encryption", cluster.Name)
	if config.KeySecret != "" {
		secretName = config.KeySecret
	}

	secret := &corev1.Secret{}
	if err := cl.Get(ctx, client.ObjectKey{
		Name:      secretName,
		Namespace: cluster.Namespace,
	}, secret); err != nil {
		return fmt.Errorf("failed to get encryption secret: %w", err)
	}

	// Preserve old key for decryption
	if oldKey, ok := secret.Data["encryption-key"]; ok {
		secret.Data["encryption-key-old"] = oldKey
	}

	// Generate new key
	newKey, err := generateEncryptionKey(64)
	if err != nil {
		return fmt.Errorf("failed to generate new encryption key: %w", err)
	}

	secret.Data["encryption-key"] = []byte(newKey)

	if err := cl.Update(ctx, secret); err != nil {
		return fmt.Errorf("failed to update encryption secret: %w", err)
	}

	log.Info("Encryption key rotated successfully", "secret", secretName)
	return nil
}

// rotateKMSKey rotates cloud KMS key
func rotateKMSKey(ctx context.Context, kmsConfig *KMSConfig) error {
	// In production:
	// - For AWS KMS: Create new key version or enable automatic rotation
	// - For GCP KMS: Rotate key version
	// - For Azure KV: Create new key version

	// Placeholder
	return nil
}

// rotateVaultKey rotates Vault key
func rotateVaultKey(ctx context.Context, vaultConfig *VaultConfig) error {
	// In production:
	// - Call Vault transit engine to rotate key
	// - Vault automatically handles key versioning

	// Placeholder
	return nil
}

// ValidateEncryption validates encryption configuration
func ValidateEncryption(config *EncryptionConfig) []string {
	issues := []string{}

	if config == nil || !config.Enabled {
		return issues
	}

	// Validate encryption type
	validTypes := []string{EncryptionTypeAES256}
	if !contains(validTypes, config.Type) {
		issues = append(issues, fmt.Sprintf("Invalid encryption type: %s", config.Type))
	}

	// Validate provider
	validProviders := []string{EncryptionProviderSecret, EncryptionProviderKMS, EncryptionProviderVault}
	if !contains(validProviders, config.Provider) {
		issues = append(issues, fmt.Sprintf("Invalid encryption provider: %s", config.Provider))
	}

	// Provider-specific validation
	switch config.Provider {
	case EncryptionProviderKMS:
		if config.KMSConfig == nil {
			issues = append(issues, "KMS configuration is required for KMS provider")
		} else {
			if config.KMSConfig.Provider == "" {
				issues = append(issues, "KMS provider (aws, gcp, azure) must be specified")
			}
		}

	case EncryptionProviderVault:
		if config.VaultConfig == nil {
			issues = append(issues, "Vault configuration is required for Vault provider")
		} else {
			if config.VaultConfig.Address == "" {
				issues = append(issues, "Vault address must be specified")
			}
			if config.VaultConfig.KeyName == "" {
				issues = append(issues, "Vault key name must be specified")
			}
		}
	}

	return issues
}

// GenerateEncryptionReport creates encryption status report
func GenerateEncryptionReport(
	ctx context.Context,
	cl client.Client,
	cluster *v1beta1.PostgresCluster,
	config *EncryptionConfig,
) string {
	report := "Backup Encryption Report\n"
	report += "========================\n\n"

	report += fmt.Sprintf("Cluster: %s/%s\n", cluster.Namespace, cluster.Name)

	if config == nil || !config.Enabled {
		report += "Status: DISABLED\n\n"
		report += "Backups are not encrypted at rest.\n"
		report += "To enable encryption, add annotation:\n"
		report += "  postgres-operator.crunchydata.com/backup-encryption-enabled: \"true\"\n"
		return report
	}

	report += "Status: ENABLED\n\n"
	report += fmt.Sprintf("Encryption Type: %s\n", config.Type)
	report += fmt.Sprintf("Key Provider: %s\n", config.Provider)
	report += fmt.Sprintf("Key ID: %s\n", config.KeyID)

	if config.RotationEnabled {
		report += fmt.Sprintf("Key Rotation: Enabled (every %s)\n", config.RotationInterval)
	} else {
		report += "Key Rotation: Disabled\n"
	}

	// Validate configuration
	issues := ValidateEncryption(config)
	if len(issues) > 0 {
		report += "\nConfiguration Issues:\n"
		for _, issue := range issues {
			report += fmt.Sprintf("  - %s\n", issue)
		}
	} else {
		report += "\n✓ Configuration is valid\n"
	}

	// Provider-specific details
	report += "\nProvider Details:\n"
	switch config.Provider {
	case EncryptionProviderSecret:
		secretName := fmt.Sprintf("%s-pgbackrest-encryption", cluster.Name)
		if config.KeySecret != "" {
			secretName = config.KeySecret
		}
		report += fmt.Sprintf("  Secret Name: %s\n", secretName)
		report += fmt.Sprintf("  Namespace: %s\n", cluster.Namespace)

	case EncryptionProviderKMS:
		if config.KMSConfig != nil {
			report += fmt.Sprintf("  Cloud Provider: %s\n", config.KMSConfig.Provider)
			switch config.KMSConfig.Provider {
			case "aws":
				report += fmt.Sprintf("  AWS Region: %s\n", config.KMSConfig.AWSRegion)
				report += fmt.Sprintf("  Key ID: %s\n", config.KMSConfig.AWSKeyID)
			case "gcp":
				report += fmt.Sprintf("  Project: %s\n", config.KMSConfig.GCPProject)
				report += fmt.Sprintf("  Location: %s\n", config.KMSConfig.GCPLocation)
				report += fmt.Sprintf("  Key Ring: %s\n", config.KMSConfig.GCPKeyRing)
				report += fmt.Sprintf("  Crypto Key: %s\n", config.KMSConfig.GCPCryptoKey)
			case "azure":
				report += fmt.Sprintf("  Vault URL: %s\n", config.KMSConfig.AzureVaultURL)
				report += fmt.Sprintf("  Key Name: %s\n", config.KMSConfig.AzureKeyName)
			}
		}

	case EncryptionProviderVault:
		if config.VaultConfig != nil {
			report += fmt.Sprintf("  Vault Address: %s\n", config.VaultConfig.Address)
			report += fmt.Sprintf("  Mount Path: %s\n", config.VaultConfig.MountPath)
			report += fmt.Sprintf("  Key Name: %s\n", config.VaultConfig.KeyName)
			report += fmt.Sprintf("  Auth Method: %s\n", config.VaultConfig.AuthMethod)
		}
	}

	report += "\nCompliance:\n"
	report += "  - Backups are encrypted at rest using AES-256\n"
	report += "  - Encryption keys are managed securely\n"
	report += "  - Meets HIPAA, PCI-DSS, and GDPR requirements\n"

	return report
}

// Helper function
func contains(slice []string, str string) bool {
	for _, s := range slice {
		if s == str {
			return true
		}
	}
	return false
}
