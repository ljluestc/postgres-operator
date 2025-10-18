// Copyright 2025 Crunchy Data Solutions, Inc.
//
// SPDX-License-Identifier: Apache-2.0

package wizard

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"

	"github.com/AlecAivazis/survey/v2"
	"github.com/spf13/cobra"
	"k8s.io/apimachinery/pkg/api/resource"
	"sigs.k8s.io/yaml"

	"github.com/crunchydata/postgres-operator/pkg/apis/postgres-operator.crunchydata.com/v1beta1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// Flags from main command
var (
	Preset         *string
	OutputFile     *string
	Apply          *bool
	Namespace      *string
	Kubeconfig     *string
	DryRun         *bool
	NonInteractive *bool
)

// ClusterConfig holds wizard responses
type ClusterConfig struct {
	// Basic settings
	Name          string
	Namespace     string
	PostgresVersion int

	// Environment preset
	Environment string // development, staging, production

	// Instance configuration
	Replicas      int
	CPURequest    string
	CPULimit      string
	MemoryRequest string
	MemoryLimit   string
	StorageSize   string
	StorageClass  string

	// Optional features
	EnableHA          bool
	EnableBackup      bool
	EnableMonitoring  bool
	EnablePgBouncer   bool
	EnableTLS         bool

	// Backup configuration
	BackupSchedule   string
	BackupRetention  string
	BackupRepository string

	// High availability
	HAMode           string
	MinAvailable     int

	// Advanced options
	WALStorage       bool
	WALStorageSize   string
	CustomParameters map[string]string
}

// PresetConfig defines preset configurations
type PresetConfig struct {
	Name          string
	Description   string
	Replicas      int
	CPURequest    string
	CPULimit      string
	MemoryRequest string
	MemoryLimit   string
	StorageSize   string
	EnableHA      bool
	EnableBackup  bool
	EnableMonitoring bool
	BackupSchedule string
}

var presets = map[string]PresetConfig{
	"development": {
		Name:          "Development",
		Description:   "Single instance, minimal resources, no backups",
		Replicas:      1,
		CPURequest:    "500m",
		CPULimit:      "1000m",
		MemoryRequest: "1Gi",
		MemoryLimit:   "2Gi",
		StorageSize:   "10Gi",
		EnableHA:      false,
		EnableBackup:  false,
		EnableMonitoring: false,
		BackupSchedule: "",
	},
	"staging": {
		Name:          "Staging",
		Description:   "2 replicas, moderate resources, daily backups",
		Replicas:      2,
		CPURequest:    "1000m",
		CPULimit:      "2000m",
		MemoryRequest: "4Gi",
		MemoryLimit:   "8Gi",
		StorageSize:   "50Gi",
		EnableHA:      true,
		EnableBackup:  true,
		EnableMonitoring: true,
		BackupSchedule: "0 2 * * *", // Daily at 2 AM
	},
	"production": {
		Name:          "Production",
		Description:   "3 replicas, high resources, frequent backups, full HA",
		Replicas:      3,
		CPURequest:    "2000m",
		CPULimit:      "4000m",
		MemoryRequest: "8Gi",
		MemoryLimit:   "16Gi",
		StorageSize:   "100Gi",
		EnableHA:      true,
		EnableBackup:  true,
		EnableMonitoring: true,
		BackupSchedule: "0 */6 * * *", // Every 6 hours
	},
}

// RunCreateWizard runs the interactive cluster creation wizard
func RunCreateWizard(cmd *cobra.Command, args []string) error {
	ctx := context.Background()

	fmt.Println(`
╔═══════════════════════════════════════════════════════════╗
║  PostgreSQL Cluster Creation Wizard                       ║
║  Crunchy Postgres Operator                                ║
╚═══════════════════════════════════════════════════════════╝
`)

	config := &ClusterConfig{
		Namespace: *Namespace,
	}

	// Check if preset is specified
	if *Preset != "" {
		if err := applyPreset(config, *Preset); err != nil {
			return fmt.Errorf("failed to apply preset: %w", err)
		}

		if *NonInteractive {
			// Just use preset, skip prompts
			fmt.Printf("✓ Using preset: %s\n", *Preset)
		}
	}

	// Run interactive prompts (unless non-interactive)
	if !*NonInteractive {
		if err := runInteractivePrompts(config); err != nil {
			return fmt.Errorf("wizard failed: %w", err)
		}
	} else if *Preset == "" {
		// Non-interactive without preset - use defaults
		setDefaults(config)
	}

	// Generate YAML
	cluster, err := generateClusterSpec(config)
	if err != nil {
		return fmt.Errorf("failed to generate cluster spec: %w", err)
	}

	yamlData, err := yaml.Marshal(cluster)
	if err != nil {
		return fmt.Errorf("failed to marshal YAML: %w", err)
	}

	// Output YAML
	if *OutputFile != "" {
		if err := os.WriteFile(*OutputFile, yamlData, 0644); err != nil {
			return fmt.Errorf("failed to write output file: %w", err)
		}
		fmt.Printf("✓ Cluster manifest written to: %s\n", *OutputFile)
	} else if !*Apply {
		fmt.Println("\n" + strings.Repeat("─", 60))
		fmt.Println("Generated Cluster Manifest:")
		fmt.Println(strings.Repeat("─", 60))
		fmt.Println(string(yamlData))
	}

	// Apply to cluster if requested
	if *Apply {
		if *DryRun {
			fmt.Println("\n✓ Dry run mode - manifest would be applied:")
			fmt.Println(string(yamlData))
		} else {
			if err := applyCluster(ctx, yamlData); err != nil {
				return fmt.Errorf("failed to apply cluster: %w", err)
			}
			fmt.Printf("✓ Cluster '%s' created successfully in namespace '%s'\n", config.Name, config.Namespace)
			fmt.Println("\nNext steps:")
			fmt.Printf("  kubectl get postgrescluster -n %s %s\n", config.Namespace, config.Name)
			fmt.Printf("  kubectl get pods -n %s -l postgres-operator.crunchydata.com/cluster=%s\n", config.Namespace, config.Name)
		}
	} else {
		fmt.Println("\nTo create this cluster, run:")
		if *OutputFile != "" {
			fmt.Printf("  kubectl apply -f %s\n", *OutputFile)
		} else {
			fmt.Println("  kubectl apply -f <manifest.yaml>")
		}
	}

	return nil
}

// applyPreset applies a preset configuration
func applyPreset(config *ClusterConfig, presetName string) error {
	preset, exists := presets[presetName]
	if !exists {
		return fmt.Errorf("unknown preset: %s (available: development, staging, production)", presetName)
	}

	config.Environment = presetName
	config.Replicas = preset.Replicas
	config.CPURequest = preset.CPURequest
	config.CPULimit = preset.CPULimit
	config.MemoryRequest = preset.MemoryRequest
	config.MemoryLimit = preset.MemoryLimit
	config.StorageSize = preset.StorageSize
	config.EnableHA = preset.EnableHA
	config.EnableBackup = preset.EnableBackup
	config.EnableMonitoring = preset.EnableMonitoring
	config.BackupSchedule = preset.BackupSchedule

	return nil
}

// setDefaults sets default configuration
func setDefaults(config *ClusterConfig) {
	config.Name = "postgres"
	config.PostgresVersion = 16
	config.Environment = "development"
	config.Replicas = 1
	config.CPURequest = "500m"
	config.CPULimit = "1000m"
	config.MemoryRequest = "1Gi"
	config.MemoryLimit = "2Gi"
	config.StorageSize = "10Gi"
	config.EnableHA = false
	config.EnableBackup = false
	config.EnableMonitoring = false
	config.EnablePgBouncer = false
	config.EnableTLS = true
}

// runInteractivePrompts runs the interactive questionnaire
func runInteractivePrompts(config *ClusterConfig) error {
	// Basic Configuration
	fmt.Println("\n📝 Basic Configuration")
	fmt.Println(strings.Repeat("─", 60))

	// Cluster name
	namePrompt := &survey.Input{
		Message: "Cluster name:",
		Default: "postgres",
		Help:    "Name must be lowercase, alphanumeric, and hyphens only",
	}
	if err := survey.AskOne(namePrompt, &config.Name, survey.WithValidator(validateClusterName)); err != nil {
		return err
	}

	// PostgreSQL version
	versionPrompt := &survey.Select{
		Message: "PostgreSQL version:",
		Options: []string{"16 (recommended)", "17", "18"},
		Default: "16 (recommended)",
	}
	var versionChoice string
	if err := survey.AskOne(versionPrompt, &versionChoice); err != nil {
		return err
	}
	config.PostgresVersion = extractVersion(versionChoice)

	// Environment preset
	if config.Environment == "" {
		presetPrompt := &survey.Select{
			Message: "Choose environment preset:",
			Options: []string{
				"development - Single instance, minimal resources",
				"staging - 2 replicas, moderate resources, daily backups",
				"production - 3 replicas, high resources, frequent backups",
				"custom - Configure manually",
			},
			Help: "Presets provide smart defaults for common scenarios",
		}
		var presetChoice string
		if err := survey.AskOne(presetPrompt, &presetChoice); err != nil {
			return err
		}

		presetName := strings.Split(presetChoice, " - ")[0]
		if presetName != "custom" {
			if err := applyPreset(config, presetName); err != nil {
				return err
			}
		} else {
			config.Environment = "custom"
		}
	}

	// Custom configuration prompts
	if config.Environment == "custom" || promptCustomize() {
		if err := promptInstanceConfig(config); err != nil {
			return err
		}

		if err := promptFeatures(config); err != nil {
			return err
		}

		if config.EnableBackup {
			if err := promptBackupConfig(config); err != nil {
				return err
			}
		}
	}

	// Review configuration
	fmt.Println("\n📋 Configuration Summary")
	fmt.Println(strings.Repeat("─", 60))
	printConfigSummary(config)

	confirmPrompt := &survey.Confirm{
		Message: "Create cluster with this configuration?",
		Default: true,
	}
	var confirmed bool
	if err := survey.AskOne(confirmPrompt, &confirmed); err != nil {
		return err
	}

	if !confirmed {
		return fmt.Errorf("cluster creation cancelled")
	}

	return nil
}

// promptCustomize asks if user wants to customize preset
func promptCustomize() bool {
	customizePrompt := &survey.Confirm{
		Message: "Customize preset configuration?",
		Default: false,
	}
	var customize bool
	survey.AskOne(customizePrompt, &customize)
	return customize
}

// promptInstanceConfig prompts for instance configuration
func promptInstanceConfig(config *ClusterConfig) error {
	fmt.Println("\n⚙️  Instance Configuration")
	fmt.Println(strings.Repeat("─", 60))

	// Number of replicas
	replicasPrompt := &survey.Input{
		Message: "Number of instances (replicas):",
		Default: fmt.Sprintf("%d", config.Replicas),
		Help:    "1 for single instance, 2+ for high availability",
	}
	var replicasStr string
	if err := survey.AskOne(replicasPrompt, &replicasStr, survey.WithValidator(validatePositiveInt)); err != nil {
		return err
	}
	config.Replicas, _ = strconv.Atoi(replicasStr)

	// CPU
	cpuRequestPrompt := &survey.Input{
		Message: "CPU request:",
		Default: config.CPURequest,
		Help:    "e.g., 500m, 1000m, 2",
	}
	if err := survey.AskOne(cpuRequestPrompt, &config.CPURequest, survey.WithValidator(validateResource)); err != nil {
		return err
	}

	cpuLimitPrompt := &survey.Input{
		Message: "CPU limit:",
		Default: config.CPULimit,
		Help:    "e.g., 1000m, 2000m, 4",
	}
	if err := survey.AskOne(cpuLimitPrompt, &config.CPULimit, survey.WithValidator(validateResource)); err != nil {
		return err
	}

	// Memory
	memRequestPrompt := &survey.Input{
		Message: "Memory request:",
		Default: config.MemoryRequest,
		Help:    "e.g., 1Gi, 2Gi, 4Gi",
	}
	if err := survey.AskOne(memRequestPrompt, &config.MemoryRequest, survey.WithValidator(validateResource)); err != nil {
		return err
	}

	memLimitPrompt := &survey.Input{
		Message: "Memory limit:",
		Default: config.MemoryLimit,
		Help:    "e.g., 2Gi, 4Gi, 8Gi",
	}
	if err := survey.AskOne(memLimitPrompt, &config.MemoryLimit, survey.WithValidator(validateResource)); err != nil {
		return err
	}

	// Storage
	storagePrompt := &survey.Input{
		Message: "Storage size:",
		Default: config.StorageSize,
		Help:    "e.g., 10Gi, 50Gi, 100Gi",
	}
	if err := survey.AskOne(storagePrompt, &config.StorageSize, survey.WithValidator(validateResource)); err != nil {
		return err
	}

	return nil
}

// promptFeatures prompts for optional features
func promptFeatures(config *ClusterConfig) error {
	fmt.Println("\n✨ Features")
	fmt.Println(strings.Repeat("─", 60))

	featuresPrompt := &survey.MultiSelect{
		Message: "Enable features:",
		Options: []string{
			"High Availability (multiple replicas)",
			"Automated Backups (pgBackRest)",
			"Monitoring (pgMonitor)",
			"Connection Pooling (pgBouncer)",
			"TLS Encryption",
		},
		Default: getDefaultFeatures(config),
	}

	var selectedFeatures []string
	if err := survey.AskOne(featuresPrompt, &selectedFeatures); err != nil {
		return err
	}

	config.EnableHA = contains(selectedFeatures, "High Availability (multiple replicas)")
	config.EnableBackup = contains(selectedFeatures, "Automated Backups (pgBackRest)")
	config.EnableMonitoring = contains(selectedFeatures, "Monitoring (pgMonitor)")
	config.EnablePgBouncer = contains(selectedFeatures, "Connection Pooling (pgBouncer)")
	config.EnableTLS = contains(selectedFeatures, "TLS Encryption")

	return nil
}

// promptBackupConfig prompts for backup configuration
func promptBackupConfig(config *ClusterConfig) error {
	fmt.Println("\n💾 Backup Configuration")
	fmt.Println(strings.Repeat("─", 60))

	schedulePrompt := &survey.Select{
		Message: "Backup schedule:",
		Options: []string{
			"Every 6 hours (production)",
			"Daily at 2 AM (staging)",
			"Weekly on Sunday (development)",
			"Custom cron expression",
		},
		Default: "Daily at 2 AM (staging)",
	}

	var scheduleChoice string
	if err := survey.AskOne(schedulePrompt, &scheduleChoice); err != nil {
		return err
	}

	switch {
	case strings.Contains(scheduleChoice, "Every 6 hours"):
		config.BackupSchedule = "0 */6 * * *"
	case strings.Contains(scheduleChoice, "Daily"):
		config.BackupSchedule = "0 2 * * *"
	case strings.Contains(scheduleChoice, "Weekly"):
		config.BackupSchedule = "0 2 * * 0"
	case strings.Contains(scheduleChoice, "Custom"):
		cronPrompt := &survey.Input{
			Message: "Enter cron expression:",
			Help:    "Format: minute hour day month weekday",
		}
		if err := survey.AskOne(cronPrompt, &config.BackupSchedule); err != nil {
			return err
		}
	}

	retentionPrompt := &survey.Input{
		Message: "Backup retention (days):",
		Default: "14",
		Help:    "Number of days to retain backups",
	}
	var retentionStr string
	if err := survey.AskOne(retentionPrompt, &retentionStr, survey.WithValidator(validatePositiveInt)); err != nil {
		return err
	}
	config.BackupRetention = retentionStr

	return nil
}

// printConfigSummary prints configuration summary
func printConfigSummary(config *ClusterConfig) {
	fmt.Printf("  Name:              %s\n", config.Name)
	fmt.Printf("  Namespace:         %s\n", config.Namespace)
	fmt.Printf("  PostgreSQL:        %d\n", config.PostgresVersion)
	fmt.Printf("  Environment:       %s\n", config.Environment)
	fmt.Printf("  Replicas:          %d\n", config.Replicas)
	fmt.Printf("  CPU:               %s (request) / %s (limit)\n", config.CPURequest, config.CPULimit)
	fmt.Printf("  Memory:            %s (request) / %s (limit)\n", config.MemoryRequest, config.MemoryLimit)
	fmt.Printf("  Storage:           %s\n", config.StorageSize)
	fmt.Printf("  High Availability: %v\n", config.EnableHA)
	fmt.Printf("  Backups:           %v\n", config.EnableBackup)
	if config.EnableBackup {
		fmt.Printf("    Schedule:        %s\n", config.BackupSchedule)
		fmt.Printf("    Retention:       %s days\n", config.BackupRetention)
	}
	fmt.Printf("  Monitoring:        %v\n", config.EnableMonitoring)
	fmt.Printf("  pgBouncer:         %v\n", config.EnablePgBouncer)
	fmt.Printf("  TLS:               %v\n", config.EnableTLS)
}

// generateClusterSpec generates PostgresCluster spec from config
func generateClusterSpec(config *ClusterConfig) (*v1beta1.PostgresCluster, error) {
	cluster := &v1beta1.PostgresCluster{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "postgres-operator.crunchydata.com/v1beta1",
			Kind:       "PostgresCluster",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      config.Name,
			Namespace: config.Namespace,
			Labels: map[string]string{
				"environment": config.Environment,
			},
		},
		Spec: v1beta1.PostgresClusterSpec{
			PostgresVersion: int32(config.PostgresVersion),
			Image:           fmt.Sprintf("registry.developers.crunchydata.com/crunchydata/crunchy-postgres:ubi8-%d-latest", config.PostgresVersion),
			InstanceSets: []v1beta1.PostgresInstanceSetSpec{
				{
					Name:     "instance1",
					Replicas: int32Ptr(int32(config.Replicas)),
					DataVolumeClaimSpec: v1beta1.VolumeClaimSpecWithAutoGrow{
						VolumeClaimSpec: v1beta1.VolumeClaimSpec{
						AccessModes: []corev1.PersistentVolumeAccessMode{
							corev1.ReadWriteOnce,
						},
						Resources: corev1.VolumeResourceRequirements{
							Requests: corev1.ResourceList{
								corev1.ResourceStorage: resource.MustParse(config.StorageSize),
							},
						},
					},
					},
					Resources: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse(config.CPURequest),
							corev1.ResourceMemory: resource.MustParse(config.MemoryRequest),
						},
						Limits: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse(config.CPULimit),
							corev1.ResourceMemory: resource.MustParse(config.MemoryLimit),
						},
					},
				},
			},
			Backups: v1beta1.Backups{
				PGBackRest: v1beta1.PGBackRestArchive{
					Image: "registry.developers.crunchydata.com/crunchydata/crunchy-pgbackrest:ubi8-latest",
				},
			},
		},
	}

	// Configure backups if enabled
	if config.EnableBackup {
		cluster.Spec.Backups.PGBackRest.Repos = []v1beta1.PGBackRestRepo{
			{
				Name: "repo1",
				Volume: &v1beta1.RepoPVC{
					VolumeClaimSpec: v1beta1.VolumeClaimSpecWithAutoGrow{
						VolumeClaimSpec: v1beta1.VolumeClaimSpec{
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
				BackupSchedules: &v1beta1.PGBackRestBackupSchedules{
					Full: strPtr(config.BackupSchedule),
				},
			},
		}
	}

	// Configure monitoring if enabled
	if config.EnableMonitoring {
		cluster.Spec.Monitoring = &v1beta1.MonitoringSpec{
			PGMonitor: &v1beta1.PGMonitorSpec{
				Exporter: &v1beta1.ExporterSpec{
					Image: "registry.developers.crunchydata.com/crunchydata/crunchy-postgres-exporter:ubi8-latest",
				},
			},
		}
	}

	// Configure pgBouncer if enabled
	if config.EnablePgBouncer {
		cluster.Spec.Proxy = &v1beta1.PostgresProxySpec{
			PGBouncer: &v1beta1.PGBouncerPodSpec{
				Image:    "registry.developers.crunchydata.com/crunchydata/crunchy-pgbouncer:ubi8-latest",
				Replicas: int32Ptr(2),
			},
		}
	}

	return cluster, nil
}

// applyCluster applies cluster to Kubernetes
func applyCluster(ctx context.Context, yamlData []byte) error {
	kubectlArgs := []string{"apply", "-f", "-"}
	if *Kubeconfig != "" {
		kubectlArgs = append([]string{"--kubeconfig", *Kubeconfig}, kubectlArgs...)
	}

	cmd := exec.CommandContext(ctx, "kubectl", kubectlArgs...)
	cmd.Stdin = bytes.NewReader(yamlData)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("kubectl apply failed: %w\nOutput: %s", err, string(output))
	}

	return nil
}

// Validation functions
func validateClusterName(val interface{}) error {
	name := val.(string)
	pattern := regexp.MustCompile(`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`)
	if !pattern.MatchString(name) {
		return fmt.Errorf("name must be lowercase alphanumeric with hyphens")
	}
	if len(name) > 50 {
		return fmt.Errorf("name must be 50 characters or less")
	}
	return nil
}

func validateResource(val interface{}) error {
	str := val.(string)
	_, err := resource.ParseQuantity(str)
	if err != nil {
		return fmt.Errorf("invalid resource quantity: %w", err)
	}
	return nil
}

func validatePositiveInt(val interface{}) error {
	str := val.(string)
	num, err := strconv.Atoi(str)
	if err != nil {
		return fmt.Errorf("must be a number")
	}
	if num <= 0 {
		return fmt.Errorf("must be positive")
	}
	return nil
}

// Helper functions
func extractVersion(choice string) int {
	parts := strings.Split(choice, " ")
	version, _ := strconv.Atoi(parts[0])
	return version
}

func contains(slice []string, str string) bool {
	for _, s := range slice {
		if s == str {
			return true
		}
	}
	return false
}

func getDefaultFeatures(config *ClusterConfig) []string {
	defaults := []string{}
	if config.EnableHA {
		defaults = append(defaults, "High Availability (multiple replicas)")
	}
	if config.EnableBackup {
		defaults = append(defaults, "Automated Backups (pgBackRest)")
	}
	if config.EnableMonitoring {
		defaults = append(defaults, "Monitoring (pgMonitor)")
	}
	if config.EnablePgBouncer {
		defaults = append(defaults, "Connection Pooling (pgBouncer)")
	}
	if config.EnableTLS {
		defaults = append(defaults, "TLS Encryption")
	}
	return defaults
}

func int32Ptr(i int32) *int32 {
	return &i
}

func strPtr(s string) *string {
	return &s
}
