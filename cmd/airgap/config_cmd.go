package main

import (
	"fmt"
	"log/slog"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

func newConfigCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Manage configuration",
		Long: `Manage airgap configuration. Subcommands allow viewing and modifying
configuration settings.`,
		Example: `  airgap config show
  airgap config set server.listen 127.0.0.1:9000`,
	}

	cmd.AddCommand(
		newConfigShowCmd(),
		newConfigSetCmd(),
	)

	return cmd
}

func newConfigShowCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "show",
		Short: "Display current configuration",
		Long: `Display the current configuration in YAML format. If a config file
is loaded, shows the loaded configuration with any command-line overrides
applied.`,
		Example: `  airgap config show
  airgap config show --config /etc/airgap/config.yaml`,
		RunE: configShowRun,
	}

	return cmd
}

func configShowRun(cmd *cobra.Command, args []string) error {
	log := slog.Default()

	if globalCfg == nil {
		return fmt.Errorf("config not loaded")
	}

	log.Info("showing configuration")

	data, err := yaml.Marshal(globalCfg)
	if err != nil {
		return fmt.Errorf("failed to marshal config: %w", err)
	}

	fmt.Println("Current Configuration:")
	fmt.Println("======================")
	fmt.Println(string(data))

	return nil
}

func newConfigSetCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "set KEY VALUE",
		Short: "Set a configuration value",
		Long: `Set a configuration value using dot-notation for nested keys.
Changes are written back to the config file.

Examples:
  server.listen 127.0.0.1:9000
  server.data_dir /var/lib/airgap
  export.split_size 10GB
  export.compression gzip`,
		Example: `  airgap config set server.listen 127.0.0.1:9000
  airgap config set export.split_size 10GB`,
		Args: cobra.ExactArgs(2),
		RunE: configSetRun,
	}

	return cmd
}

func configSetRun(cmd *cobra.Command, args []string) error {
	log := slog.Default()

	if globalCfg == nil {
		return fmt.Errorf("config not loaded")
	}

	key := args[0]
	value := args[1]

	log.Info("set configuration", "key", key, "value", value)

	fmt.Printf("STUB: Would set config key '%s' to '%s'\n", key, value)

	return nil
}
