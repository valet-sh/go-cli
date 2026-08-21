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
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
)

const (
	// inlineBoxMaxDocLines is the maximum number of documentation lines
	// visible in the inline box before the user needs to scroll.
	inlineBoxMaxDocLines = 5

	// inlineBoxPaddingH is the horizontal padding inside the box border (each side).
	inlineBoxPaddingH = 1

	// docsScrollHalfPage is the number of lines scrolled by ctrl+d / ctrl+u.
	docsScrollHalfPage = 3

	// docsScrollFullPage is the number of lines scrolled by ctrl+f / ctrl+b.
	docsScrollFullPage = inlineBoxMaxDocLines
)

// InlineBox is the unified command input + documentation panel that appears
// inline below the selected command in the horizontal list.
//
// It contains:
//   - A single textinput with the command path as a non-editable prompt prefix.
//     The user types arguments (free-form, passed as-is to Ansible).
//   - The command's Long description rendered below the input, scrollable with
//     vim-style ctrl+d/u/f/b keys.
//
// The box is always in "insert mode" — there is no modal switching.
type InlineBox struct {
	// docsLines holds the wrapped documentation lines for scrolling.
	docsLines []string

	// docsOffset is the current scroll position (index of first visible line).
	docsOffset int

	// input is the argument textinput. The prompt is set to the command path.
	input textinput.Model

	// width is the total available width for the box (including border).
	width int
}

// NewInlineBox creates an InlineBox for the given command.
// commandPath is e.g. "init-instance" or "service".
// docs is the Long description (falls back to Short).
// width is the total width available.
func NewInlineBox(commandPath, docs string, width int) InlineBox {
	innerWidth := width - 2*inlineBoxPaddingH - 2 // 2 for left/right border chars

	// Wrap docs to fit inside the box.
	wrapped := wordWrap(docs, innerWidth)
	docLines := strings.Split(wrapped, "\n")

	// Build the textinput with the command path as a styled prompt.
	inp := textinput.New()
	inp.Prompt = "valet.sh " + commandPath + " "
	inp.CharLimit = 512

	// Style the prompt as ghost text (dim) and the input as normal text.
	inputStyles := inp.Styles()
	inputStyles.Focused.Prompt = styles.InputGhostPrompt
	inputStyles.Focused.Text = styles.InputText
	inp.SetStyles(inputStyles)

	inp.SetWidth(innerWidth - len("valet.sh "+commandPath+" "))
	inp.Focus()

	return InlineBox{
		docsLines: docLines,
		input:     inp,
		width:     width,
	}
}

// Value returns the raw argument string the user has typed.
func (b InlineBox) Value() string {
	return strings.TrimSpace(b.input.Value())
}

// Update handles key events for the inline box.
// Returns the updated box and any tea.Cmd.
func (b InlineBox) Update(msg tea.Msg) (InlineBox, tea.Cmd) {
	keyMsg, ok := msg.(tea.KeyPressMsg)
	if !ok {
		var cmd tea.Cmd
		b.input, cmd = b.input.Update(msg)
		return b, cmd
	}

	switch keyMsg.String() {
	case "ctrl+d":
		b.scrollDocs(docsScrollHalfPage)
		return b, nil
	case "ctrl+u":
		b.scrollDocs(-docsScrollHalfPage)
		return b, nil
	case "ctrl+f":
		b.scrollDocs(docsScrollFullPage)
		return b, nil
	case "ctrl+b":
		b.scrollDocs(-docsScrollFullPage)
		return b, nil
	}

	var cmd tea.Cmd
	b.input, cmd = b.input.Update(msg)
	return b, cmd
}

// InputView returns the rendered textinput for embedding in the header.
// The input itself is not shown inside the box — it lives in the header line.
func (b InlineBox) InputView() string {
	return b.input.View()
}

// View renders the inline box as a bordered string ready to embed in the layout.
// The box contains docs only — the input is shown in the header line.
func (b InlineBox) View() string {
	innerWidth := b.width - 2*inlineBoxPaddingH - 2

	contentLines := b.visibleDocLines()

	totalLines := len(b.docsLines)
	if totalLines > inlineBoxMaxDocLines {
		remaining := totalLines - b.docsOffset - inlineBoxMaxDocLines
		if remaining > 0 {
			hint := styles.InputGhostPrompt.Render("▼ ctrl+d scroll")
			contentLines = append(contentLines, hint)
		} else {
			hint := styles.InputGhostPrompt.Render("▲ ctrl+u scroll up")
			contentLines = append(contentLines, hint)
		}
	}

	content := strings.Join(contentLines, "\n")

	return styles.PreviewBox.Width(innerWidth).Render(content)
}

// scrollDocs adjusts the documentation scroll position by delta lines,
// clamped to valid bounds.
func (b *InlineBox) scrollDocs(delta int) {
	maxOffset := max(len(b.docsLines)-inlineBoxMaxDocLines, 0)
	b.docsOffset += delta
	if b.docsOffset < 0 {
		b.docsOffset = 0
	}
	if b.docsOffset > maxOffset {
		b.docsOffset = maxOffset
	}
}

// visibleDocLines returns the slice of doc lines currently visible in the box.
func (b InlineBox) visibleDocLines() []string {
	if len(b.docsLines) == 0 {
		return nil
	}
	end := min(b.docsOffset+inlineBoxMaxDocLines, len(b.docsLines))
	return b.docsLines[b.docsOffset:end]
}
