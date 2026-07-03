package main

import (
	"errors"
	"fmt"

	"github.com/agentberlin/bluesnake/internal/config"
	"github.com/agentberlin/bluesnake/internal/queue"
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

// crawlBase resolves the spider crawl's base config from the --setup /
// --profile / --config trio, and describes the resolution so the command can
// say out loud which setup a crawl is about to run with (#88 makes the
// implicit default history-dependent, so it must never be silent):
//
//	--profile / --config    explicit base, wins; --setup must stay untouched
//	--setup last (default)  the seed site's last-crawl setup; app settings
//	                        (the saved default profile) when never crawled
//	--setup app             the app settings, explicitly
//	--setup defaults        built-in defaults — the pinned, history-free base
//	                        (what a bare crawl meant before #88; use in CI)
func crawlBase(storeDir, setup string, setupSet bool, profile, cfgFile, seedURL string) (*config.Config, string, error) {
	if profile != "" || cfgFile != "" {
		if setupSet {
			return nil, "", errors.New("--setup is mutually exclusive with --profile/--config (they ARE the setup)")
		}
		cfg, err := baseConfig(storeDir, profile, cfgFile)
		return cfg, "", err
	}
	switch setup {
	case "defaults":
		return config.Default(), "setup: built-in defaults", nil
	case "app":
		cfg, err := runner.LoadProfile(storeDir, "")
		return cfg, "setup: app settings", err
	case "last":
		cfg, src, err := runner.ResolveBase(storeDir, queue.JobSpec{URL: seedURL, ConfigSource: "last"})
		if err != nil {
			return nil, "", err
		}
		if src.Kind == "last" {
			return cfg, fmt.Sprintf("setup: last crawl of this site (%s, %s) — --setup app|defaults, --profile or --config override",
				src.CrawlID, src.Started.Format("2006-01-02")), nil
		}
		return cfg, "setup: app settings (site not crawled before)", nil
	default:
		return nil, "", fmt.Errorf("--setup must be last, app or defaults (got %q)", setup)
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
					// every surface's base when a site has no last-crawl setup
					// and no profile is named (`crawl --setup app` selects it
					// explicitly; `--setup defaults` bypasses it for the pinned
					// built-ins)
					fmt.Fprintf(cmd.OutOrStdout(), "%s  (the app settings — the fallback base on every surface when a site has no last-crawl setup)\n", n)
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
