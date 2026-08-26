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
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

const (
	// spinnerTickInterval is how often the spinner animation advances.
	spinnerTickInterval = 50 * time.Millisecond

	// taskLogPrefix is the string that begins a new Ansible task line in the log.
	// Used to count completed tasks for the progress indicator.
	taskLogPrefix = "TASK ["
)

// execTickMsg fires periodically to advance the spinner animation.
type execTickMsg time.Time

// execDoneMsg is sent when the ansible-playbook process exits.
type execDoneMsg struct{ err error }

// ansibleEventMsg carries one parsed event from the ansible.posix.jsonl stdout stream.
// A single message can carry a task-name update, formatted log lines, or an EOF signal.
type ansibleEventMsg struct {
	// taskName, when non-empty, updates the spinner's current task display.
	taskName string
	// logLines are formatted lines to append to the live log viewport.
	logLines []string
	// eof is true when the stdout pipe is closed — no more events will arrive.
	eof bool
}

// ExecModel is a Bubble Tea model that shows execution progress and a failure log viewer.
// It handles ansible-playbook subprocess execution, live task name updates, and
// post-failure log viewing.
type ExecModel struct {
	// command is the display string shown in the header, e.g. "service start php83".
	command string

	version string
	proc    *exec.Cmd

	// cleanup deletes temporary files after the process exits.
	cleanup func()

	// ansibleOut is the reader connected to ansible-playbook's stdout pipe.
	// ansible.posix.jsonl writes one JSON line per event; readTaskCmd reads
	// task names from v2_playbook_on_task_start events and writes vsh_stdout
	// content directly to output (bypassing the BubbleTea message queue).
	ansibleOut io.Reader

	// output is a shared buffer for vsh_stdout content (tables, listings)
	// extracted from v2_runner_on_ok JSON events. Written by the readTaskCmd
	// goroutine directly — bypassing the BubbleTea message queue so that
	// tea.Quit cannot race ahead of ansibleOutputMsg delivery. The pointer
	// is shared across all value-copies of ExecModel (Elm architecture).
	// Read by runExecPanel() after p.Run() returns.
	output *bytes.Buffer

	// logLines accumulates all formatted log lines produced from JSON events.
	// Printed to stdout after BubbleTea exits if the user requested log view.
	logLines []string

	// tasksDone is the number of Ansible tasks completed so far, counted
	// by detecting "TASK [" lines in the accumulated log output.
	tasksDone int

	// currentTask is the human-readable name of the task currently being shown.
	// Extracted from JSON events. Always shows the most recently discovered
	// non-meta-task (what Ansible is currently doing).
	currentTask string

	// spinnerFrame is the current index into the spinner animation frames.
	// Advances on every execTickMsg while the process is running.
	spinnerFrame int

	// done is true once the subprocess has exited.
	done bool

	// err is non-nil if ansible exited with a non-zero status.
	err error

	// awaitingLogPrompt is true when we have shown the "View full log? [Y/n]"
	// prompt and are waiting for the user's answer.
	awaitingLogPrompt bool

	// logViewRequested is true once the user has said Y and the program should
	// quit and display the log. After BubbleTea exits, the log is printed to stdout
	// with native terminal scrolling and selection support.
	logViewRequested bool

	// stdoutEOF is true once readTaskCmd has returned ansibleEventMsg{eof:true},
	// meaning the stdout pipe has been fully drained and all vsh_stdout
	// content has been written to the output buffer. Used to coordinate
	// the quit decision when ansibleEventMsg{eof:true} arrives before execDoneMsg.
	stdoutEOF bool

	// width/height of the available area.
	width  int
	height int
}

// NewExecModel creates a new ExecModel ready to run.
// proc must already be started via ansible.RunSubprocess().
// cleanup is called after the process exits to release resources.
// width/height are the dimensions of the panel area.
// output is a shared *bytes.Buffer that readTaskCmd writes vsh_stdout content
// to directly (bypassing the BubbleTea message queue). The caller reads from
// it after p.Run() returns to print tables/listings to the terminal.
func NewExecModel(command, version string, proc *exec.Cmd, ansibleOut io.Reader, output *bytes.Buffer, cleanup func(), width, height int) ExecModel {
	return ExecModel{
		command:    command,
		version:    version,
		proc:       proc,
		ansibleOut: ansibleOut,
		output:     output,
		cleanup:    cleanup,
		width:      width,
		height:     height,
	}
}

// SetSize updates the panel dimensions (called on terminal resize).
func (e ExecModel) SetSize(width, height int) ExecModel {
	e.width = width
	e.height = height
	return e
}

func (e ExecModel) Init() tea.Cmd {
	return tea.Batch(
		tickCmd(),
		readTaskCmd(e.ansibleOut, e.output),
	)
}

// Update handles events, tick, process exit, and key presses.
func (e ExecModel) Update(msg tea.Msg) (ExecModel, tea.Cmd) {
	switch msg := msg.(type) {
	case ansibleEventMsg:
		if msg.eof {
			e.stdoutEOF = true
			return e, waitForProcess(e.proc)
		}
		if msg.taskName != "" {
			e.currentTask = msg.taskName
		}
		if len(msg.logLines) > 0 {
			e.appendLines(msg.logLines)
		}
		return e, readTaskCmd(e.ansibleOut, e.output)

	case execTickMsg:
		if !e.done {
			e.spinnerFrame++
			return e, tickCmd()
		}
		return e, nil

	case execDoneMsg:
		e.done = true
		e.err = msg.err
		if e.cleanup != nil {
			e.cleanup()
		}
		// Quit in CLI success mode only when stdout is fully drained too.
		if e.err == nil {
			if e.stdoutEOF {
				return e, tea.Quit
			}
			return e, nil
		}
		return e, resyncTerminalCmd()

	case tea.KeyPressMsg:
		return e.handleKey(msg)

	case tea.WindowSizeMsg:
		e = e.SetSize(msg.Width, msg.Height)
		return e, nil
	}

	return e, nil
}

// handleKey processes keyboard input for all exec sub-states.
func (e ExecModel) handleKey(msg tea.KeyPressMsg) (ExecModel, tea.Cmd) {
	key := msg.String()

	if e.awaitingLogPrompt {
		switch key {
		case "y", "Y", "enter":
			e.awaitingLogPrompt = false
			e.logViewRequested = true
			return e, tea.Quit
		case "n", "esc", "q", "ctrl+c":
			return e, tea.Quit
		}
		return e, nil
	}

	if !e.done {
		if key == "ctrl+c" {
			if e.proc != nil && e.proc.Process != nil {
				_ = e.proc.Process.Signal(os.Interrupt)
			}
			if e.cleanup != nil {
				e.cleanup()
				e.cleanup = nil
			}
			return e, tea.Quit
		}
		return e, nil
	}

	if e.err != nil && !e.awaitingLogPrompt {
		if key == "ctrl+c" || key == "esc" || key == "q" {
			return e, tea.Quit
		}
		// If user presses y/Y, skip the prompt and request log view.
		if key == "y" || key == "Y" {
			e.logViewRequested = true
			return e, tea.Quit
		}
		// Any other key: show the prompt and wait for response.
		e.awaitingLogPrompt = true
		return e, nil
	}

	// Done (success) — any key exits.
	return e, tea.Quit
}

func (e ExecModel) View() string {
	return e.cliView()
}

// cliView renders the minimal CLI view: command header, spinner line, and optional error prompt.
func (e ExecModel) cliView() string {
	var output strings.Builder

	cmdLabel := styles.Header.Render("▶ valet.sh " + e.command)
	versionLabel := styles.Version.Render("v" + e.version)
	versionPadding := max(e.width-lipgloss.Width(cmdLabel)-lipgloss.Width(versionLabel)-2, 1)
	_, _ = fmt.Fprintln(&output, cmdLabel+strings.Repeat(" ", versionPadding)+versionLabel)

	_, _ = fmt.Fprintln(&output, e.progressBarView())

	if e.done && e.err != nil {
		if e.awaitingLogPrompt {
			promptLine := styles.HelpDesc.Render("View full log? ") +
				styles.HelpKey.Render("[Y]") +
				styles.HelpDesc.Render("/") +
				styles.ItemDim.Render("n")
			_, _ = fmt.Fprint(&output, promptLine)
		} else {
			hintLine := styles.HelpDesc.Render("(press y to view log, q to exit)")
			_, _ = fmt.Fprint(&output, hintLine)
		}
	}

	return output.String()
}

// spinnerFrames are the animation frames for the progress spinner (Braille U+2800..U+28FF).
var spinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

// progressBarView renders the progress indicator: spinner with task while running, checkmark/cross when done.
func (e ExecModel) progressBarView() string {
	if e.done {
		if e.err != nil {
			return lipgloss.NewStyle().Foreground(colourRed).Render(
				"✘  " + fmt.Sprintf("%d tasks", e.tasksDone),
			)
		}
		return styles.ItemSelected.Render(
			"✔  " + fmt.Sprintf("%d tasks completed", e.tasksDone),
		)
	}

	frame := spinnerFrames[e.spinnerFrame%len(spinnerFrames)]
	spinner := styles.HelpKey.Render(frame)

	taskDisplay := e.currentTask
	if taskDisplay == "" {
		taskDisplay = "running..."
	} else {
		taskDisplay = shortTaskName(taskDisplay)
	}
	counter := styles.HelpDesc.Render("  " + taskDisplay)
	return spinner + counter
}

// IsDone returns true once the subprocess has exited.
func (e ExecModel) IsDone() bool { return e.done }

// Err returns the subprocess exit error, if any.
func (e ExecModel) Err() error { return e.err }

// LogViewRequested returns true if the user requested to view the full log.
// After BubbleTea exits, the caller should display the log via printLogView().
func (e ExecModel) LogViewRequested() bool { return e.logViewRequested }

// LogLines returns the accumulated formatted log lines.
// Typically used after BubbleTea exits to display the log via printLogView().
func (e ExecModel) LogLines() []string { return e.logLines }

// appendLines appends multiple lines to the logLines accumulator,
// counting TASK-prefix lines to advance the task counter.
func (e *ExecModel) appendLines(lines []string) {
	for _, line := range lines {
		if strings.HasPrefix(line, taskLogPrefix) {
			e.tasksDone++
		}
	}
	e.logLines = append(e.logLines, lines...)
}

// tickCmd returns a tea.Cmd that fires after spinnerTickInterval.
func tickCmd() tea.Cmd {
	return tea.Tick(spinnerTickInterval, func(t time.Time) tea.Msg {
		return execTickMsg(t)
	})
}

type resyncExecCommand struct{}

func (resyncExecCommand) Run() error          { return nil }
func (resyncExecCommand) SetStdin(io.Reader)  {}
func (resyncExecCommand) SetStdout(io.Writer) {}
func (resyncExecCommand) SetStderr(io.Writer) {}

func resyncTerminalCmd() tea.Cmd {
	return tea.Exec(resyncExecCommand{}, nil)
}

func waitForProcess(cmd *exec.Cmd) tea.Cmd {
	return func() tea.Msg {
		if cmd == nil {
			return execDoneMsg{}
		}
		return execDoneMsg{err: cmd.Wait()}
	}
}

// readTaskCmd returns a tea.Cmd that blocks reading JSON lines from the
// ansible.posix.jsonl stdout stream and returns an ansibleEventMsg for each
// parsed event:
//
//   - v2_playbook_on_task_start / v2_runner_on_start → taskName + TASK log line
//   - v2_runner_on_ok with vsh_stdout → written to out buffer (no BubbleTea msg)
//   - v2_runner_on_ok with stderr/stdout → warning log lines in ansibleEventMsg
//   - v2_runner_on_failed / v2_runner_on_unreachable → error log lines
//   - v2_playbook_on_stats → recap log lines
//   - EOF / pipe closed → ansibleEventMsg{eof: true}
//
// vsh_stdout is written to the shared buffer directly (bypassing BubbleTea)
// so that tea.Quit cannot race ahead of buffer writes in CLI success mode.
// Reads byte-by-byte so no bytes are lost between successive goroutine calls.
func readTaskCmd(r io.Reader, out *bytes.Buffer) tea.Cmd {
	if r == nil {
		return nil
	}
	return func() tea.Msg {
		var line []byte
		oneByte := make([]byte, 1)
		for {
			n, err := r.Read(oneByte)
			if n > 0 {
				b := oneByte[0]
				switch b {
				case '\n':
					if msg := parseJSONEvent(line, out); msg != nil {
						return msg
					}
					line = line[:0]
				case '\r':
					// CR terminates spinner-text lines — reset without parsing.
					line = line[:0]
				default:
					line = append(line, b)
				}
			}
			if err != nil {
				return ansibleEventMsg{eof: true}
			}
		}
	}
}

// IsTTY reports whether stdout is connected to a terminal.
func IsTTY() bool {
	fi, err := os.Stdout.Stat()
	if err != nil {
		return false
	}
	return (fi.Mode() & os.ModeCharDevice) != 0
}
