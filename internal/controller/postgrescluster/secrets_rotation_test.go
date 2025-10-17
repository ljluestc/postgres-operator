// Copyright 2025 Crunchy Data Solutions, Inc.
//
// SPDX-License-Identifier: Apache-2.0

package postgrescluster

import (
	"testing"
	"time"

	"gotest.tools/v3/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/crunchydata/postgres-operator/pkg/apis/postgres-operator.crunchydata.com/v1beta1"
)

func TestGenerateSecurePassword(t *testing.T) {
	t.Run("MinimumLength", func(t *testing.T) {
		password, err := generateSecurePassword(MinPasswordLength)
		assert.NilError(t, err)
		assert.Equal(t, len(password), MinPasswordLength)
	})

	t.Run("CustomLength", func(t *testing.T) {
		length := 32
		password, err := generateSecurePassword(length)
		assert.NilError(t, err)
		assert.Equal(t, len(password), length)
	})

	t.Run("Uniqueness", func(t *testing.T) {
		pwd1, err := generateSecurePassword(24)
		assert.NilError(t, err)

		pwd2, err := generateSecurePassword(24)
		assert.NilError(t, err)

		assert.Assert(t, pwd1 != pwd2, "Passwords should be unique")
	})

	t.Run("CharacterSet", func(t *testing.T) {
		password, err := generateSecurePassword(100)
		assert.NilError(t, err)

		// Verify all characters are from the allowed charset
		for _, char := range password {
			assert.Assert(t, containsChar(PasswordCharset, char), "Invalid character: %c", char)
		}
	})
}

func TestShouldRotatePasswords(t *testing.T) {
	t.Run("NeverRotated", func(t *testing.T) {
		cluster := &v1beta1.PostgresCluster{}
		should := shouldRotatePasswords(cluster, 90*24*time.Hour)
		assert.Assert(t, should, "Should rotate when never rotated before")
	})

	t.Run("RotationDue", func(t *testing.T) {
		cluster := &v1beta1.PostgresCluster{
			ObjectMeta: metav1.ObjectMeta{
				Annotations: map[string]string{
					AnnotationLastPasswordRotation: time.Now().Add(-100 * 24 * time.Hour).Format(time.RFC3339),
				},
			},
		}
		should := shouldRotatePasswords(cluster, 90*24*time.Hour)
		assert.Assert(t, should, "Should rotate when interval has passed")
	})

	t.Run("RotationNotDue", func(t *testing.T) {
		cluster := &v1beta1.PostgresCluster{
			ObjectMeta: metav1.ObjectMeta{
				Annotations: map[string]string{
					AnnotationLastPasswordRotation: time.Now().Add(-30 * 24 * time.Hour).Format(time.RFC3339),
				},
			},
		}
		should := shouldRotatePasswords(cluster, 90*24*time.Hour)
		assert.Assert(t, !should, "Should not rotate when interval has not passed")
	})

	t.Run("InvalidTimestamp", func(t *testing.T) {
		cluster := &v1beta1.PostgresCluster{
			ObjectMeta: metav1.ObjectMeta{
				Annotations: map[string]string{
					AnnotationLastPasswordRotation: "invalid-timestamp",
				},
			},
		}
		should := shouldRotatePasswords(cluster, 90*24*time.Hour)
		assert.Assert(t, should, "Should rotate when timestamp is invalid")
	})
}

func TestShouldRotateCertificates(t *testing.T) {
	t.Run("NeverRotated", func(t *testing.T) {
		cluster := &v1beta1.PostgresCluster{}
		should := shouldRotateCertificates(cluster, 365*24*time.Hour)
		assert.Assert(t, should, "Should rotate when never rotated before")
	})

	t.Run("RotationDue", func(t *testing.T) {
		cluster := &v1beta1.PostgresCluster{
			ObjectMeta: metav1.ObjectMeta{
				Annotations: map[string]string{
					AnnotationLastCertificateRotation: time.Now().Add(-400 * 24 * time.Hour).Format(time.RFC3339),
				},
			},
		}
		should := shouldRotateCertificates(cluster, 365*24*time.Hour)
		assert.Assert(t, should, "Should rotate when interval has passed")
	})

	t.Run("RotationNotDue", func(t *testing.T) {
		cluster := &v1beta1.PostgresCluster{
			ObjectMeta: metav1.ObjectMeta{
				Annotations: map[string]string{
					AnnotationLastCertificateRotation: time.Now().Add(-100 * 24 * time.Hour).Format(time.RFC3339),
				},
			},
		}
		should := shouldRotateCertificates(cluster, 365*24*time.Hour)
		assert.Assert(t, !should, "Should not rotate when interval has not passed")
	})
}

func TestGetRotationStatus(t *testing.T) {
	t.Run("NoRotationHistory", func(t *testing.T) {
		cluster := &v1beta1.PostgresCluster{}
		status := GetRotationStatus(cluster)

		assert.Assert(t, status != nil)
		assert.Assert(t, status.LastPasswordRotation == nil)
		assert.Assert(t, status.LastCertificateRotation == nil)
	})

	t.Run("WithRotationHistory", func(t *testing.T) {
		pwdTime := time.Now().Add(-30 * 24 * time.Hour)
		certTime := time.Now().Add(-180 * 24 * time.Hour)

		cluster := &v1beta1.PostgresCluster{
			ObjectMeta: metav1.ObjectMeta{
				Annotations: map[string]string{
					AnnotationLastPasswordRotation:    pwdTime.Format(time.RFC3339),
					AnnotationLastCertificateRotation: certTime.Format(time.RFC3339),
				},
			},
		}
		status := GetRotationStatus(cluster)

		assert.Assert(t, status != nil)
		assert.Assert(t, status.LastPasswordRotation != nil)
		assert.Assert(t, status.LastCertificateRotation != nil)
	})

	t.Run("InvalidTimestamps", func(t *testing.T) {
		cluster := &v1beta1.PostgresCluster{
			ObjectMeta: metav1.ObjectMeta{
				Annotations: map[string]string{
					AnnotationLastPasswordRotation:    "invalid",
					AnnotationLastCertificateRotation: "invalid",
				},
			},
		}
		status := GetRotationStatus(cluster)

		assert.Assert(t, status != nil)
		// Should handle invalid timestamps gracefully
	})
}

func TestTriggerManualRotation(t *testing.T) {
	t.Run("BothTypes", func(t *testing.T) {
		cluster := &v1beta1.PostgresCluster{}
		TriggerManualRotation(cluster, true, true)

		assert.Equal(t, cluster.Annotations[AnnotationRotatePasswords], "true")
		assert.Equal(t, cluster.Annotations[AnnotationRotateCertificates], "true")
	})

	t.Run("PasswordsOnly", func(t *testing.T) {
		cluster := &v1beta1.PostgresCluster{}
		TriggerManualRotation(cluster, true, false)

		assert.Equal(t, cluster.Annotations[AnnotationRotatePasswords], "true")
		_, hasCert := cluster.Annotations[AnnotationRotateCertificates]
		assert.Assert(t, !hasCert)
	})

	t.Run("CertificatesOnly", func(t *testing.T) {
		cluster := &v1beta1.PostgresCluster{}
		TriggerManualRotation(cluster, false, true)

		_, hasPwd := cluster.Annotations[AnnotationRotatePasswords]
		assert.Assert(t, !hasPwd)
		assert.Equal(t, cluster.Annotations[AnnotationRotateCertificates], "true")
	})
}

func TestValidateRotationConfig(t *testing.T) {
	t.Run("ValidConfig", func(t *testing.T) {
		config := &SecretsRotationConfig{
			Strategy: RotationStrategyAutomatic,
			PasswordRotation: PasswordRotationConfig{
				Enabled:          true,
				RotationInterval: 90 * 24 * time.Hour,
				PreserveHistory:  2,
			},
			CertificateRotation: CertificateRotationConfig{
				Enabled:             true,
				RotationInterval:    365 * 24 * time.Hour,
				CertificateLifetime: 730 * 24 * time.Hour,
			},
		}

		err := ValidateRotationConfig(config)
		assert.NilError(t, err)
	})

	t.Run("NilConfig", func(t *testing.T) {
		err := ValidateRotationConfig(nil)
		assert.ErrorContains(t, err, "cannot be nil")
	})

	t.Run("InvalidStrategy", func(t *testing.T) {
		config := &SecretsRotationConfig{
			Strategy: "invalid-strategy",
		}

		err := ValidateRotationConfig(config)
		assert.ErrorContains(t, err, "invalid rotation strategy")
	})

	t.Run("IntervalTooShort", func(t *testing.T) {
		config := &SecretsRotationConfig{
			Strategy: RotationStrategyAutomatic,
			PasswordRotation: PasswordRotationConfig{
				Enabled:          true,
				RotationInterval: 12 * time.Hour,
			},
		}

		err := ValidateRotationConfig(config)
		assert.ErrorContains(t, err, "at least 24 hours")
	})

	t.Run("NegativeHistory", func(t *testing.T) {
		config := &SecretsRotationConfig{
			Strategy: RotationStrategyAutomatic,
			PasswordRotation: PasswordRotationConfig{
				Enabled:          true,
				RotationInterval: 24 * time.Hour,
				PreserveHistory:  -1,
			},
		}

		err := ValidateRotationConfig(config)
		assert.ErrorContains(t, err, "cannot be negative")
	})

	t.Run("CertificateLifetimeTooShort", func(t *testing.T) {
		config := &SecretsRotationConfig{
			Strategy: RotationStrategyAutomatic,
			CertificateRotation: CertificateRotationConfig{
				Enabled:             true,
				RotationInterval:    24 * time.Hour,
				CertificateLifetime: 12 * time.Hour,
			},
		}

		err := ValidateRotationConfig(config)
		assert.ErrorContains(t, err, "at least 24 hours")
	})
}

func TestEncodePasswordHash(t *testing.T) {
	t.Run("BasicEncoding", func(t *testing.T) {
		password := "test-password-123"
		hash, err := EncodePasswordHash(password)

		assert.NilError(t, err)
		assert.Assert(t, hash != "")
		assert.Assert(t, len(hash) > len("SCRAM-SHA-256$"))
	})

	t.Run("DifferentPasswords", func(t *testing.T) {
		hash1, err := EncodePasswordHash("password1")
		assert.NilError(t, err)

		hash2, err := EncodePasswordHash("password2")
		assert.NilError(t, err)

		assert.Assert(t, hash1 != hash2, "Different passwords should produce different hashes")
	})
}

func TestRotationConstants(t *testing.T) {
	assert.Equal(t, RotationStrategyAutomatic, "automatic")
	assert.Equal(t, RotationStrategyManual, "manual")
	assert.Equal(t, DefaultPasswordRotationDays, 90)
	assert.Equal(t, DefaultCertificateRotationDays, 365)
	assert.Equal(t, DefaultRootCARotationDays, 1825)
	assert.Equal(t, MinPasswordLength, 24)
}

func TestSecretsRotationConfig(t *testing.T) {
	config := SecretsRotationConfig{
		PasswordRotation: PasswordRotationConfig{
			Enabled:          true,
			RotationInterval: 90 * 24 * time.Hour,
			Users:            []string{"postgres", "app_user"},
			PreserveHistory:  2,
			NotifyWebhook:    "https://example.com/webhook",
		},
		CertificateRotation: CertificateRotationConfig{
			Enabled:             true,
			RotationInterval:    365 * 24 * time.Hour,
			RotateRootCA:        false,
			CertificateLifetime: 730 * 24 * time.Hour,
		},
		Strategy:    RotationStrategyAutomatic,
		GracePeriod: 5 * time.Minute,
	}

	assert.Assert(t, config.PasswordRotation.Enabled)
	assert.Assert(t, config.CertificateRotation.Enabled)
	assert.Equal(t, config.Strategy, RotationStrategyAutomatic)
	assert.Equal(t, len(config.PasswordRotation.Users), 2)
}

// Helper function
func containsChar(s string, c rune) bool {
	for _, char := range s {
		if char == c {
			return true
		}
	}
	return false
}
