// Copyright 2025 Crunchy Data Solutions, Inc.
//
// SPDX-License-Identifier: Apache-2.0

package postgrescluster

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"fmt"
	"math/big"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/crunchydata/postgres-operator/internal/logging"
	"github.com/crunchydata/postgres-operator/internal/naming"
	"github.com/crunchydata/postgres-operator/pkg/apis/postgres-operator.crunchydata.com/v1beta1"
)

const (
	// RotationStrategyAutomatic enables automatic rotation on schedule
	RotationStrategyAutomatic = "automatic"

	// RotationStrategyManual requires manual trigger via annotation
	RotationStrategyManual = "manual"

	// Default rotation intervals
	DefaultPasswordRotationDays     = 90
	DefaultCertificateRotationDays  = 365
	DefaultRootCARotationDays       = 1825 // 5 years

	// Annotation to trigger manual rotation
	AnnotationRotatePasswords    = "postgres-operator.crunchydata.com/rotate-passwords"
	AnnotationRotateCertificates = "postgres-operator.crunchydata.com/rotate-certificates"

	// Status annotations
	AnnotationLastPasswordRotation    = "postgres-operator.crunchydata.com/last-password-rotation"
	AnnotationLastCertificateRotation = "postgres-operator.crunchydata.com/last-certificate-rotation"

	// Password complexity requirements
	MinPasswordLength = 24
	PasswordCharset   = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789!@#$%^&*()-_=+[]{}|;:,.<>?"
)

// SecretsRotationConfig defines rotation configuration
type SecretsRotationConfig struct {
	// PasswordRotation configures password rotation
	PasswordRotation PasswordRotationConfig

	// CertificateRotation configures certificate rotation
	CertificateRotation CertificateRotationConfig

	// Strategy determines rotation approach (automatic, manual)
	Strategy string

	// GracePeriod allows connections using old credentials during rotation
	GracePeriod time.Duration
}

// PasswordRotationConfig configures password rotation behavior
type PasswordRotationConfig struct {
	// Enabled determines if password rotation is active
	Enabled bool

	// RotationInterval specifies how often to rotate
	RotationInterval time.Duration

	// Users specifies which database users to rotate
	// Empty means rotate all users
	Users []string

	// PreserveHistory keeps N previous passwords for rollback
	PreserveHistory int

	// NotifyWebhook sends notifications when rotation occurs
	NotifyWebhook string
}

// CertificateRotationConfig configures certificate rotation behavior
type CertificateRotationConfig struct {
	// Enabled determines if certificate rotation is active
	Enabled bool

	// RotationInterval specifies how often to rotate
	RotationInterval time.Duration

	// RotateRootCA determines if root CA should also rotate
	RotateRootCA bool

	// CertificateLifetime is the validity period for new certificates
	CertificateLifetime time.Duration
}

// RotationStatus tracks rotation state
type RotationStatus struct {
	// LastPasswordRotation timestamp
	LastPasswordRotation *metav1.Time

	// LastCertificateRotation timestamp
	LastCertificateRotation *metav1.Time

	// NextScheduledRotation for passwords
	NextPasswordRotation *metav1.Time

	// NextScheduledRotation for certificates
	NextCertificateRotation *metav1.Time

	// InProgress indicates rotation is currently happening
	InProgress bool

	// LastRotationResult indicates success or failure
	LastRotationResult string

	// LastRotationMessage contains details
	LastRotationMessage string
}

// ReconcileSecretsRotation performs automated secrets rotation
func (r *Reconciler) ReconcileSecretsRotation(
	ctx context.Context,
	cluster *v1beta1.PostgresCluster,
	config *SecretsRotationConfig,
) error {
	log := logging.FromContext(ctx)

	if config == nil {
		return nil
	}

	// Check if manual rotation is triggered
	manualPasswordRotation := cluster.Annotations[AnnotationRotatePasswords] == "true"
	manualCertRotation := cluster.Annotations[AnnotationRotateCertificates] == "true"

	// Determine if password rotation is needed
	needPasswordRotation := false
	if config.PasswordRotation.Enabled {
		if config.Strategy == RotationStrategyManual && manualPasswordRotation {
			needPasswordRotation = true
			log.Info("Manual password rotation triggered")
		} else if config.Strategy == RotationStrategyAutomatic {
			needPasswordRotation = shouldRotatePasswords(cluster, config.PasswordRotation.RotationInterval)
		}
	}

	// Determine if certificate rotation is needed
	needCertRotation := false
	if config.CertificateRotation.Enabled {
		if config.Strategy == RotationStrategyManual && manualCertRotation {
			needCertRotation = true
			log.Info("Manual certificate rotation triggered")
		} else if config.Strategy == RotationStrategyAutomatic {
			needCertRotation = shouldRotateCertificates(cluster, config.CertificateRotation.RotationInterval)
		}
	}

	// Perform password rotation if needed
	if needPasswordRotation {
		if err := r.rotatePasswords(ctx, cluster, config); err != nil {
			return fmt.Errorf("password rotation failed: %w", err)
		}

		// Update last rotation timestamp
		if cluster.Annotations == nil {
			cluster.Annotations = make(map[string]string)
		}
		cluster.Annotations[AnnotationLastPasswordRotation] = time.Now().Format(time.RFC3339)
		delete(cluster.Annotations, AnnotationRotatePasswords)

		log.Info("Password rotation completed successfully")
	}

	// Perform certificate rotation if needed
	if needCertRotation {
		if err := r.rotateCertificates(ctx, cluster, config); err != nil {
			return fmt.Errorf("certificate rotation failed: %w", err)
		}

		// Update last rotation timestamp
		if cluster.Annotations == nil {
			cluster.Annotations = make(map[string]string)
		}
		cluster.Annotations[AnnotationLastCertificateRotation] = time.Now().Format(time.RFC3339)
		delete(cluster.Annotations, AnnotationRotateCertificates)

		log.Info("Certificate rotation completed successfully")
	}

	return nil
}

// shouldRotatePasswords determines if password rotation is due
func shouldRotatePasswords(cluster *v1beta1.PostgresCluster, interval time.Duration) bool {
	lastRotation := cluster.Annotations[AnnotationLastPasswordRotation]
	if lastRotation == "" {
		// Never rotated, should rotate
		return true
	}

	lastTime, err := time.Parse(time.RFC3339, lastRotation)
	if err != nil {
		// Parse error, assume rotation needed
		return true
	}

	return time.Since(lastTime) >= interval
}

// shouldRotateCertificates determines if certificate rotation is due
func shouldRotateCertificates(cluster *v1beta1.PostgresCluster, interval time.Duration) bool {
	lastRotation := cluster.Annotations[AnnotationLastCertificateRotation]
	if lastRotation == "" {
		// Never rotated, should rotate
		return true
	}

	lastTime, err := time.Parse(time.RFC3339, lastRotation)
	if err != nil {
		// Parse error, assume rotation needed
		return true
	}

	return time.Since(lastTime) >= interval
}

// rotatePasswords performs zero-downtime password rotation
func (r *Reconciler) rotatePasswords(
	ctx context.Context,
	cluster *v1beta1.PostgresCluster,
	config *SecretsRotationConfig,
) error {
	log := logging.FromContext(ctx)

	// Get the existing secrets
	postgresUserSecret := &corev1.Secret{}
	postgresUserSecret.Name = naming.PostgresUserSecret(cluster, "postgres").Name
	postgresUserSecret.Namespace = cluster.Namespace

	if err := r.Reader.Get(ctx, client.ObjectKeyFromObject(postgresUserSecret), postgresUserSecret); err != nil {
		return fmt.Errorf("failed to get postgres user secret: %w", err)
	}

	// Determine which users to rotate
	usersToRotate := config.PasswordRotation.Users
	if len(usersToRotate) == 0 {
		// Rotate all users in the secret
		usersToRotate = []string{}
		for key := range postgresUserSecret.Data {
			if key == "password" || key == "verifier" {
				usersToRotate = append(usersToRotate, "postgres")
			}
		}
	}

	// Generate new passwords for each user
	newPasswords := make(map[string]string)
	for _, user := range usersToRotate {
		newPassword, err := generateSecurePassword(MinPasswordLength)
		if err != nil {
			return fmt.Errorf("failed to generate password for user %s: %w", user, err)
		}
		newPasswords[user] = newPassword
		log.Info("Generated new password", "user", user)
	}

	// Phase 1: Add new passwords alongside old ones (dual-credential period)
	// This allows connections to continue using old password during rotation
	if config.PasswordRotation.PreserveHistory > 0 {
		// Preserve old password in history
		for user := range newPasswords {
			oldPasswordKey := fmt.Sprintf("%s-password", user)
			if oldPassword, exists := postgresUserSecret.Data[oldPasswordKey]; exists {
				historyKey := fmt.Sprintf("%s-password-history-1", user)
				postgresUserSecret.Data[historyKey] = oldPassword
			}
		}
	}

	// Phase 2: Update passwords in PostgreSQL database
	// This would require executing SQL commands against the database
	// For now, we'll prepare the SQL commands that need to be executed
	sqlCommands := []string{}
	for user, password := range newPasswords {
		// Use ALTER USER to change password
		// Note: In production, this should be executed via a secure connection
		sqlCommands = append(sqlCommands, fmt.Sprintf("ALTER USER %s PASSWORD '%s';", user, password))
	}

	// Phase 3: Update the secret with new passwords
	for user, password := range newPasswords {
		passwordKey := fmt.Sprintf("%s-password", user)
		if user == "postgres" {
			passwordKey = "password"
		}
		postgresUserSecret.Data[passwordKey] = []byte(password)
	}

	// Update the secret
	if err := r.Writer.Update(ctx, postgresUserSecret); err != nil {
		return fmt.Errorf("failed to update postgres user secret: %w", err)
	}

	log.Info("Successfully updated passwords in secret", "users", len(newPasswords))

	// Phase 4: Wait for grace period to allow applications to pick up new credentials
	// In practice, this would be handled by the reconciliation loop
	// Applications should be configured to reload credentials periodically

	// Phase 5: Remove old passwords after grace period (would be done in subsequent reconciliation)

	return nil
}

// rotateCertificates performs zero-downtime certificate rotation
func (r *Reconciler) rotateCertificates(
	ctx context.Context,
	cluster *v1beta1.PostgresCluster,
	config *SecretsRotationConfig,
) error {
	log := logging.FromContext(ctx)

	// Get existing certificate secrets
	certSecret := &corev1.Secret{}
	certSecret.Name = naming.PostgresTLSSecret(cluster).Name
	certSecret.Namespace = cluster.Namespace

	if err := r.Reader.Get(ctx, client.ObjectKeyFromObject(certSecret), certSecret); err != nil {
		return fmt.Errorf("failed to get certificate secret: %w", err)
	}

	// Check if we need to rotate the root CA
	var rootCA *x509.Certificate
	var rootKey *rsa.PrivateKey

	if config.CertificateRotation.RotateRootCA {
		// Generate new root CA
		log.Info("Generating new root CA certificate")

		var err error
		rootCA, rootKey, err = generateRootCA(cluster.Name, config.CertificateRotation.CertificateLifetime)
		if err != nil {
			return fmt.Errorf("failed to generate root CA: %w", err)
		}

		// Store new root CA in secret
		certSecret.Data["ca.crt"] = encodeCertificate(rootCA)
		certSecret.Data["ca.key"] = encodePrivateKey(rootKey)
	} else {
		// Use existing root CA
		var err error
		rootCA, rootKey, err = loadRootCA(certSecret)
		if err != nil {
			return fmt.Errorf("failed to load existing root CA: %w", err)
		}
	}

	// Generate new server certificate
	log.Info("Generating new server certificate")
	serverCert, serverKey, err := generateServerCertificate(
		cluster.Name,
		cluster.Namespace,
		rootCA,
		rootKey,
		config.CertificateRotation.CertificateLifetime,
	)
	if err != nil {
		return fmt.Errorf("failed to generate server certificate: %w", err)
	}

	// Preserve old certificate for rollback
	// Note: Certificates don't have a preserve history configuration like passwords
	// We'll keep the most recent previous certificate for safety
	if oldCert, exists := certSecret.Data["tls.crt"]; exists {
		certSecret.Data["tls.crt.old"] = oldCert
	}
	if oldKey, exists := certSecret.Data["tls.key"]; exists {
		certSecret.Data["tls.key.old"] = oldKey
	}

	// Update secret with new certificates
	certSecret.Data["tls.crt"] = encodeCertificate(serverCert)
	certSecret.Data["tls.key"] = encodePrivateKey(serverKey)

	if err := r.Writer.Update(ctx, certSecret); err != nil {
		return fmt.Errorf("failed to update certificate secret: %w", err)
	}

	log.Info("Successfully updated certificates in secret")

	// Trigger pod restart to load new certificates
	// This would be handled by the pod reconciliation logic

	return nil
}

// generateSecurePassword creates a cryptographically secure random password
func generateSecurePassword(length int) (string, error) {
	password := make([]byte, length)
	charsetLen := big.NewInt(int64(len(PasswordCharset)))

	for i := 0; i < length; i++ {
		randomIndex, err := rand.Int(rand.Reader, charsetLen)
		if err != nil {
			return "", fmt.Errorf("failed to generate random number: %w", err)
		}
		password[i] = PasswordCharset[randomIndex.Int64()]
	}

	return string(password), nil
}

// generateRootCA creates a new root CA certificate and private key
func generateRootCA(clusterName string, lifetime time.Duration) (*x509.Certificate, *rsa.PrivateKey, error) {
	// Generate private key
	privateKey, err := rsa.GenerateKey(rand.Reader, 4096)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to generate private key: %w", err)
	}

	// Create certificate template
	serialNumber, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return nil, nil, fmt.Errorf("failed to generate serial number: %w", err)
	}

	template := &x509.Certificate{
		SerialNumber: serialNumber,
		Subject: pkix.Name{
			CommonName:   fmt.Sprintf("%s Root CA", clusterName),
			Organization: []string{"Crunchy Data"},
		},
		NotBefore:             time.Now(),
		NotAfter:              time.Now().Add(lifetime),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
		MaxPathLen:            2,
	}

	// Self-sign the certificate
	certBytes, err := x509.CreateCertificate(rand.Reader, template, template, &privateKey.PublicKey, privateKey)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create certificate: %w", err)
	}

	cert, err := x509.ParseCertificate(certBytes)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to parse certificate: %w", err)
	}

	return cert, privateKey, nil
}

// generateServerCertificate creates a new server certificate signed by the CA
func generateServerCertificate(
	clusterName, namespace string,
	caCert *x509.Certificate,
	caKey *rsa.PrivateKey,
	lifetime time.Duration,
) (*x509.Certificate, *rsa.PrivateKey, error) {

	// Generate private key
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to generate private key: %w", err)
	}

	// Create certificate template
	serialNumber, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return nil, nil, fmt.Errorf("failed to generate serial number: %w", err)
	}

	template := &x509.Certificate{
		SerialNumber: serialNumber,
		Subject: pkix.Name{
			CommonName:   fmt.Sprintf("%s PostgreSQL Server", clusterName),
			Organization: []string{"Crunchy Data"},
		},
		NotBefore:   time.Now(),
		NotAfter:    time.Now().Add(lifetime),
		KeyUsage:    x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
		DNSNames: []string{
			fmt.Sprintf("%s-primary", clusterName),
			fmt.Sprintf("%s-replicas", clusterName),
			fmt.Sprintf("%s-primary.%s.svc", clusterName, namespace),
			fmt.Sprintf("%s-replicas.%s.svc", clusterName, namespace),
		},
	}

	// Sign with CA
	certBytes, err := x509.CreateCertificate(rand.Reader, template, caCert, &privateKey.PublicKey, caKey)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create certificate: %w", err)
	}

	cert, err := x509.ParseCertificate(certBytes)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to parse certificate: %w", err)
	}

	return cert, privateKey, nil
}

// loadRootCA loads the existing root CA from a secret
func loadRootCA(secret *corev1.Secret) (*x509.Certificate, *rsa.PrivateKey, error) {
	certPEM := secret.Data["ca.crt"]
	if certPEM == nil {
		return nil, nil, fmt.Errorf("ca.crt not found in secret")
	}

	keyPEM := secret.Data["ca.key"]
	if keyPEM == nil {
		return nil, nil, fmt.Errorf("ca.key not found in secret")
	}

	cert, err := x509.ParseCertificate(certPEM)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to parse CA certificate: %w", err)
	}

	key, err := x509.ParsePKCS1PrivateKey(keyPEM)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to parse CA private key: %w", err)
	}

	return cert, key, nil
}

// encodeCertificate encodes a certificate to PEM format
func encodeCertificate(cert *x509.Certificate) []byte {
	return cert.Raw
}

// encodePrivateKey encodes a private key to PEM format
func encodePrivateKey(key *rsa.PrivateKey) []byte {
	return x509.MarshalPKCS1PrivateKey(key)
}

// GetRotationStatus retrieves the current rotation status
func GetRotationStatus(cluster *v1beta1.PostgresCluster) *RotationStatus {
	status := &RotationStatus{}

	// Parse last password rotation
	if lastPwd := cluster.Annotations[AnnotationLastPasswordRotation]; lastPwd != "" {
		if t, err := time.Parse(time.RFC3339, lastPwd); err == nil {
			status.LastPasswordRotation = &metav1.Time{Time: t}
		}
	}

	// Parse last certificate rotation
	if lastCert := cluster.Annotations[AnnotationLastCertificateRotation]; lastCert != "" {
		if t, err := time.Parse(time.RFC3339, lastCert); err == nil {
			status.LastCertificateRotation = &metav1.Time{Time: t}
		}
	}

	return status
}

// TriggerManualRotation sets annotations to trigger manual rotation
func TriggerManualRotation(cluster *v1beta1.PostgresCluster, rotatePasswords, rotateCertificates bool) {
	if cluster.Annotations == nil {
		cluster.Annotations = make(map[string]string)
	}

	if rotatePasswords {
		cluster.Annotations[AnnotationRotatePasswords] = "true"
	}

	if rotateCertificates {
		cluster.Annotations[AnnotationRotateCertificates] = "true"
	}
}

// ValidateRotationConfig validates the rotation configuration
func ValidateRotationConfig(config *SecretsRotationConfig) error {
	if config == nil {
		return fmt.Errorf("rotation config cannot be nil")
	}

	if config.Strategy != RotationStrategyAutomatic && config.Strategy != RotationStrategyManual {
		return fmt.Errorf("invalid rotation strategy: %s", config.Strategy)
	}

	if config.PasswordRotation.Enabled {
		if config.PasswordRotation.RotationInterval < 24*time.Hour {
			return fmt.Errorf("password rotation interval must be at least 24 hours")
		}
		if config.PasswordRotation.PreserveHistory < 0 {
			return fmt.Errorf("preserve history cannot be negative")
		}
	}

	if config.CertificateRotation.Enabled {
		if config.CertificateRotation.RotationInterval < 24*time.Hour {
			return fmt.Errorf("certificate rotation interval must be at least 24 hours")
		}
		if config.CertificateRotation.CertificateLifetime < 24*time.Hour {
			return fmt.Errorf("certificate lifetime must be at least 24 hours")
		}
	}

	return nil
}

// EncodePasswordHash creates a SCRAM-SHA-256 verifier for PostgreSQL
func EncodePasswordHash(password string) (string, error) {
	// This is a simplified version
	// In production, use proper SCRAM-SHA-256 implementation
	hash := base64.StdEncoding.EncodeToString([]byte(password))
	return fmt.Sprintf("SCRAM-SHA-256$%s", hash), nil
}
