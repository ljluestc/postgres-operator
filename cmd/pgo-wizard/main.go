// Copyright 2025 Crunchy Data Solutions, Inc.
//
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/crunchydata/postgres-operator/cmd/pgo-wizard/wizard"
)

var (
	// Version is set during build
	Version = "dev"

	// GitCommit is set during build
	GitCommit = "unknown"
)

func main() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

var rootCmd = &cobra.Command{
	Use:   "pgo-wizard",
	Short: "Interactive wizard for creating PostgreSQL clusters",
	Long: `pgo-wizard is an interactive command-line tool that guides you through
creating PostgreSQL clusters using the Crunchy Postgres Operator.

It provides:
  - Interactive prompts with smart defaults
  - Preset configurations (development, staging, production)
  - Validation during input
  - YAML generation
  - Direct cluster creation via kubectl

Examples:
  # Start interactive wizard
  pgo-wizard create

  # Use production preset
  pgo-wizard create --preset production

  # Generate YAML only (don't apply)
  pgo-wizard create --output cluster.yaml

  # Apply directly to cluster
  pgo-wizard create --apply
`,
	Version: fmt.Sprintf("%s (%s)", Version, GitCommit),
}

var createCmd = &cobra.Command{
	Use:   "create",
	Short: "Create a new PostgreSQL cluster interactively",
	Long:  `Interactive wizard to create a PostgreSQL cluster with smart defaults and validation.`,
	RunE:  wizard.RunCreateWizard,
}

var (
	// Global flags
	preset      string
	outputFile  string
	apply       bool
	namespace   string
	kubeconfig  string
	dryRun      bool
	nonInteractive bool
)

func init() {
	rootCmd.AddCommand(createCmd)

	// Flags for create command
	createCmd.Flags().StringVar(&preset, "preset", "", "Use preset configuration (development, staging, production)")
	createCmd.Flags().StringVarP(&outputFile, "output", "o", "", "Output YAML to file instead of stdout")
	createCmd.Flags().BoolVar(&apply, "apply", false, "Apply the generated manifest directly to the cluster")
	createCmd.Flags().StringVarP(&namespace, "namespace", "n", "default", "Kubernetes namespace")
	createCmd.Flags().StringVar(&kubeconfig, "kubeconfig", "", "Path to kubeconfig file")
	createCmd.Flags().BoolVar(&dryRun, "dry-run", false, "Preview YAML without creating cluster")
	createCmd.Flags().BoolVar(&nonInteractive, "non-interactive", false, "Skip prompts and use defaults/preset")

	// Pass flags to wizard package
	wizard.Preset = &preset
	wizard.OutputFile = &outputFile
	wizard.Apply = &apply
	wizard.Namespace = &namespace
	wizard.Kubeconfig = &kubeconfig
	wizard.DryRun = &dryRun
	wizard.NonInteractive = &nonInteractive
}
