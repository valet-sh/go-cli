// Copyright 2026 TechDivision GmbH
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"github.com/valet-sh/cli/internal/commands"
	"github.com/valet-sh/cli/internal/platform"
	"github.com/valet-sh/cli/internal/prechecks"
	"github.com/valet-sh/cli/internal/setup"
	"github.com/valet-sh/cli/internal/tui"
	"github.com/valet-sh/cli/internal/updater"
)

// Version is set at build time via -ldflags "-X main.Version=x.y.z".
var Version = "dev"

func main() {

	if err := prechecks.RequirementsCheck(); err != nil {
		fmt.Fprintln(os.Stderr, commands.ErrorPrefix(err.Error()))
		os.Exit(1)
	}

	vimMode := hasVIFlag(os.Args)
	if vimMode {
		os.Args = removeVIFlag(os.Args)
	}

	if err := updater.CheckMigration(platform.RepoDir()); err != nil {
		fmt.Fprintln(os.Stderr, commands.ErrorPrefix(err.Error()))
		os.Exit(1)
	}

	// Run the periodic update check before dispatching any command.
	// Skipped on --help / --version / -h invocations and when the user is
	// explicitly running self-upgrade (which is the update mechanism itself).
	if !updater.IsHelpOrVersionCall(os.Args) && !updater.IsSelfUpgradeCall(os.Args) {
		updater.Check(Version, os.Args, platform.RepoDir())
	}

	root := newRootCmd()

	// Launch TUI when: no arguments given, OR --vi flag was passed.
	if len(os.Args) == 1 || vimMode {
		result, err := tui.Run(root, Version, vimMode)
		if err != nil {
			fmt.Fprintf(os.Stderr, "TUI error: %v\n", err)
			os.Exit(1)
		}

		// The launcher quit with a selected command — dispatch it via RunWithPanel.
		// This runs on the clean terminal after BubbleTea has torn down, so
		// Ansible's vars_prompt (password) works natively before the exec panel
		// takes over.
		if len(result.Args) > 0 {
			if err := tui.RunWithPanel(root, result.Args, Version); err != nil {
				fmt.Fprintln(os.Stderr, commands.ErrorPrefix(err.Error()))
				os.Exit(1)
			}
		}
		os.Exit(0)
	}

	// Arguments present — show the execution panel on TTY, fall back to
	// direct cobra/ansible for non-TTY (CI, pipes).
	if err := tui.RunWithPanel(root, os.Args[1:], Version); err != nil {
		fmt.Fprintln(os.Stderr, commands.ErrorPrefix(err.Error()))
		os.Exit(1)
	}
}

func hasVIFlag(args []string) bool {
	for _, a := range args[1:] {
		if a == "--vi" || a == "-vi" {
			return true
		}
	}
	return false
}

func removeVIFlag(args []string) []string {
	result := make([]string, 0, len(args))
	for _, a := range args {
		if a != "--vi" && a != "-vi" {
			result = append(result, a)
		}
	}
	return result
}

func newRootCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "valet.sh",
		Short: "valet.sh — local dev environment manager for Magento and PHP projects",
		Long: `valet.sh manages your local development environment for Magento, Neos,
AEM, and other PHP-based projects. It handles multiple simultaneous versions
of PHP, MySQL/MariaDB, Elasticsearch/OpenSearch, Redis, RabbitMQ, and nginx
on both Ubuntu and macOS (Apple Silicon).

Configuration is driven by a .valet-sh.yml file in each project directory.`,
		SilenceUsage:      true,
		SilenceErrors:     true,
		Version:           Version,
		DisableAutoGenTag: true,
		CompletionOptions: cobra.CompletionOptions{DisableDefaultCmd: false},
		// No args at root → show help.
		// Unknown command → cobra calls RunE with the unknown token as an arg,
		// so we show help and exit cleanly rather than printing a confusing error.
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) > 0 {
				fmt.Fprintln(os.Stderr, commands.ErrorPrefix(fmt.Sprintf("unknown command %q", args[0])))
				fmt.Fprintln(os.Stderr)
			}
			return cmd.Help()
		},
	}

	// Install colored help formatter on root — cascades to all subcommands.
	commands.SetHelpFormatter(cmd)

	// Print version in the same style as the rest of the tool.
	cmd.SetVersionTemplate(fmt.Sprintf("valet.sh %s\n", Version))

	// Auto-discover subcommands from playbooks/*.yml header annotations.
	// Each playbook with a @command annotation becomes a cobra command.
	// Playbooks with colon-separated names (e.g. project:env) are grouped
	// under a parent command automatically.

	discovered, err := commands.Discover(platform.RepoDir())
	if err != nil {
		// Non-fatal: if playbooks dir is missing (e.g. first-time install
		// before valet-sh is cloned), the binary still starts and shows help.
		// fmt.Fprintf(os.Stderr, "warning: could not load commands from playbooks: %v\n", err)
	} else {
		commands.ApplyHooks(discovered)
		cmd.AddCommand(discovered...)
	}

	cmd.AddCommand(&cobra.Command{
		Use:          "setup",
		Short:        "Setup valet-sh",
		Long:         `Setup initializes valet-sh for the current user. It installs the valet-sh`,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return setup.Setup()
		},
	})

	cmd.AddCommand(&cobra.Command{
		Use:   "self-upgrade",
		Short: "Check for and apply updates to valet-sh CLI and playbooks",
		Long: `self-upgrade checks for new versions of both the valet-sh CLI binary and the
Ansible playbook repository, and applies updates if available.

Steps performed:
  1. Checks GitHub releases for a newer CLI binary
  2. Downloads and installs the binary with SHA256 verification
  3. Checks the valet-sh playbook repo for upstream commits
  4. Pulls the latest playbooks via git
  5. Re-executes your original command with the updated CLI if the binary changed`,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return updater.SelfUpgrade(Version, platform.RepoDir())
		},
	})

	cmd.AddCommand(&cobra.Command{
		Use:   "release-channel",
		Short: "Show or change the release channel for valet-sh updates",
		Long: `Shows or change the current release channel (branch) for valet-sh (Ansible playbooks and Runtime) updates.
	     `,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return updater.ReleaseChannel(platform.RepoDir())
		},
	})

	return cmd
}
