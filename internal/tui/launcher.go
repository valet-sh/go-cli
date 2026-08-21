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

	"charm.land/bubbles/v2/list"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/spf13/cobra"
)

// screen tracks which state the launcher is in.
type screen int

const (
	screenList   screen = iota // navigating the horizontal command bar
	screenInline               // inline box open (arg input + docs)
	screenHelp                 // help view for current command (read-only, scrollable)

	// helpViewMaxLines is the maximum number of content lines shown in the help view.
	// Matches inlineBoxMaxDocLines to keep the UI consistent and prevent viewport jumps.
	helpViewMaxLines = 8
)

// stackEntry records a navigation level so we can Esc back.
type stackEntry struct {
	list  list.Model
	title string // breadcrumb label for this level, e.g. "service"
}

// model is the root Bubble Tea model for the valet-sh launcher.
// Elm architecture requires value receivers — do not use pointer receivers.
type model struct {
	root    *cobra.Command
	version string
	vimMode bool

	stack       []stackEntry
	commandList list.Model

	// inlineBox is the unified arg-input + docs box shown when a command
	// with arguments (or any command, for previewing) is selected.
	inlineBox *InlineBox

	// activeScreen controls which component receives keystrokes.
	activeScreen screen

	// selectedArgs holds the argv to dispatch after the launcher quits.
	// Set by executeCommand(); read by Run() to populate Result.Args.
	selectedArgs []string

	// width/height of the terminal at last WindowSizeMsg.
	width  int
	height int

	// help holds the state for the help view screen (screenHelp).
	// Only meaningful when activeScreen == screenHelp.
	help helpState
}

// Result is returned by Run() after the TUI exits.
type Result struct {
	// Args is the argv slice to dispatch, e.g. ["service", "start", "php83"].
	// Empty means the user cancelled or the command was handled internally.
	Args []string
}

// Run launches the interactive TUI launcher.
// vimMode starts the launcher with vim-style navigation enabled.
//
// When the user selects and confirms a command, Run quits BubbleTea and
// returns Result.Args so the caller can dispatch via RunWithPanel. This
// keeps the launcher as a pure navigation layer — Ansible's vars_prompt
// (password prompt) runs on the raw terminal before BubbleTea restarts
// as the exec panel.
func Run(root *cobra.Command, version string, vimMode bool) (Result, error) {
	m := newModel(root, version, vimMode)
	p := tea.NewProgram(m)

	final, err := p.Run()
	if err != nil {
		return Result{}, err
	}

	if fm, ok := final.(model); ok && len(fm.selectedArgs) > 0 {
		return Result{Args: fm.selectedArgs}, nil
	}

	return Result{}, nil
}

// newModel initialises the launcher model.
func newModel(root *cobra.Command, version string, vimMode bool) model {
	rootList := buildList(root.Commands(), false, 80, 10)
	return model{
		root:        root,
		version:     version,
		vimMode:     vimMode,
		stack:       []stackEntry{{list: rootList, title: "valet.sh"}},
		commandList: rootList,
		width:       80,
		height:      24,
	}
}

// buildList creates a bubbles list.Model from a slice of cobra commands.
// bubbles/list handles keyboard navigation and filter state; the visual
// rendering is delegated to renderHorizontalList().
// withBack=true for submenus (disables filtering); withBack=false for root.
func buildList(cmds []*cobra.Command, withBack bool, width, height int) list.Model {
	items := itemsFromCommands(cmds, withBack)
	delegate := NewCommandDelegate()
	l := list.New(items, delegate, width, height)
	l.SetShowTitle(false)
	l.SetShowStatusBar(false)
	l.SetFilteringEnabled(!withBack) // only root list can filter
	l.SetShowHelp(false)
	l.DisableQuitKeybindings()

	// Use prefix filter for faster, more predictable matching.
	l.Filter = prefixFilter

	// Style the filter input to match the valet-sh palette.
	l.FilterInput.Prompt = "/ "
	filterStyles := l.FilterInput.Styles()
	filterStyles.Focused.Prompt = styles.InputGhostPrompt.Bold(true).Foreground(colourBlue)
	filterStyles.Focused.Text = styles.InputText
	filterStyles.Blurred.Prompt = styles.InputGhostPrompt
	filterStyles.Blurred.Text = styles.ItemDim
	l.FilterInput.SetStyles(filterStyles)

	return l
}

// --- Elm Architecture ----------------------------------------------------------

// Init satisfies tea.Model.
func (m model) Init() tea.Cmd { return nil }

// Update is the central event handler.
func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m = m.resizeAll()
		return m, nil

	case tea.KeyPressMsg:
		return m.handleKey(msg)
	}

	return m.routeMsg(msg)
}

// handleKey routes keyboard input based on active screen and vim mode.
func (m model) handleKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	key := msg.String()

	if key == "ctrl+c" {
		return m, tea.Quit
	}

	if key == "ctrl+[" {
		m.vimMode = !m.vimMode
		return m, nil
	}

	switch m.activeScreen {
	case screenList:
		return m.handleListKey(key, msg)

	case screenInline:
		return m.handleInlineKey(key, msg)

	case screenHelp:
		return m.handleHelpKey(key, msg)
	}

	return m, nil
}

// handleListKey handles key events on the horizontal command list.
func (m model) handleListKey(key string, msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if key == "q" && m.commandList.FilterState() != list.Filtering {
		return m, tea.Quit
	}

	switch key {
	case "left", "up":
		m.commandList.CursorUp()
		return m, nil
	case "right", "down":
		m.commandList.CursorDown()
		return m, nil
	}

	if m.vimMode {
		switch key {
		case "h", "k":
			m.commandList.CursorUp()
			return m, nil
		case "l", "j":
			m.commandList.CursorDown()
			return m, nil
		}
	}

	switch key {
	case "esc":
		if m.commandList.FilterState() != list.Filtering {
			return m.popStack()
		}
	case "?":
		if m.commandList.FilterState() != list.Filtering {
			return m.openHelp()
		}
	case "enter":
		return m.selectItem()
	}

	// Root view only — filter activation.
	if len(m.stack) == 1 && m.commandList.FilterState() != list.Filtering {
		if m.vimMode {
			// Vim mode: '/' opens filter. All other unhandled keys are swallowed.
			if key == "/" {
				m.commandList.SetFilterText("")
				m.commandList.SetFilterState(list.Filtering)
				m.stack[len(m.stack)-1].list = m.commandList
				return m, nil
			}
			return m, nil
		}
		// Normal mode: any printable character (except '/') activates filter
		// with that character pre-typed.
		if msg.Text != "" && msg.Text != "/" && msg.Mod == 0 {
			m.commandList.SetFilterText(msg.Text)
			m.commandList.SetFilterState(list.Filtering)
			m.stack[len(m.stack)-1].list = m.commandList
			return m, nil
		}
	}

	var cmd tea.Cmd
	m.commandList, cmd = m.commandList.Update(msg)
	m.stack[len(m.stack)-1].list = m.commandList
	return m, cmd
}

// handleInlineKey handles key events when the inline box is open.
func (m model) handleInlineKey(key string, msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch key {
	case "?":
		m.inlineBox = nil
		m.activeScreen = screenList
		return m.openHelp()

	case "esc":
		m.inlineBox = nil
		m.activeScreen = screenList
		return m, nil

	case "enter":
		return m.executeCommand()
	}

	if m.inlineBox != nil {
		var cmd tea.Cmd
		updated, cmd := m.inlineBox.Update(msg)
		m.inlineBox = &updated
		return m, cmd
	}

	return m, nil
}

// selectItem handles Enter on the command list.
func (m model) selectItem() (tea.Model, tea.Cmd) {
	sel, ok := m.commandList.SelectedItem().(CommandItem)
	if !ok {
		return m, nil
	}

	if sel.IsBack {
		return m.popStack()
	}

	if sel.HasSubCommands() {
		return m.pushStack(sel)
	}

	path := m.fullCommandPath(sel.Title())
	docs := sel.LongDescription()
	box := NewInlineBox(path, docs, m.inlineBoxWidth())
	m.inlineBox = &box
	m.activeScreen = screenInline
	return m, nil
}

// argsFromInlineBox builds the argv slice from the current command path
// and whatever the user typed into the inline box.
func (m model) argsFromInlineBox() []string {
	if m.inlineBox == nil {
		return strings.Fields(m.commandPathFromStack())
	}
	args := strings.Fields(m.commandPathFromStack())
	if rawInput := m.inlineBox.Value(); rawInput != "" {
		args = append(args, strings.Fields(rawInput)...)
	}
	return args
}

// executeCommand stores the selected command args and quits the launcher.
// The caller reads selectedArgs from the final model state via Run().
func (m model) executeCommand() (tea.Model, tea.Cmd) {
	if m.inlineBox == nil {
		return m, nil
	}
	m.selectedArgs = m.argsFromInlineBox()
	return m, tea.Quit
}

// pushStack navigates into a submenu.
func (m model) pushStack(sel CommandItem) (tea.Model, tea.Cmd) {
	subList := buildList(sel.Cmd.Commands(), true, m.width, 10)
	m.stack = append(m.stack, stackEntry{list: subList, title: sel.Title()})
	m.commandList = subList
	return m, nil
}

// popStack navigates back up one level, or quits at the root.
func (m model) popStack() (tea.Model, tea.Cmd) {
	if len(m.stack) <= 1 {
		return m, tea.Quit
	}
	m.stack = m.stack[:len(m.stack)-1]
	m.commandList = m.stack[len(m.stack)-1].list
	return m, nil
}

// routeMsg forwards non-key messages to the active component.
func (m model) routeMsg(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd
	switch m.activeScreen {
	case screenInline:
		if m.inlineBox != nil {
			updated, c := m.inlineBox.Update(msg)
			m.inlineBox = &updated
			cmd = c
		}
	default:
		m.commandList, cmd = m.commandList.Update(msg)
		m.stack[len(m.stack)-1].list = m.commandList
	}
	return m, cmd
}

// resizeAll updates all components after a terminal resize.
func (m model) resizeAll() model {
	m.commandList.SetSize(m.width, 10)
	m.stack[len(m.stack)-1].list = m.commandList

	return m
}

// --- View -----------------------------------------------------------------------

// View renders the full TUI.
// No alt-screen — renders inline below the cursor like fzf.
func (m model) View() tea.View {
	v := tea.NewView(m.render())
	v.MouseMode = tea.MouseModeCellMotion
	return v
}

func (m model) render() string {
	var output strings.Builder

	_, _ = fmt.Fprintln(&output, m.headerView())
	_, _ = fmt.Fprintln(&output, dividerLine(m.width))

	switch {
	case m.activeScreen == screenInline && m.inlineBox != nil:
		_, _ = fmt.Fprintln(&output, renderHorizontalList(m.commandList, m.width))
		_, _ = fmt.Fprintln(&output, m.inlineBox.View())
		_, _ = fmt.Fprintln(&output, dividerLine(m.width))
		_, _ = fmt.Fprint(&output, renderInlineHelpBar(m.width))
	case m.activeScreen == screenHelp:
		_, _ = fmt.Fprint(&output, m.helpView())
	default:
		_, _ = fmt.Fprintln(&output, renderHorizontalList(m.commandList, m.width))
		// Show filter bar when filtering is active.
		// Vim mode: show immediately on '/' (even empty) so the user knows filter is active.
		// Normal mode: only show once text has been typed (first char is pre-filled via SetFilterText).
		if m.commandList.FilterState() == list.Filtering &&
			(m.vimMode || m.commandList.FilterInput.Value() != "") {
			_, _ = fmt.Fprintln(&output, "  "+m.commandList.FilterInput.View())
		}
		_, _ = fmt.Fprintln(&output, dividerLine(m.width))
		_, _ = fmt.Fprint(&output, renderHelpBar(m.vimMode, m.width))
	}

	return output.String()
}

func (m model) headerView() string {
	crumbs := make([]string, len(m.stack))
	for i := range m.stack {
		crumbs[i] = m.stack[i].title
	}
	breadcrumb := strings.Join(crumbs, " › ")

	title := styles.Header.Render("▶ " + breadcrumb)

	var titleSuffix string
	switch m.activeScreen {
	case screenInline:
		if m.inlineBox != nil {
			titleSuffix = "  " + m.inlineBox.InputView()
		}
	case screenList:
		if sel, ok := m.commandList.SelectedItem().(CommandItem); ok && !sel.IsBack {
			titleSuffix = "  " + styles.GhostCommand.Render(sel.Title())
		}
	}

	vimIndicator := ""
	if m.vimMode {
		vimIndicator = styles.VimModeIndicator.Render("[VIM]") + "  "
	}
	versionLabel := styles.Version.Render("v" + m.version)
	right := vimIndicator + versionLabel

	leftLen := lipgloss.Width(title) + lipgloss.Width(titleSuffix)
	rightLen := lipgloss.Width(right)
	versionPadding := max(m.width-leftLen-rightLen-1, 1)

	return title + titleSuffix + strings.Repeat(" ", versionPadding) + right
}

// --- Layout helpers -------------------------------------------------------------

// inlineBoxWidth returns the width for the inline box — full terminal width.
func (m model) inlineBoxWidth() int {
	return m.width
}

// fullCommandPath returns the full command path for the given leaf name,
// including any submenu breadcrumbs from the navigation stack.
// e.g. if stack is [valet.sh, project] and leaf is "env" → "project env".
func (m model) fullCommandPath(leafName string) string {
	var parts []string
	for i := range m.stack[1:] {
		parts = append(parts, m.stack[i+1].title)
	}
	parts = append(parts, leafName)
	return strings.Join(parts, " ")
}

// commandPathFromStack returns the current command path from the stack
// (excluding root) as a space-separated string, used to build ansible args.
func (m model) commandPathFromStack() string {
	sel, ok := m.commandList.SelectedItem().(CommandItem)
	if !ok {
		return ""
	}
	return m.fullCommandPath(sel.Title())
}
