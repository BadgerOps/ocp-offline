package main

import (
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/BadgerOps/airgap/internal/engine"
	"github.com/spf13/cobra"
)

var (
	exportTo          string
	exportProvider    string
	exportSplitSize   string
	exportCompression string
)

func newExportCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "export",
		Short: "Export synced content for transfer to offline environments",
		Long: `Export synced content for transfer to offline environments. Creates archives
or split files suitable for burning to media or transferring via sneakernet.

The --to flag is required and specifies the output directory. By default,
exports all enabled providers; use --provider to export specific ones.

Supports configurable split size (for multi-volume exports) and compression
formats (none, gzip, zstd).`,
		Example: `  airgap export --to /mnt/transfer-disk --all
  airgap export --to /mnt/usb --provider epel
  airgap export --to /mnt/transfer --provider container-images --split-size 4GB --compression zstd
  airgap export --to /mnt/external --provider rhcos --compression gzip`,
		RunE: exportRun,
	}

	cmd.Flags().StringVar(&exportTo, "to", "", "output directory for exported content (required)")
	cmd.Flags().StringVar(&exportProvider, "provider", "", "comma-separated list of providers to export")
	cmd.Flags().StringVar(&exportSplitSize, "split-size", "25GB", "split large archives into chunks of this size")
	cmd.Flags().StringVar(&exportCompression, "compression", "zstd", "compression format (none, gzip, zstd)")

	if err := cmd.MarkFlagRequired("to"); err != nil {
		panic(err)
	}

	return cmd
}

func exportRun(cmd *cobra.Command, args []string) error {
	log := slog.Default()

	if globalEngine == nil {
		return fmt.Errorf("engine not initialized")
	}

	var providers []string
	if exportProvider != "" {
		providers = strings.Split(exportProvider, ",")
		for i, p := range providers {
			providers[i] = strings.TrimSpace(p)
		}
	} else {
		for name := range globalCfg.Providers {
			if globalCfg.ProviderEnabled(name) {
				providers = append(providers, name)
			}
		}
	}

	if len(providers) == 0 {
		log.Warn("no providers to export")
		return nil
	}

	splitSize, err := engine.ParseSize(exportSplitSize)
	if err != nil {
		return fmt.Errorf("invalid split size %q: %w", exportSplitSize, err)
	}

	fmt.Printf("Exporting to %s...\n", exportTo)
	fmt.Printf("  Providers: %v\n", providers)
	fmt.Printf("  Split size: %s\n", exportSplitSize)
	fmt.Printf("  Compression: %s\n", exportCompression)
	fmt.Println()

	report, err := globalEngine.Export(cmd.Context(), engine.ExportOptions{
		OutputDir:   exportTo,
		Providers:   providers,
		SplitSize:   splitSize,
		Compression: exportCompression,
	})
	if err != nil {
		return fmt.Errorf("export failed: %w", err)
	}

	fmt.Printf("Export complete:\n")
	fmt.Printf("  Archives: %d\n", len(report.Archives))
	fmt.Printf("  Files: %d\n", report.TotalFiles)
	fmt.Printf("  Total size: %s\n", formatBytes(report.TotalSize))
	fmt.Printf("  Duration: %s\n", report.Duration.Round(time.Second))
	fmt.Printf("  Manifest: %s\n", report.ManifestPath)

	for _, arch := range report.Archives {
		fmt.Printf("  - %s (%s)\n", arch.Name, formatBytes(arch.Size))
	}

	return nil
}
