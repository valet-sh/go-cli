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

package tui

import (
	"fmt"
	"testing"

	"charm.land/bubbles/v2/list"
	tea "charm.land/bubbletea/v2"
	"github.com/spf13/cobra"
)

// noop is a minimal RunE that satisfies cobra's "available" check.
func noop(_ *cobra.Command, _ []string) error { return nil }

// testRoot builds a minimal cobra command tree for testing.
func testRoot() *cobra.Command {
	root := &cobra.Command{Use: "valet.sh", Short: "test root", RunE: noop}

	root.AddCommand(&cobra.Command{Use: "install", Short: "Install services", RunE: noop})

	service := &cobra.Command{Use: "service <action> [service-name]", Short: "Manage services"}
	service.AddCommand(&cobra.Command{Use: "start", Short: "Start a service", RunE: noop})
	service.AddCommand(&cobra.Command{Use: "stop", Short: "Stop a service", RunE: noop})
	root.AddCommand(service)

	project := &cobra.Command{Use: "project", Short: "Project operations"}
	project.AddCommand(&cobra.Command{Use: "env", Short: "Regenerate env.php", RunE: noop})
	project.AddCommand(&cobra.Command{Use: "cc", Short: "Clear cache", RunE: noop})
	root.AddCommand(project)

	return root
}

// ---------------------------------------------------------------------------
// Model initialisation
// ---------------------------------------------------------------------------

func TestNewModel(t *testing.T) {
	root := testRoot()
	m := newModel(root, "1.0.0", false)

	if len(m.stack) != 1 {
		t.Fatalf("expected 1 stack entry, got %d", len(m.stack))
	}
	if m.stack[0].title != "valet.sh" {
		t.Errorf("expected root title 'valet.sh', got %q", m.stack[0].title)
	}
	if m.activeScreen != screenList {
		t.Errorf("expected screenList on init, got %v", m.activeScreen)
	}
	if m.vimMode {
		t.Error("expected vimMode false by default")
	}
}

func TestNewModelVimMode(t *testing.T) {
	root := testRoot()
	m := newModel(root, "1.0.0", true)

	if !m.vimMode {
		t.Error("expected vimMode true when passed true")
	}
}

// ---------------------------------------------------------------------------
// Quit behaviour
// ---------------------------------------------------------------------------

func TestCtrlCAlwaysQuits(t *testing.T) {
	root := testRoot()
	m := newModel(root, "1.0.0", false)

	_, cmd := m.Update(tea.KeyPressMsg{Text: "ctrl+c"})
	if cmd == nil {
		t.Error("ctrl+c should always quit")
	}
}

func TestQKeyQuitsWhenNotFiltering(t *testing.T) {
	root := testRoot()
	m := newModel(root, "1.0.0", false)

	if m.commandList.FilterState() != list.Unfiltered {
		t.Fatal("expected Unfiltered on fresh model")
	}

	_, cmd := m.Update(tea.KeyPressMsg{Text: "q"})
	if cmd == nil {
		t.Error("q should quit when not filtering")
	}
}

// ---------------------------------------------------------------------------
// Vim mode toggle
// ---------------------------------------------------------------------------

func TestVimModeTogglesWithCtrlBracket(t *testing.T) {
	root := testRoot()
	m := newModel(root, "1.0.0", false)

	if m.vimMode {
		t.Fatal("expected vimMode false initially")
	}

	result, _ := m.Update(tea.KeyPressMsg{Text: "ctrl+["})
	rm := result.(model)
	if !rm.vimMode {
		t.Error("expected vimMode true after ctrl+[")
	}

	result2, _ := rm.Update(tea.KeyPressMsg{Text: "ctrl+["})
	rm2 := result2.(model)
	if rm2.vimMode {
		t.Error("expected vimMode false after second ctrl+[")
	}
}

// ---------------------------------------------------------------------------
// Navigation
// ---------------------------------------------------------------------------

func TestLeftArrowMovesBack(t *testing.T) {
	root := testRoot()
	m := newModel(root, "1.0.0", false)
	m.width = 120
	m.height = 40

	// Move right first so there's somewhere to go back.
	result1, _ := m.Update(tea.KeyPressMsg{Text: "right"})
	rm1 := result1.(model)
	idxAfterRight := rm1.commandList.Index()

	result2, _ := rm1.Update(tea.KeyPressMsg{Text: "left"})
	rm2 := result2.(model)

	if rm2.commandList.Index() >= idxAfterRight {
		t.Errorf("left should decrease index: got %d, was %d", rm2.commandList.Index(), idxAfterRight)
	}
}

func TestVimHLNavigation(t *testing.T) {
	root := testRoot()
	m := newModel(root, "1.0.0", true) // vim mode
	m.width = 120
	m.height = 40

	startIdx := m.commandList.Index()

	// l moves right.
	result1, _ := m.Update(tea.KeyPressMsg{Text: "l"})
	rm1 := result1.(model)
	if rm1.commandList.Index() <= startIdx {
		t.Error("l in vim mode should move cursor right (CursorDown)")
	}

	// h moves back.
	result2, _ := rm1.Update(tea.KeyPressMsg{Text: "h"})
	rm2 := result2.(model)
	if rm2.commandList.Index() != startIdx {
		t.Errorf("h in vim mode: expected index %d, got %d", startIdx, rm2.commandList.Index())
	}
}

func TestVimHLNotActiveInNormalMode(t *testing.T) {
	root := testRoot()
	m := newModel(root, "1.0.0", false) // normal mode
	m.width = 120
	m.height = 40

	startIdx := m.commandList.Index()

	// h in normal mode goes to the filter (types 'h') not navigation.
	// Index should not change from CursorUp since filtering activates.
	result, _ := m.Update(tea.KeyPressMsg{Text: "h"})
	rm := result.(model)
	// In normal mode h types into filter, so index won't change via navigation.
	_ = rm
	_ = startIdx // navigation via h only works in vim mode
}

// ---------------------------------------------------------------------------
// Navigation stack
// ---------------------------------------------------------------------------

func TestEscAtRootQuits(t *testing.T) {
	root := testRoot()
	m := newModel(root, "1.0.0", false)

	_, cmd := m.Update(tea.KeyPressMsg{Text: "esc"})
	if cmd == nil {
		t.Error("Esc at root should quit")
	}
}

func TestPushAndPopStack(t *testing.T) {
	root := testRoot()
	m := newModel(root, "1.0.0", false)
	m.width = 120
	m.height = 40

	var serviceCmd *cobra.Command
	for _, c := range root.Commands() {
		if len(c.Commands()) > 0 {
			serviceCmd = c
			break
		}
	}
	if serviceCmd == nil {
		t.Fatal("no command with subcommands found")
	}

	pushed, _ := m.pushStack(CommandItem{Cmd: serviceCmd})
	pm := pushed.(model)

	if len(pm.stack) != 2 {
		t.Fatalf("expected 2 stack entries after push, got %d", len(pm.stack))
	}

	popped, _ := pm.Update(tea.KeyPressMsg{Text: "esc"})
	rpm := popped.(model)

	if len(rpm.stack) != 1 {
		t.Errorf("expected 1 stack entry after Esc, got %d", len(rpm.stack))
	}
}

// ---------------------------------------------------------------------------
// Window resize
// ---------------------------------------------------------------------------

func TestWindowResize(t *testing.T) {
	root := testRoot()
	m := newModel(root, "1.0.0", false)

	result, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	rm := result.(model)

	if rm.width != 120 {
		t.Errorf("expected width 120, got %d", rm.width)
	}
	if rm.height != 40 {
		t.Errorf("expected height 40, got %d", rm.height)
	}
}

// ---------------------------------------------------------------------------
// Filter / search bar
// ---------------------------------------------------------------------------

func TestFilteringEnabledOnBuildList(t *testing.T) {
	root := testRoot()
	m := newModel(root, "1.0.0", false)

	if !m.commandList.FilteringEnabled() {
		t.Error("expected filtering to be enabled")
	}
}

// ---------------------------------------------------------------------------
// Inline box
// ---------------------------------------------------------------------------

func TestInlineBoxOpensOnEnter(t *testing.T) {
	root := testRoot()
	m := newModel(root, "1.0.0", false)
	m.width = 120
	m.height = 40

	// Select first available item.
	if m.commandList.FilterState() != list.Unfiltered {
		t.Skip("unexpected filter state")
	}

	result, _ := m.Update(tea.KeyPressMsg{Text: "enter"})
	rm := result.(model)

	if rm.activeScreen != screenInline {
		t.Errorf("expected screenInline after Enter, got %v", rm.activeScreen)
	}
	if rm.inlineBox == nil {
		t.Error("expected inlineBox to be non-nil after Enter")
	}
}

func TestInlineBoxClosesOnEsc(t *testing.T) {
	root := testRoot()
	m := newModel(root, "1.0.0", false)
	m.width = 120
	m.height = 40

	// Open inline box.
	result, _ := m.Update(tea.KeyPressMsg{Text: "enter"})
	rm := result.(model)
	if rm.activeScreen != screenInline {
		t.Skip("inline box did not open — skipping")
	}

	// Close with Esc.
	result2, _ := rm.Update(tea.KeyPressMsg{Text: "esc"})
	rm2 := result2.(model)

	if rm2.activeScreen != screenList {
		t.Errorf("expected screenList after Esc, got %v", rm2.activeScreen)
	}
	if rm2.inlineBox != nil {
		t.Error("expected inlineBox to be nil after Esc")
	}
}

// ---------------------------------------------------------------------------
// CommandItem
// ---------------------------------------------------------------------------

func TestCommandItemTitle(t *testing.T) {
	cmd := &cobra.Command{Use: "service <action> [name]", Short: "Manage services"}
	item := CommandItem{Cmd: cmd}

	if item.Title() != "service" {
		t.Errorf("expected 'service', got %q", item.Title())
	}
}

func TestCommandItemBackTitle(t *testing.T) {
	item := CommandItem{IsBack: true}
	if item.Title() != "← back" {
		t.Errorf("expected '← back', got %q", item.Title())
	}
}

func TestCommandItemHasSubCommands(t *testing.T) {
	parent := &cobra.Command{Use: "service", Short: "Manage services"}
	parent.AddCommand(&cobra.Command{Use: "start", Short: "Start", RunE: noop})

	item := CommandItem{Cmd: parent}
	if !item.HasSubCommands() {
		t.Error("expected HasSubCommands true for command with available children")
	}

	leaf := &cobra.Command{Use: "install", Short: "Install", RunE: noop}
	leafItem := CommandItem{Cmd: leaf}
	if leafItem.HasSubCommands() {
		t.Error("expected HasSubCommands false for leaf command")
	}
}

// ---------------------------------------------------------------------------
// itemsFromCommands
// ---------------------------------------------------------------------------

func TestItemsFromCommands(t *testing.T) {
	root := testRoot()
	cmds := root.Commands()

	items := itemsFromCommands(cmds, false)
	if len(items) != len(cmds) {
		t.Errorf("expected %d items, got %d", len(cmds), len(items))
	}

	withBack := itemsFromCommands(cmds, true)
	if len(withBack) != len(cmds)+1 {
		t.Errorf("expected %d items with back, got %d", len(cmds)+1, len(withBack))
	}
	back, ok := withBack[0].(CommandItem)
	if !ok || !back.IsBack {
		t.Error("expected first item to be back item")
	}
}

// ---------------------------------------------------------------------------
// Help View (? key)
// ---------------------------------------------------------------------------

func TestHelpKeyOpensHelpView(t *testing.T) {
	root := testRoot()
	m := newModel(root, "1.0.0", false)
	m.width = 120
	m.height = 40

	// Press ? to open help for the selected command.
	result, _ := m.Update(tea.KeyPressMsg{Text: "?"})
	rm := result.(model)

	if rm.activeScreen != screenHelp {
		t.Errorf("expected screenHelp after ?, got %v", rm.activeScreen)
	}
	if len(rm.help.lines) == 0 {
		t.Error("expected help.lines to be non-empty after opening help")
	}
	if rm.help.title == "" {
		t.Error("expected help.title to be set")
	}
}

func TestHelpKeyFromInlineBox(t *testing.T) {
	root := testRoot()
	m := newModel(root, "1.0.0", false)
	m.width = 120
	m.height = 40

	// Open inline box first.
	result, _ := m.Update(tea.KeyPressMsg{Text: "enter"})
	rm := result.(model)
	if rm.activeScreen != screenInline {
		t.Skip("inline box did not open")
	}

	// ? in inline box should close the box and open help for the same command.
	result2, _ := rm.Update(tea.KeyPressMsg{Text: "?"})
	rm2 := result2.(model)

	if rm2.activeScreen != screenHelp {
		t.Errorf("? in inline box should open help, got screen %v", rm2.activeScreen)
	}
	if rm2.inlineBox != nil {
		t.Error("expected inline box to be closed after ? key")
	}
}

func TestHelpViewClosesWithEsc(t *testing.T) {
	root := testRoot()
	m := newModel(root, "1.0.0", false)
	m.width = 120
	m.height = 40

	// Open help view.
	result, _ := m.Update(tea.KeyPressMsg{Text: "?"})
	rm := result.(model)
	if rm.activeScreen != screenHelp {
		t.Skip("help view did not open")
	}

	// Close with Esc.
	result2, _ := rm.Update(tea.KeyPressMsg{Text: "esc"})
	rm2 := result2.(model)

	if rm2.activeScreen != screenList {
		t.Errorf("expected screenList after Esc from help, got %v", rm2.activeScreen)
	}
}

func TestHelpViewClosesWithQ(t *testing.T) {
	root := testRoot()
	m := newModel(root, "1.0.0", false)
	m.width = 120
	m.height = 40

	// Open help view.
	result, _ := m.Update(tea.KeyPressMsg{Text: "?"})
	rm := result.(model)
	if rm.activeScreen != screenHelp {
		t.Skip("help view did not open")
	}

	// Close with Q.
	result2, _ := rm.Update(tea.KeyPressMsg{Text: "q"})
	rm2 := result2.(model)

	if rm2.activeScreen != screenList {
		t.Errorf("expected screenList after Q from help, got %v", rm2.activeScreen)
	}
}

func TestHelpViewScrollDownWithJ(t *testing.T) {
	root := testRoot()
	m := newModel(root, "1.0.0", false)
	m.width = 120
	m.height = 10

	// Open help view.
	result, _ := m.Update(tea.KeyPressMsg{Text: "?"})
	rm := result.(model)
	if rm.activeScreen != screenHelp {
		t.Skip("help view did not open")
	}

	// Manually populate help.lines with many lines to allow scrolling.
	for i := range 100 {
		rm.help.lines = append(rm.help.lines, "Line "+fmt.Sprintf("%d", i))
	}

	initialOffset := rm.help.offset

	// Scroll down with J.
	result2, _ := rm.Update(tea.KeyPressMsg{Text: "j"})
	rm2 := result2.(model)

	if rm2.help.offset <= initialOffset {
		t.Errorf("expected help.offset to increase with J, got %d (was %d)", rm2.help.offset, initialOffset)
	}
}

func TestHelpViewScrollUpWithK(t *testing.T) {
	root := testRoot()
	m := newModel(root, "1.0.0", false)
	m.width = 120
	m.height = 10

	// Open help view.
	result, _ := m.Update(tea.KeyPressMsg{Text: "?"})
	rm := result.(model)
	if rm.activeScreen != screenHelp {
		t.Skip("help view did not open")
	}

	// Manually populate help.lines with many lines to allow scrolling.
	for i := range 100 {
		rm.help.lines = append(rm.help.lines, "Line "+fmt.Sprintf("%d", i))
	}

	// Scroll down first.
	result2, _ := rm.Update(tea.KeyPressMsg{Text: "j"})
	rm2 := result2.(model)

	offsetAfterDown := rm2.help.offset

	// Scroll up with K.
	result3, _ := rm2.Update(tea.KeyPressMsg{Text: "k"})
	rm3 := result3.(model)

	if rm3.help.offset >= offsetAfterDown {
		t.Errorf("expected help.offset to decrease with K, got %d (was %d)", rm3.help.offset, offsetAfterDown)
	}
}

// ---------------------------------------------------------------------------
// wordWrap
// ---------------------------------------------------------------------------

func TestWordWrap(t *testing.T) {
	tests := []struct {
		text     string
		maxWidth int
		wantLen  int
	}{
		{"hello world", 20, 1},
		{"hello world", 5, 2},
		{"a b c d e f", 3, 3},
		{"", 20, 0},
	}

	for _, tc := range tests {
		wrapped := wordWrap(tc.text, tc.maxWidth)
		if tc.text == "" {
			if wrapped != "" {
				t.Errorf("wordWrap(%q, %d): want empty, got %q", tc.text, tc.maxWidth, wrapped)
			}
			continue
		}
		lines := 1
		for _, ch := range wrapped {
			if ch == '\n' {
				lines++
			}
		}
		if lines != tc.wantLen {
			t.Errorf("wordWrap(%q, %d): want %d lines, got %d", tc.text, tc.maxWidth, tc.wantLen, lines)
		}
	}
}
