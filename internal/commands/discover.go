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

// Package commands contains the auto-discovery logic that builds cobra
// commands dynamically from the playbook header annotations in playbooks/*.yml.
// Adding a new playbook automatically makes it available as a CLI command —
// no Go code needs to be written or modified.
package commands

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"github.com/valet-sh/cli/internal/ansible"
)

// playbookMeta holds the parsed metadata from a playbook file's header
// comment block. All fields come directly from the @annotation lines that
// sit before the first YAML `---` document separator.
type playbookMeta struct {
	// command is the canonical command name from @command, e.g. "project:env".
	command string
	// description is the one-line summary from @description, shown in help lists.
	description string
	// usage is the full usage string from @usage, e.g. "valet.sh service <action> [service]".
	usage string
	// helpText is the multi-line body from @help lines, shown in --help Long description.
	helpText string
}

// Discover scans the playbooks/ directory under repoDir, parses each
// playbook's header annotations, and returns a slice of *cobra.Command
// ready to be added to the root command.
//
// Playbooks whose names contain a colon (e.g. project:env.yml) are grouped
// under a parent command (e.g. "project") with subcommands for each variant
// (e.g. "env", "cc"). The parent command itself only shows help.
//
// Callers should apply ApplyHooks() to the returned commands before
// registering them so that pre-run validation (e.g. .valet-sh.yml checks)
// is attached to the appropriate commands.
func Discover(repoDir string) ([]*cobra.Command, error) {
	playbooksDir := filepath.Join(repoDir, "playbooks")

	entries, err := os.ReadDir(playbooksDir)
	if err != nil {
		return nil, fmt.Errorf("reading playbooks directory %s: %w", playbooksDir, err)
	}

	// parents collects grouped subcommands keyed by parent name (e.g. "project").
	parents := map[string]*cobra.Command{}
	// parentSubs tracks sub-commands for each parent.
	parentSubs := map[string][]*cobra.Command{}

	var topLevel []*cobra.Command

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !strings.HasSuffix(name, ".yml") {
			continue
		}

		playbookPath := filepath.Join(playbooksDir, name)
		meta, err := parsePlaybookHeader(playbookPath)
		if err != nil {
			// Non-fatal: skip unreadable/unparseable playbooks.
			continue
		}
		if meta.command == "" {
			// No @command annotation — not a CLI command.
			continue
		}

		cmd := buildCommand(*meta)

		if strings.Contains(meta.command, ":") {
			// e.g. "project:env" → parent="project", sub="env"
			parts := strings.SplitN(meta.command, ":", 2)
			parentName := parts[0]

			if _, exists := parents[parentName]; !exists {
				parent := &cobra.Command{
					Use:   parentName,
					Short: parentName + " commands",
					Annotations: map[string]string{
						"playbook-group": "true",
					},
				}
				parents[parentName] = parent
			}
			parentSubs[parentName] = append(parentSubs[parentName], cmd)
		} else {
			topLevel = append(topLevel, cmd)
		}
	}

	// Wire up parent → subcommand relationships.
	for parentName, parent := range parents {
		for _, sub := range parentSubs[parentName] {
			parent.AddCommand(sub)
		}
		topLevel = append(topLevel, parent)
	}

	return topLevel, nil
}

// buildCommand creates a *cobra.Command from the parsed playbookMeta.
// The command's RunE calls ansible.Run with the playbook name extracted
// from the @command annotation (colons preserved, e.g. "project:env").
func buildCommand(meta playbookMeta) *cobra.Command {
	// Derive the cobra Use string from @usage if available, otherwise fall
	// back to just the subcommand name (last segment after any colon).
	use := commandUse(meta)

	// Build a clean Long description from @help text.
	long := buildLong(meta)

	playbook := meta.command // e.g. "service" or "project:env"

	cmd := &cobra.Command{
		Use:                use,
		Short:              meta.description,
		Long:               long,
		Args:               cobra.ArbitraryArgs,
		DisableFlagParsing: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			workDir, err := os.Getwd()
			if err != nil {
				return fmt.Errorf("getting working directory: %w", err)
			}
			positional, opts, verbose, debug := ansible.SplitTokens(args)
			return ansible.Run(&ansible.RunOpts{
				Playbook: playbook,
				Args:     positional,
				Opts:     opts,
				WorkDir:  workDir,
				Verbose:  verbose,
				Debug:    debug,
			})
		},
	}

	// Store the playbook name in Annotations so hooks can identify commands
	// without parsing the Use string.
	cmd.Annotations = map[string]string{
		"playbook": playbook,
	}

	cmd.Flags().BoolP("verbose", "v", false, "Enable verbose Ansible output")
	cmd.Flags().BoolP("debug", "d", false, "Enable debug output")

	return cmd
}

// commandUse derives the cobra Use string for a command.
//
// For a top-level command (no colon), it strips the "valet.sh " prefix from
// @usage so only the subcommand portion remains, e.g.:
//
//	"valet.sh service <start|stop> [svc]" → "service <start|stop> [svc]"
//
// For a grouped subcommand (has colon), it strips both the prefix and the
// parent segment so only the subcommand leaf is used, e.g.:
//
//	"valet.sh project:env" → "env"
//
// Falls back to the leaf command name when @usage is absent or unrecognisable.
func commandUse(meta playbookMeta) string {
	leaf := meta.command
	if idx := strings.LastIndex(leaf, ":"); idx >= 0 {
		leaf = leaf[idx+1:]
	}

	if meta.usage == "" {
		return leaf
	}

	// Strip common prefixes: "valet.sh ", "valet ", "./"
	use := meta.usage
	for _, prefix := range []string{"valet.sh ", "valet ", "./"} {
		if after, ok := strings.CutPrefix(use, prefix); ok {
			use = after
			break
		}
	}

	// For subcommands (colon in original command), strip "parent:" prefix from use.
	if strings.Contains(meta.command, ":") {
		parts := strings.SplitN(meta.command, ":", 2)
		parentPrefix := parts[0] + ":"
		if strings.HasPrefix(use, parentPrefix) {
			use = use[len(parentPrefix):]
		} else if strings.HasPrefix(use, parts[0]+" ") {
			// e.g. "project env" without colon
			use = use[len(parts[0])+1:]
		}
	}

	if use == "" {
		return leaf
	}
	return use
}

// buildLong constructs a clean Long description from the playbook metadata.
// If @help text is available it is used; otherwise falls back to @description.
func buildLong(meta playbookMeta) string {
	if meta.helpText != "" {
		if meta.description != "" {
			return meta.description + "\n\n" + meta.helpText
		}
		return meta.helpText
	}
	return meta.description
}

// parsePlaybookHeader reads the comment block at the top of a playbook file
// (everything before the first `---` YAML document separator) and extracts
// the structured annotations.
//
// Supported annotations:
//
//	# @command:     "service"
//	# @description: "start/stop or enable/disable a service"
//	# @usage:       "valet.sh service <action> [service]"
//	# @help:
//	# line 1 of help text
//	# line 2 of help text
//
// The @help block extends to the end of the comment block (i.e. until `---`
// or EOF). Blank comment lines (bare `#`) are preserved as empty lines in
// the help text.
func parsePlaybookHeader(path string) (*playbookMeta, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()

	meta := &playbookMeta{}

	inHelp := false
	var helpLines []string

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()

		// Stop at the YAML document separator.
		if strings.TrimSpace(line) == "---" {
			break
		}

		// Only process comment lines.
		if !strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}

		// Strip leading "# " or "#" to get the annotation content.
		content := strings.TrimSpace(line)
		content = strings.TrimPrefix(content, "#")
		content = strings.TrimPrefix(content, " ")

		// Once we're in @help mode, every subsequent comment line is help text.
		if inHelp {
			helpLines = append(helpLines, content)
			continue
		}

		// Parse known annotations.
		switch {
		case strings.HasPrefix(content, "@command:"):
			meta.command = extractAnnotationValue(content, "@command:")
		case strings.HasPrefix(content, "@description:"):
			meta.description = extractAnnotationValue(content, "@description:")
		case strings.HasPrefix(content, "@usage:"):
			meta.usage = extractAnnotationValue(content, "@usage:")
		case strings.HasPrefix(content, "@help:"):
			inHelp = true
			// Inline value after @help: (rare but handle it)
			val := extractAnnotationValue(content, "@help:")
			if val != "" {
				helpLines = append(helpLines, val)
			}
		case strings.HasPrefix(content, "@author:"), strings.HasPrefix(content, "@platform:"):
			// Informational annotations — not used by the CLI.
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, err
	}

	if len(helpLines) > 0 {
		meta.helpText = strings.TrimSpace(strings.Join(helpLines, "\n"))
	}

	return meta, nil
}

// extractAnnotationValue strips the annotation key and surrounding quotes from
// a value, e.g.:
//
//	`@command: "service"` → `service`
//	`@description: start/stop` → `start/stop`
func extractAnnotationValue(content, key string) string {
	val := strings.TrimPrefix(content, key)
	val = strings.TrimSpace(val)
	// Strip surrounding double quotes individually so an unclosed quote
	// (e.g. @description: "some text without closing quote) is still cleaned up.
	val = strings.TrimPrefix(val, `"`)
	val = strings.TrimSuffix(val, `"`)
	return strings.TrimSpace(val)
}
