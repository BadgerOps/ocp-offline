package main

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/spf13/cobra"
)

var (
	validateAll      bool
	validateProvider string
)

func newValidateCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "validate",
		Short: "Validate local content integrity against manifests",
		Long: `Validate local content integrity against provider manifests. This command
checks that all downloaded files have correct checksums and reports any
missing or corrupted files.

Without --provider or --all, validates all enabled providers.`,
		Example: `  airgap validate --all
  airgap validate --provider epel
  airgap validate --provider ocp-binaries,rhcos`,
		RunE: validateRun,
	}

	cmd.Flags().BoolVar(&validateAll, "all", false, "validate all enabled providers")
	cmd.Flags().StringVar(&validateProvider, "provider", "", "comma-separated list of providers to validate")

	return cmd
}

func validateRun(cmd *cobra.Command, args []string) error {
	log := slog.Default()

	if globalCfg == nil {
		return fmt.Errorf("config not loaded")
	}

	if globalEngine == nil {
		return fmt.Errorf("sync engine not initialized")
	}

	var providers []string

	if validateAll {
		for name := range globalCfg.Providers {
			providers = append(providers, name)
		}
	} else if validateProvider != "" {
		providers = strings.Split(validateProvider, ",")
		for i, p := range providers {
			providers[i] = strings.TrimSpace(p)
		}
	} else {
		// Validate all enabled providers by default
		for name := range globalCfg.Providers {
			if globalCfg.ProviderEnabled(name) {
				providers = append(providers, name)
			}
		}
	}

	if len(providers) == 0 {
		log.Warn("no providers to validate")
		return nil
	}

	log.Info("validate operation", "providers", providers)

	ctx := context.Background()
	totalValid := 0
	totalInvalid := 0

	fmt.Println("Validating providers...")
	fmt.Println()

	for _, providerName := range providers {
		log.Info("validating provider", "provider", providerName)

		report, err := globalEngine.ValidateProvider(ctx, providerName)
		if err != nil {
			fmt.Printf("%s: ERROR - %v\n", providerName, err)
			totalInvalid++
			continue
		}

		totalValid += report.ValidFiles
		totalInvalid += len(report.InvalidFiles)

		// Print provider report
		fmt.Printf("%s:\n", providerName)
		fmt.Printf("  Total Files:   %d\n", report.TotalFiles)
		fmt.Printf("  Valid Files:   %d\n", report.ValidFiles)
		fmt.Printf("  Invalid Files: %d\n", len(report.InvalidFiles))

		if len(report.InvalidFiles) > 0 {
			fmt.Println("  Invalid files:")
			for _, inv := range report.InvalidFiles {
				fmt.Printf("    - %s\n", inv.Path)
				if inv.Expected != "" && inv.Actual != "" {
					fmt.Printf("      Expected: %s\n", inv.Expected)
					fmt.Printf("      Actual:   %s\n", inv.Actual)
				}
			}
		}
		fmt.Println()
	}

	// Print summary
	fmt.Println("=== VALIDATION SUMMARY ===")
	fmt.Printf("Total Valid:   %d\n", totalValid)
	fmt.Printf("Total Invalid: %d\n", totalInvalid)

	if totalInvalid > 0 {
		return fmt.Errorf("validation failed: %d invalid files", totalInvalid)
	}

	return nil
}
