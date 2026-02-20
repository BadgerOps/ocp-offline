package main

import (
	"fmt"
	"log/slog"
	"strings"

	"github.com/spf13/cobra"
)

var (
	statusProvider string
	statusFailed   bool
)

func newStatusCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Display synchronization status of providers",
		Long: `Display the current synchronization status of all or specific providers.
Shows file counts, sizes, last sync time, and any failed files.

Use --provider to check specific providers, or --failed to show only
providers with failures.`,
		Example: `  airgap status
  airgap status --provider epel
  airgap status --provider ocp-binaries,rhcos
  airgap status --failed`,
		RunE: statusRun,
	}

	cmd.Flags().StringVar(&statusProvider, "provider", "", "comma-separated list of providers to show status for")
	cmd.Flags().BoolVar(&statusFailed, "failed", false, "show only providers with failed files")

	return cmd
}

func statusRun(cmd *cobra.Command, args []string) error {
	log := slog.Default()

	if globalCfg == nil {
		return fmt.Errorf("config not loaded")
	}

	if globalEngine == nil {
		return fmt.Errorf("sync engine not initialized")
	}

	var providers []string

	if statusProvider != "" {
		providers = strings.Split(statusProvider, ",")
		for i, p := range providers {
			providers[i] = strings.TrimSpace(p)
		}
	} else {
		for name := range globalCfg.Providers {
			providers = append(providers, name)
		}
	}

	if len(providers) == 0 {
		log.Warn("no providers found")
		return nil
	}

	log.Info("status request", "providers", providers, "failed_only", statusFailed)

	// Get status from engine
	statuses := globalEngine.Status()

	// Filter providers
	var filteredStatuses []string
	for _, p := range providers {
		if _, ok := statuses[p]; ok {
			if statusFailed && statuses[p].FailedFiles == 0 {
				continue
			}
			filteredStatuses = append(filteredStatuses, p)
		}
	}

	if len(filteredStatuses) == 0 {
		fmt.Println("No providers found matching criteria")
		return nil
	}

	// Print table header
	fmt.Println("Provider Status")
	fmt.Println("===============")
	fmt.Println("")
	fmt.Printf("%-20s %10s %12s %10s %12s\n", "Provider", "Files", "Size", "Failed", "Last Sync")
	fmt.Println(strings.Repeat("-", 70))

	// Print each provider status
	for _, providerName := range filteredStatuses {
		status := statuses[providerName]

		sizeStr := formatBytes(status.TotalSize)
		lastSyncStr := "never"
		if !status.LastSync.IsZero() {
			lastSyncStr = status.LastSync.Format("2006-01-02 15:04")
		}

		fmt.Printf("%-20s %10d %12s %10d %12s\n",
			providerName,
			status.FileCount,
			sizeStr,
			status.FailedFiles,
			lastSyncStr,
		)
	}

	fmt.Println("")

	return nil
}

// formatBytes formats a byte count into human-readable format
func formatBytes(bytes int64) string {
	const unit = 1024
	if bytes < unit {
		return fmt.Sprintf("%d B", bytes)
	}

	div, exp := int64(unit), 0
	for n := bytes / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}

	return fmt.Sprintf("%.1f %cB", float64(bytes)/float64(div), "KMGTPE"[exp])
}
