package main

import (
	"fmt"
	"time"

	"github.com/BadgerOps/airgap/internal/engine"
	"github.com/spf13/cobra"
)

var (
	importFrom          string
	importVerifyOnly    bool
	importForce         bool
	importSkipValidated bool
)

func newImportCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "import",
		Short: "Import exported content from offline media",
		Long: `Import previously exported content from offline media into the local
data directory. Automatically detects and validates manifests before importing.

Use --verify-only to check imports without actually writing files.
Use --force to overwrite existing files during import.
Use --skip-validated to skip re-validation of previously validated archives.`,
		Example: `  airgap import --from /mnt/usb
  airgap import --from /mnt/transfer-disk --verify-only
  airgap import --from /media/offline-backup --force`,
		RunE: importRun,
	}

	cmd.Flags().StringVar(&importFrom, "from", "", "source directory containing exported content (required)")
	cmd.Flags().BoolVar(&importVerifyOnly, "verify-only", false, "verify imports without writing files")
	cmd.Flags().BoolVar(&importForce, "force", false, "overwrite existing files during import")
	cmd.Flags().BoolVar(&importSkipValidated, "skip-validated", false, "skip re-validation of previously validated archives")

	if err := cmd.MarkFlagRequired("from"); err != nil {
		panic(err)
	}

	return cmd
}

func importRun(cmd *cobra.Command, args []string) error {
	if globalEngine == nil {
		return fmt.Errorf("engine not initialized")
	}

	fmt.Printf("Importing from %s...\n", importFrom)
	if importVerifyOnly {
		fmt.Println("  Mode: verify only")
	}
	if importForce {
		fmt.Println("  Mode: force (skip checksum verification)")
	}
	if importSkipValidated {
		fmt.Println("  Mode: skip previously validated archives")
	}
	fmt.Println()

	report, err := globalEngine.Import(cmd.Context(), engine.ImportOptions{
		SourceDir:     importFrom,
		VerifyOnly:    importVerifyOnly,
		Force:         importForce,
		SkipValidated: importSkipValidated,
	})
	if err != nil {
		// Still print partial report if available
		if report != nil {
			printImportReport(report)
		}
		return fmt.Errorf("import failed: %w", err)
	}

	printImportReport(report)
	return nil
}

func printImportReport(report *engine.ImportReport) {
	fmt.Printf("Import results:\n")
	fmt.Printf("  Archives validated: %d\n", report.ArchivesValidated)
	fmt.Printf("  Archives failed: %d\n", report.ArchivesFailed)
	if report.ArchivesSkipped > 0 {
		fmt.Printf("  Archives skipped (previously validated): %d\n", report.ArchivesSkipped)
	}
	fmt.Printf("  Files extracted: %d\n", report.FilesExtracted)
	fmt.Printf("  Total size: %s\n", formatBytes(report.TotalSize))
	fmt.Printf("  Duration: %s\n", report.Duration.Round(time.Second))
	if len(report.Errors) > 0 {
		fmt.Println("  Errors:")
		for _, e := range report.Errors {
			fmt.Printf("    - %s\n", e)
		}
	}
}
