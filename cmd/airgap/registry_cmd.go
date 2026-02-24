package main

import (
	"fmt"
	"time"

	"github.com/BadgerOps/airgap/internal/engine"
	"github.com/spf13/cobra"
)

var (
	registryPushSource string
	registryPushTarget string
	registryPushDryRun bool
)

func newRegistryCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "registry",
		Short: "Registry operations for mirrored container images",
	}

	cmd.AddCommand(newRegistryPushCmd())
	return cmd
}

func newRegistryPushCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "push",
		Short: "Push mirrored container images to a destination registry",
		Long: `Pushes images mirrored by a container_images provider into a target registry
provider definition (credentials and endpoint configured in provider settings).`,
		Example: `  airgap registry push --source-provider container-images --target-provider quay-prod
  airgap registry push --source-provider container-images --target-provider lab-registry --dry-run`,
		RunE: registryPushRun,
	}

	cmd.Flags().StringVar(&registryPushSource, "source-provider", "", "container_images provider name to push from (required)")
	cmd.Flags().StringVar(&registryPushTarget, "target-provider", "", "registry provider name to push to (required)")
	cmd.Flags().BoolVar(&registryPushDryRun, "dry-run", false, "plan the push without executing skopeo copy")
	_ = cmd.MarkFlagRequired("source-provider")
	_ = cmd.MarkFlagRequired("target-provider")

	return cmd
}

func registryPushRun(cmd *cobra.Command, args []string) error {
	if globalEngine == nil {
		return fmt.Errorf("engine not initialized")
	}

	fmt.Printf("Pushing images from %q to registry target %q...\n", registryPushSource, registryPushTarget)
	if registryPushDryRun {
		fmt.Println("  Mode: dry run")
	}
	fmt.Println()

	report, err := globalEngine.PushContainerImages(cmd.Context(), engine.RegistryPushOptions{
		SourceProvider: registryPushSource,
		TargetProvider: registryPushTarget,
		DryRun:         registryPushDryRun,
	})
	if report != nil {
		printRegistryPushReport(report)
	}
	if err != nil {
		return fmt.Errorf("registry push failed: %w", err)
	}
	return nil
}

func printRegistryPushReport(report *engine.RegistryPushReport) {
	fmt.Println("Registry push results:")
	fmt.Printf("  Source provider: %s\n", report.SourceProvider)
	fmt.Printf("  Target provider: %s\n", report.TargetProvider)
	fmt.Printf("  Images total: %d\n", report.ImagesTotal)
	fmt.Printf("  Images pushed: %d\n", report.ImagesPushed)
	fmt.Printf("  Blobs processed: %d\n", report.BlobsProcessed)
	fmt.Printf("  Manifests pushed: %d\n", report.ManifestsPushed)
	fmt.Printf("  Duration: %s\n", report.Duration.Round(time.Second))
	if len(report.Failures) > 0 {
		fmt.Printf("  Failures: %d\n", len(report.Failures))
		for _, f := range report.Failures {
			fmt.Printf("    - %s\n", f)
		}
	}
	fmt.Println()
}
