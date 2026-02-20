package main

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/BadgerOps/airgap/internal/provider"
	"github.com/spf13/cobra"
)

var (
	syncAll      bool
	syncProvider string
	syncDryRun   bool
	syncForce    bool
)

func newSyncCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "sync",
		Short: "Synchronize content from configured providers",
		Long: `Synchronize content from configured providers. By default, syncs all enabled
providers. Use --provider to sync specific providers.

The sync command will:
  1. Compare upstream manifests with local content
  2. Plan required downloads, deletions, and updates
  3. Execute the plan (or show it with --dry-run)
  4. Validate checksums for all downloaded files
  5. Retry failed downloads based on configuration

Without --all or --provider, all enabled providers are synced.`,
		Example: `  airgap sync --all
  airgap sync --provider epel,ocp-binaries
  airgap sync --provider rhcos --dry-run
  airgap sync --provider container-images --force`,
		RunE: syncRun,
	}

	cmd.Flags().BoolVar(&syncAll, "all", false, "sync all enabled providers")
	cmd.Flags().StringVar(&syncProvider, "provider", "", "comma-separated list of providers to sync")
	cmd.Flags().BoolVar(&syncDryRun, "dry-run", false, "show what would be done without making changes")
	cmd.Flags().BoolVar(&syncForce, "force", false, "force re-download of all files regardless of checksums")

	return cmd
}

func syncRun(cmd *cobra.Command, args []string) error {
	log := slog.Default()

	if globalCfg == nil {
		return fmt.Errorf("config not loaded")
	}

	if globalEngine == nil {
		return fmt.Errorf("sync engine not initialized")
	}

	var providers []string

	if syncAll {
		for name := range globalCfg.Providers {
			providers = append(providers, name)
		}
	} else if syncProvider != "" {
		providers = strings.Split(syncProvider, ",")
		for i, p := range providers {
			providers[i] = strings.TrimSpace(p)
		}
	} else {
		// Sync all enabled providers by default
		for name := range globalCfg.Providers {
			if globalCfg.ProviderEnabled(name) {
				providers = append(providers, name)
			}
		}
	}

	if len(providers) == 0 {
		log.Warn("no providers to sync")
		return nil
	}

	log.Info("sync operation", "providers", providers, "dry_run", syncDryRun, "force", syncForce)

	// Build sync options
	opts := provider.SyncOptions{
		DryRun:     syncDryRun,
		Force:      syncForce,
		MaxWorkers: 4, // Default worker count
	}

	ctx := context.Background()

	// Display results
	if syncDryRun {
		fmt.Println("DRY RUN: Sync will perform the following actions:")
	}

	totalDownloaded := 0
	totalSkipped := 0
	totalFailed := 0
	totalDeleted := 0

	// Sync each provider
	for _, providerName := range providers {
		log.Info("syncing provider", "provider", providerName)

		report, err := globalEngine.SyncProvider(ctx, providerName, opts)
		if err != nil {
			fmt.Printf("  ERROR: %s - %v\n", providerName, err)
			totalFailed++
			continue
		}

		totalDownloaded += report.Downloaded
		totalSkipped += report.Skipped
		totalDeleted += report.Deleted
		totalFailed += len(report.Failed)

		// Print provider report
		fmt.Printf("\n%s:\n", providerName)
		fmt.Printf("  Downloaded: %d\n", report.Downloaded)
		fmt.Printf("  Skipped:    %d\n", report.Skipped)
		fmt.Printf("  Deleted:    %d\n", report.Deleted)
		fmt.Printf("  Failed:     %d\n", len(report.Failed))
		fmt.Printf("  Bytes:      %d\n", report.BytesTransferred)

		if len(report.Failed) > 0 {
			fmt.Println("  Failed files:")
			for _, ff := range report.Failed {
				fmt.Printf("    - %s: %s\n", ff.Path, ff.Error)
			}
		}
	}

	// Print summary
	fmt.Println("\n=== SYNC SUMMARY ===")
	fmt.Printf("Total Downloaded: %d\n", totalDownloaded)
	fmt.Printf("Total Skipped:    %d\n", totalSkipped)
	fmt.Printf("Total Deleted:    %d\n", totalDeleted)
	fmt.Printf("Total Failed:     %d\n", totalFailed)

	if totalFailed > 0 {
		return fmt.Errorf("sync completed with %d failures", totalFailed)
	}

	return nil
}
