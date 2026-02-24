package main

import (
	"fmt"
	"sort"
	"strings"

	"github.com/spf13/cobra"
)

func newProvidersCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "providers",
		Short: "Inspect configured providers",
		Long: `Inspect configured provider definitions stored in the local database.
Use "providers list" to see names, types, and enabled state.`,
		RunE: providersListRun,
	}

	cmd.AddCommand(newProvidersListCmd())
	return cmd
}

func newProvidersListCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List configured providers",
		Long:    "List all configured providers, including provider type and whether each one is enabled.",
		RunE:    providersListRun,
	}
}

func providersListRun(cmd *cobra.Command, args []string) error {
	if globalStore == nil {
		return fmt.Errorf("store not initialized")
	}

	configs, err := globalStore.ListProviderConfigs()
	if err != nil {
		return fmt.Errorf("listing provider configs: %w", err)
	}

	if len(configs) == 0 {
		fmt.Println("No providers configured.")
		return nil
	}

	sort.Slice(configs, func(i, j int) bool {
		return configs[i].Name < configs[j].Name
	})

	fmt.Println("Configured Providers")
	fmt.Println("====================")
	fmt.Println("")
	fmt.Printf("%-24s %-18s %-8s %-10s\n", "Name", "Type", "Enabled", "Loaded")
	fmt.Println(strings.Repeat("-", 66))

	for _, pc := range configs {
		enabled := "no"
		if pc.Enabled {
			enabled = "yes"
		}

		loaded := "no"
		if globalRegistry != nil {
			if _, ok := globalRegistry.Get(pc.Name); ok {
				loaded = "yes"
			}
		}

		fmt.Printf("%-24s %-18s %-8s %-10s\n", pc.Name, pc.Type, enabled, loaded)
	}
	fmt.Println("")

	return nil
}
