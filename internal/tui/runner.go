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
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"syscall"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/term"
	"github.com/spf13/cobra"
	"github.com/valet-sh/cli/internal/ansible"
	"github.com/valet-sh/cli/internal/platform"
)

// RunWithPanel executes a valet command with a live TUI panel. It gates BubbleTea
// startup on the first JSON task-start event, ensuring Ansible's vars_prompt
// password input runs on the raw terminal. Non-TTY falls back to normal ansible.Run().
func RunWithPanel(root *cobra.Command, args []string, version string) error {
	for _, arg := range args {
		if arg != "--help" && arg != "-h" {
			continue
		}
		root.InitDefaultHelpCmd()
		root.InitDefaultCompletionCmd(args...)
		if cmd, _, err := root.Find(args); err == nil {
			return cmd.Help()
		}
		os.Args = append([]string{os.Args[0]}, args...)
		return root.Execute()
	}
	// If the resolved command has no "playbook" annotation it is a built-in
	// cobra command (e.g. self-upgrade, version) — execute via cobra, not ansible.
	if cmd, _, err := root.Find(args); err == nil && cmd != root && cmd.Annotations["playbook"] == "" {
		os.Args = append([]string{os.Args[0]}, args...)
		return root.Execute()
	}

	if !IsTTY() {
		os.Args = append([]string{os.Args[0]}, args...)
		return root.Execute()
	}

	opts, err := resolveRunOpts(root, args)
	if err != nil {
		os.Args = append([]string{os.Args[0]}, args...)
		return root.Execute()
	}

	if opts.Debug {
		return ansible.Run(opts)
	}

	proc, ansibleOut, cleanup, err := ansible.RunSubprocess(opts)
	if err != nil {
		return fmt.Errorf("starting ansible-playbook: %w", err)
	}

	taskOut := waitForFirstJSONTask(ansibleOut)

	commandStr := strings.Join(args, " ")
	return runExecPanel(commandStr, version, proc, taskOut, cleanup)
}

// waitForFirstJSONTask reads JSON lines until it finds a task-start event,
// buffering everything and returning a reader that replays all bytes (including
// the task-start line). This gates BubbleTea startup after vars_prompt completes.
func waitForFirstJSONTask(r io.Reader) io.Reader {
	var buf bytes.Buffer
	br := bufio.NewReader(r)
	var line []byte

	for {
		b, err := br.ReadByte()
		if err == nil {
			buf.WriteByte(b)
			switch b {
			case '\n':
				if isJSONTaskStart(line) {
					goto found
				}
				line = line[:0]
			case '\r':
				line = line[:0]
			default:
				line = append(line, b)
			}
		} else {
			if len(line) > 0 && isJSONTaskStart(line) {
				break
			}
			break
		}
	}

found:
	return io.MultiReader(&buf, br)
}

// isJSONTaskStart returns true when line is a jsonl task-start event.
// Uses a fast bytes.Contains pre-check before JSON parsing to avoid
// allocating for every non-task line.
func isJSONTaskStart(line []byte) bool {
	if !bytes.Contains(line, []byte("v2_playbook_on_task_start")) &&
		!bytes.Contains(line, []byte("v2_runner_on_start")) {
		return false
	}
	var ev struct {
		Event string `json:"_event"`
	}
	return json.Unmarshal(line, &ev) == nil &&
		(ev.Event == "v2_playbook_on_task_start" || ev.Event == "v2_runner_on_start")
}

// runExecPanel starts a standalone Bubble Tea program showing the execution
// panel for the given proc. Called for direct CLI invocations (no sidebar).
//
// vsh_stdout content (service tables, db listings, etc.) is written by
// readTaskCmd directly to a shared *bytes.Buffer, bypassing the BubbleTea
// message queue so that tea.Quit cannot race ahead of ansibleOutputMsg
// delivery. The buffer is printed after p.Run() returns.
func runExecPanel(command, version string, proc *exec.Cmd, ansibleOut io.Reader, cleanup func()) error {
	width, height := 80, 24
	if w, h, err := term.GetSize(os.Stdout.Fd()); err == nil {
		width, height = w, h
	}

	var outputBuf bytes.Buffer

	m := standaloneExecModel{
		execPanel: NewExecModel(command, version, proc, ansibleOut, &outputBuf, cleanup, width, height),
	}

	p := tea.NewProgram(m)
	final, err := p.Run()

	drainStdin()

	if err != nil {
		return err
	}

	fm, hasFinal := final.(standaloneExecModel)

	if hasFinal && fm.execPanel.LogViewRequested() {
		printLogView(fm.execPanel.FullLogLines())
	}

	if outputBuf.Len() > 0 {
		fmt.Println()
		fmt.Println(outputBuf.String())
	}

	if hasFinal {
		writeDebugLog(platform.LogFile(), fm.execPanel.FullLogLines())

		if fm.execPanel.Err() != nil {
			fmt.Fprintf(os.Stderr, "  log: %s\n", platform.LogFile())
		}
	}

	if devDir := platform.DevRepoDir(); devDir != "" {
		fmt.Fprintf(os.Stderr, "\n[dev] repo: %s\n", devDir)
	}

	if hasFinal {
		return fm.execPanel.Err()
	}
	return nil
}

// writeDebugLog appends formatted log lines to the debug log file.
// Called after each TUI run to provide a human-readable record alongside
// the stderr output already written during the run.
func writeDebugLog(path string, lines []string) {
	if len(lines) == 0 {
		return
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	for _, line := range lines {
		fmt.Fprintln(f, line)
	}
}

// drainStdin discards any terminal capability-query responses left in stdin
// (e.g. DECRPM, Unicode Mode) that BubbleTea left behind, preventing visual noise.
func drainStdin() {
	fd := int(os.Stdin.Fd())

	if err := syscall.SetNonblock(fd, true); err != nil {
		return
	}
	defer syscall.SetNonblock(fd, false) //nolint:errcheck // best-effort restore; failure is non-fatal

	buf := make([]byte, 256)
	for {
		n, err := os.Stdin.Read(buf)
		if n == 0 || err != nil {
			break
		}
	}
}

// standaloneExecModel wraps ExecModel as a full standalone Bubble Tea program.
// Used for direct CLI invocations (no launcher sidebar).
type standaloneExecModel struct {
	execPanel ExecModel
}

func (standalone standaloneExecModel) Init() tea.Cmd {
	return standalone.execPanel.Init()
}

func (standalone standaloneExecModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd
	standalone.execPanel, cmd = standalone.execPanel.Update(msg)
	return standalone, cmd
}

func (standalone standaloneExecModel) View() tea.View {
	return tea.NewView(standalone.execPanel.View())
}

// resolveRunOpts walks the cobra command tree to find the matching command
// for the given args and builds an ansible.RunOpts, separating flags from positional args.
func resolveRunOpts(root *cobra.Command, args []string) (*ansible.RunOpts, error) {
	if len(args) == 0 {
		return nil, fmt.Errorf("no command specified")
	}

	cmd, remaining, err := root.Find(args)
	if err != nil || cmd == root {
		return nil, fmt.Errorf("unknown command: %s", args[0])
	}

	playbook := cmd.Annotations["playbook"]
	if playbook == "" {
		playbook = strings.SplitN(cmd.Use, " ", 2)[0]
	}

	positionalArgs, opts, verbose, debug := ansible.SplitTokens(remaining)

	workDir, err := os.Getwd()
	if err != nil {
		workDir = "."
	}

	return &ansible.RunOpts{
		Playbook: playbook,
		Args:     positionalArgs,
		Opts:     opts,
		WorkDir:  workDir,
		Verbose:  verbose,
		Debug:    debug,
	}, nil
}

// printLogView displays the full execution log to stdout with a header and
// divider line. Once BubbleTea has exited, the user can scroll the terminal
// history and select text naturally.
func printLogView(lines []string) {
	fmt.Println()
	fmt.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
	fmt.Println("Execution log:")
	fmt.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
	fmt.Println()

	if len(lines) == 0 {
		fmt.Println("(no log output captured)")
	} else {
		for _, line := range lines {
			fmt.Println(line)
		}
	}

	fmt.Println()
}
