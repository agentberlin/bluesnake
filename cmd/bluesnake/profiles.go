package main

import (
	"errors"
	"fmt"

	"github.com/agentberlin/bluesnake/internal/config"
	"github.com/agentberlin/bluesnake/internal/runner"
	"github.com/spf13/cobra"
)

// The CLI reads and uses profiles — the same named configs the desktop app
// manages and MCP's list_profiles/get_profile_config expose — but does not
// create, edit, or delete them: authoring stays in the desktop Settings page
// (or point --config at a plain YAML file instead).

// baseConfig resolves a command's base config: a named profile (--profile), a
// config file (--config), or the built-in defaults. The two explicit sources
// are mutually exclusive — a profile IS a saved config file, so combining
// them would be ambiguous. Dotted --set overrides apply on top at each caller.
func baseConfig(storeDir, profile, cfgFile string) (*config.Config, error) {
	switch {
	case profile != "" && cfgFile != "":
		return nil, errors.New("--profile and --config are mutually exclusive (a profile is a saved config)")
	case profile != "":
		return runner.LoadProfile(storeDir, profile)
	case cfgFile != "":
		return config.LoadFile(cfgFile)
	default:
		return config.Default(), nil
	}
}

// newProfilesCmd lists the saved profiles, default (the app settings) first —
// the CLI twin of MCP's list_profiles.
func newProfilesCmd() *cobra.Command {
	var storeDir string
	cmd := &cobra.Command{
		Use:   "profiles",
		Short: "List saved config profiles (shared with the desktop app and MCP)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			names := runner.ListProfileNames(storeDir)
			if len(names) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "no profiles saved — the desktop app's Settings page creates them")
				return nil
			}
			for _, n := range names {
				if n == runner.DefaultProfileName {
					// the desktop/MCP default; the CLI itself stays on built-in
					// defaults unless --profile names it, so say so precisely
					fmt.Fprintf(cmd.OutOrStdout(), "%s  (the app settings — what the desktop and MCP use unless a profile is named)\n", n)
				} else {
					fmt.Fprintln(cmd.OutOrStdout(), n)
				}
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&storeDir, "store-dir", defaultStoreDir(), "crawl storage directory")
	return cmd
}
