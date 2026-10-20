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

package commands

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/spf13/cobra"
)

// writePlaybook writes content to a temp file and returns its path.
func writePlaybook(t *testing.T, content string) string {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "*.yml")
	if err != nil {
		t.Fatalf("creating temp playbook: %v", err)
	}
	if _, err := f.WriteString(content); err != nil {
		t.Fatalf("writing temp playbook: %v", err)
	}
	_ = f.Close()
	return f.Name()
}

func TestParsePlaybookHeader_Full(t *testing.T) {
	path := writePlaybook(t, `#
# valet.sh | Service
#
# @author:      "Jane Dev"
# @platform:    macOS Ubuntu
# @command:     "service"
# @description: "start/stop or enable/disable a service"
# @usage:       "valet.sh service <start|stop> [svc]"
# @help:
#
# start: start a valet-sh service
# valet.sh service start mysql80
#
---
- hosts: localhost
`)

	meta, err := parsePlaybookHeader(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if meta.command != "service" {
		t.Errorf("command = %q, want %q", meta.command, "service")
	}
	if meta.description != "start/stop or enable/disable a service" {
		t.Errorf("description = %q", meta.description)
	}
	if meta.usage != "valet.sh service <start|stop> [svc]" {
		t.Errorf("usage = %q", meta.usage)
	}
	if meta.helpText == "" {
		t.Error("helpText should not be empty")
	}
}

func TestParsePlaybookHeader_MissingCommand(t *testing.T) {
	path := writePlaybook(t, `#
# @description: "no command annotation here"
# @usage:       "valet.sh something"
---
`)

	meta, err := parsePlaybookHeader(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if meta.command != "" {
		t.Errorf("expected empty command, got %q", meta.command)
	}
}

func TestParsePlaybookHeader_StopsAtSeparator(t *testing.T) {
	path := writePlaybook(t, `#
# @command: "before"
---
# @command: "after"
`)

	meta, err := parsePlaybookHeader(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if meta.command != "before" {
		t.Errorf("command = %q, want %q — parser should stop at ---", meta.command, "before")
	}
}

func TestParsePlaybookHeader_UnclosedQuote(t *testing.T) {
	// Mirrors real malformed playbooks (init.yml, init-instance.yml)
	path := writePlaybook(t, `#
# @command:     "init"
# @description: "creates a default .valet-sh.yml file in current directory
# @usage:       "valet.sh init
---
`)

	meta, err := parsePlaybookHeader(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if meta.description != "creates a default .valet-sh.yml file in current directory" {
		t.Errorf("description = %q — leading quote should be stripped even without closing quote", meta.description)
	}
	if meta.usage != "valet.sh init" {
		t.Errorf("usage = %q", meta.usage)
	}
}

func TestParsePlaybookHeader_HelpBody(t *testing.T) {
	path := writePlaybook(t, `#
# @command:     "link"
# @description: "link a project"
# @help:
#
# First help line
# Second help line
#
---
`)

	meta, err := parsePlaybookHeader(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if meta.helpText == "" {
		t.Fatal("helpText should not be empty")
	}
	// Description + blank line + help body
	long := buildLong(*meta)
	if long == "" {
		t.Error("buildLong should return non-empty string")
	}
	if long[:len(meta.description)] != meta.description {
		t.Errorf("buildLong should start with description, got %q", long)
	}
}

func TestParsePlaybookHeader_AuthorAndPlatformIgnored(t *testing.T) {
	path := writePlaybook(t, `#
# @author:      "Someone"
# @platform:    macOS
# @command:     "install"
# @description: "install something"
---
`)

	meta, err := parsePlaybookHeader(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// @author and @platform should not bleed into any field
	if meta.command != "install" {
		t.Errorf("command = %q", meta.command)
	}
	if meta.description != "install something" {
		t.Errorf("description = %q", meta.description)
	}
}

func TestParsePlaybookHeader_SubCommand(t *testing.T) {
	path := writePlaybook(t, `#
# @command:     "project:env"
# @description: "deploy project env config"
# @usage:       "valet.sh project:env"
---
`)

	meta, err := parsePlaybookHeader(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if meta.command != "project:env" {
		t.Errorf("command = %q", meta.command)
	}
}

func TestCommandUse_TopLevel(t *testing.T) {
	meta := playbookMeta{
		command: "service",
		usage:   "valet.sh service <start|stop> [svc]",
	}
	got := commandUse(meta)
	want := "service <start|stop> [svc]"
	if got != want {
		t.Errorf("commandUse = %q, want %q", got, want)
	}
}

func TestCommandUse_SubCommand(t *testing.T) {
	meta := playbookMeta{
		command: "project:env",
		usage:   "valet.sh project:env",
	}
	got := commandUse(meta)
	want := "env"
	if got != want {
		t.Errorf("commandUse = %q, want %q", got, want)
	}
}

func TestCommandUse_NoUsage(t *testing.T) {
	meta := playbookMeta{command: "install"}
	got := commandUse(meta)
	if got != "install" {
		t.Errorf("commandUse = %q, want %q", got, "install")
	}
}

func TestCommandUse_ValetPrefix(t *testing.T) {
	meta := playbookMeta{
		command: "link",
		usage:   "valet link",
	}
	got := commandUse(meta)
	if got != "link" {
		t.Errorf("commandUse = %q, want %q", got, "link")
	}
}

func TestDiscover_SkipsNoCommand(t *testing.T) {
	dir := t.TempDir()
	pbDir := filepath.Join(dir, "playbooks")
	if err := os.MkdirAll(pbDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// One playbook with @command, one without
	if err := os.WriteFile(filepath.Join(pbDir, "service.yml"), []byte(`#
# @command:     "service"
# @description: "manage services"
---
`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pbDir, "internal.yml"), []byte(`#
# @description: "no command annotation"
---
`), 0o644); err != nil {
		t.Fatal(err)
	}

	cmds, err := Discover(dir)
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(cmds) != 1 {
		t.Errorf("expected 1 command, got %d", len(cmds))
	}
	if cmds[0].Use != "service" {
		t.Errorf("command Use = %q, want %q", cmds[0].Use, "service")
	}
}

func TestDiscover_GroupsSubcommands(t *testing.T) {
	dir := t.TempDir()
	pbDir := filepath.Join(dir, "playbooks")
	if err := os.MkdirAll(pbDir, 0o755); err != nil {
		t.Fatal(err)
	}

	for name, content := range map[string]string{
		"project:env.yml": `#
# @command:     "project:env"
# @description: "deploy env config"
---
`,
		"project:cc.yml": `#
# @command:     "project:cc"
# @description: "clear cache"
---
`,
	} {
		if err := os.WriteFile(filepath.Join(pbDir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	cmds, err := Discover(dir)
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(cmds) != 1 {
		t.Fatalf("expected 1 parent command, got %d", len(cmds))
	}
	parent := cmds[0]
	if parent.Use != "project" {
		t.Errorf("parent Use = %q, want %q", parent.Use, "project")
	}
	if len(parent.Commands()) != 2 {
		t.Errorf("expected 2 subcommands, got %d", len(parent.Commands()))
	}
}

func TestExpandColonCommand(t *testing.T) {
	dir := t.TempDir()
	pbDir := filepath.Join(dir, "playbooks")
	if err := os.MkdirAll(pbDir, 0o755); err != nil {
		t.Fatal(err)
	}

	for name, content := range map[string]string{
		"project:cc.yml": `#
# @command:     "project:cc"
# @description: "clear cache"
---
`,
		"service.yml": `#
# @command:     "service"
# @description: "manage services"
---
`,
	} {
		if err := os.WriteFile(filepath.Join(pbDir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	cmds, err := Discover(dir)
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	root := &cobra.Command{Use: "valet.sh", Args: cobra.ArbitraryArgs, RunE: func(*cobra.Command, []string) error { return nil }}
	root.AddCommand(cmds...)

	tests := []struct {
		name string
		args []string
		want []string
	}{
		{"colon form", []string{"project:cc"}, []string{"project", "cc"}},
		{"colon form with args", []string{"project:cc", "-v", "foo"}, []string{"project", "cc", "-v", "foo"}},
		{"space form unchanged", []string{"project", "cc"}, []string{"project", "cc"}},
		{"top-level unchanged", []string{"service", "start"}, []string{"service", "start"}},
		{"unknown colon command unchanged", []string{"project:nope"}, []string{"project:nope"}},
		{"colon in later arg unchanged", []string{"service", "a:b"}, []string{"service", "a:b"}},
		{"no args", []string{}, []string{}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := ExpandColonCommand(root, tc.args)
			if !slices.Equal(got, tc.want) {
				t.Errorf("ExpandColonCommand(%q) = %q, want %q", tc.args, got, tc.want)
			}
		})
	}
}
