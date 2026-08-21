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
	"strings"

	tea "charm.land/bubbletea/v2"
)

// helpState holds all state for the help view screen (screenHelp).
// Only meaningful when model.activeScreen == screenHelp.
type helpState struct {
	// lines are the wrapped lines of help text shown in the help view.
	lines []string

	// offset is the scroll position (0-indexed line) in lines.
	offset int

	// title is the command path shown in the help view header, e.g. "valet.sh init-instance".
	title string

	// cmd is the cobra command being viewed; used to trigger inline box on Enter.
	cmd CommandItem
}

// openHelp opens the help view for the selected command.
// Returns the updated model or nil if the command is not a leaf.
func (m model) openHelp() (tea.Model, tea.Cmd) {
	sel, ok := m.commandList.SelectedItem().(CommandItem)
	if !ok {
		return m, nil
	}

	// Only show help for leaf commands.
	if sel.IsBack || sel.HasSubCommands() {
		return m, nil
	}

	// Build help content: Usage + description + full help text.
	var helpText strings.Builder
	if sel.Cmd.Use != "" {
		helpText.WriteString("Usage: " + sel.Cmd.Use + "\n\n")
	}
	if sel.Cmd.Short != "" {
		helpText.WriteString(sel.Cmd.Short + "\n\n")
	}
	if sel.Cmd.Long != "" {
		helpText.WriteString(sel.Cmd.Long)
	}

	// Word-wrap to terminal width, leaving 2-character margin.
	wrapped := wordWrap(helpText.String(), m.width-2)
	helpLines := strings.Split(wrapped, "\n")

	m.help = helpState{
		lines:  helpLines,
		offset: 0,
		title:  m.fullCommandPath(sel.Title()),
		cmd:    sel,
	}
	m.activeScreen = screenHelp
	return m, nil
}

// handleHelpKey handles key events in the help view.
func (m model) handleHelpKey(key string, _ tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	maxScroll := max(
		// 6 lines for header, divider, footer
		len(m.help.lines)-(m.height-6), 0)

	switch key {
	case "j", "down", "ctrl+d":
		if m.help.offset < maxScroll {
			m.help.offset++
		}
		return m, nil

	case "k", "up", "ctrl+u":
		if m.help.offset > 0 {
			m.help.offset--
		}
		return m, nil

	case "q", "esc", "?":
		m.activeScreen = screenList
		return m, nil

	case "enter":
		m.activeScreen = screenList
		path := m.fullCommandPath(m.help.cmd.Title())
		docs := m.help.cmd.LongDescription()
		box := NewInlineBox(path, docs, m.inlineBoxWidth())
		m.inlineBox = &box
		m.activeScreen = screenInline
		return m, nil
	}

	return m, nil
}

// helpView renders the full help screen with scrollable content.
func (m model) helpView() string {
	var output strings.Builder

	_, _ = fmt.Fprintln(&output, m.headerView())
	_, _ = fmt.Fprintln(&output, dividerLine(m.width))

	// Use fixed height to prevent viewport jumps when opening help.
	contentHeight := helpViewMaxLines

	// Slice the visible portion of help.lines based on scroll offset.
	endLine := min(m.help.offset+contentHeight, len(m.help.lines))

	visibleLines := m.help.lines[m.help.offset:endLine]
	for _, line := range visibleLines {
		_, _ = fmt.Fprintln(&output, "  "+line)
	}

	// Pad with blank lines if we're not at the end.
	for len(visibleLines) < contentHeight {
		_, _ = fmt.Fprintln(&output)
		visibleLines = append(visibleLines, "")
	}

	_, _ = fmt.Fprintln(&output, dividerLine(m.width))

	// Footer with navigation hints.
	footer := "  ↑/↓ scroll   j/k vim scroll   q/esc close   enter run"
	_, _ = fmt.Fprint(&output, footer)

	return output.String()
}
