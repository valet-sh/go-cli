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
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

func TestExecModelInit(t *testing.T) {
	m := NewExecModel("service start php83", "1.0.0", nil, nil, nil, nil, 80, 24)
	cmd := m.Init()
	if cmd == nil {
		t.Error("Init() should return a non-nil Cmd (tick + wait)")
	}
}

func TestExecModelDoneOnExecDoneMsg(t *testing.T) {
	m := NewExecModel("install", "1.0.0", nil, nil, nil, nil, 80, 24)

	rm, _ := m.Update(execDoneMsg{err: nil})

	if !rm.IsDone() {
		t.Error("expected IsDone() true after execDoneMsg")
	}
	if rm.Err() != nil {
		t.Errorf("expected nil error, got %v", rm.Err())
	}
	// Success — should NOT show prompt.
	if rm.awaitingLogPrompt {
		t.Error("should not show log prompt on success")
	}
}

func TestExecModelFailedExecDoneMsg(t *testing.T) {
	m := NewExecModel("service start php83", "1.0.0", nil, nil, nil, nil, 80, 24)
	sentinel := errors.New("exit status 1")

	rm, _ := m.Update(execDoneMsg{err: sentinel})

	if !rm.IsDone() {
		t.Error("expected IsDone() true after failed execDoneMsg")
	}
	if rm.Err() != sentinel {
		t.Errorf("expected sentinel error, got %v", rm.Err())
	}
	// Prompt is NOT shown immediately — it appears on first keypress.
	if rm.awaitingLogPrompt {
		t.Error("awaitingLogPrompt should not be set by execDoneMsg (only on keypress)")
	}
}

func TestExecModelLogPromptYesOpensViewer(t *testing.T) {
	m := NewExecModel("install", "1.0.0", nil, nil, nil, nil, 80, 24)

	// Trigger failure.
	m, _ = m.Update(execDoneMsg{err: errors.New("exit status 1")})

	// Press Y directly — sets logViewRequested and quits.
	m2, cmd := m.Update(tea.KeyPressMsg{Text: "y"})
	if m2.awaitingLogPrompt {
		t.Error("awaitingLogPrompt should not be set when Y pressed directly")
	}
	if !m2.logViewRequested {
		t.Error("expected logViewRequested to be true after Y")
	}
	if cmd == nil {
		t.Error("expected tea.Quit to be returned after Y")
	}
}

func TestExecModelLogPromptEnterOpensViewer(t *testing.T) {
	m := NewExecModel("install", "1.0.0", nil, nil, nil, nil, 80, 24)
	m, _ = m.Update(execDoneMsg{err: errors.New("exit status 1")})

	// Any other key triggers the prompt; then Enter confirms.
	m, _ = m.Update(tea.KeyPressMsg{Text: "x"}) // show prompt
	if !m.awaitingLogPrompt {
		t.Fatal("expected awaitingLogPrompt after non-y keypress on failure")
	}
	m2, cmd := m.Update(tea.KeyPressMsg{Text: "enter"})
	if m2.awaitingLogPrompt {
		t.Error("awaitingLogPrompt should be cleared after Enter")
	}
	if !m2.logViewRequested {
		t.Error("expected logViewRequested to be true after Enter")
	}
	if cmd == nil {
		t.Error("expected tea.Quit to be returned after Enter")
	}
}

func TestExecModelLogPromptNoQuits(t *testing.T) {
	m := NewExecModel("install", "1.0.0", nil, nil, nil, nil, 80, 24)
	m, _ = m.Update(execDoneMsg{err: errors.New("exit status 1")})

	// Show prompt with any neutral key, then press N.
	m, _ = m.Update(tea.KeyPressMsg{Text: "x"}) // show prompt
	if !m.awaitingLogPrompt {
		t.Fatal("expected awaitingLogPrompt after neutral keypress")
	}
	_, cmd := m.Update(tea.KeyPressMsg{Text: "n"})
	if cmd == nil {
		t.Error("expected tea.Quit after n in prompt")
	}
}

func TestExecModelLogPromptEscQuits(t *testing.T) {
	m := NewExecModel("install", "1.0.0", nil, nil, nil, nil, 80, 24)
	m, _ = m.Update(execDoneMsg{err: errors.New("exit status 1")})

	_, cmd := m.Update(tea.KeyPressMsg{Text: "esc"})
	if cmd == nil {
		t.Error("expected tea.Quit after esc")
	}
}

func TestExecModelTaskCounting(t *testing.T) {
	m := NewExecModel("install", "1.0.0", nil, nil, nil, nil, 80, 24)

	m.appendLines([]string{"TASK [Gathering Facts]"})
	if m.tasksDone != 1 {
		t.Errorf("expected tasksDone=1, got %d", m.tasksDone)
	}

	m.appendLines([]string{
		"some log output",
		"TASK [Start service]",
		"TASK [Enable service]",
	})
	if m.tasksDone != 3 {
		t.Errorf("expected tasksDone=3, got %d", m.tasksDone)
	}
}

func TestLogLinesAccumulation(t *testing.T) {
	m := NewExecModel("install", "1.0.0", nil, nil, nil, nil, 80, 24)

	m.appendLines([]string{"TASK [Gathering Facts] ****", "ok: [localhost]"})
	m.appendLines([]string{"TASK [Install deps] ****", "changed: [localhost]"})

	if len(m.logLines) != 4 {
		t.Errorf("expected 4 logLines, got %d", len(m.logLines))
	}
	if m.logLines[0] != "TASK [Gathering Facts] ****" {
		t.Errorf("unexpected first logLine: %q", m.logLines[0])
	}
}

func TestExecModelProgressBarRendering(t *testing.T) {
	tests := []struct {
		name        string
		tasksDone   int
		done        bool
		err         error
		currentTask string
		expectedIn  string // What we expect to find in the view (after shortening)
	}{
		{"spinner with no task", 5, false, nil, "", "running..."},
		{"spinner with task", 10, false, nil, "my : task", "task"}, // "my : task" gets shortened to "task"
		{"success state", 20, true, nil, "", "✔"},
		{"failure state", 15, true, errors.New("fail"), "", "✘"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := NewExecModel("install", "1.0.0", nil, nil, nil, nil, 80, 24)
			m.tasksDone = tc.tasksDone
			m.done = tc.done
			m.err = tc.err
			m.currentTask = tc.currentTask

			view := m.progressBarView()

			if !strings.Contains(view, tc.expectedIn) {
				t.Errorf("expected %q in view, got: %s", tc.expectedIn, view)
			}
		})
	}
}

func TestExecModelRenderProgressBarCalculations(t *testing.T) {
	tasks := []struct {
		tasksDone  int
		task       string
		expectedIn string // What should appear in the view (shortened)
	}{
		{0, "first : task", "task"},   // Shortened from "first : task"
		{10, "middle : task", "task"}, // Shortened from "middle : task"
		{20, "last : task", "task"},   // Shortened from "last : task"
	}

	for _, tc := range tasks {
		t.Run(fmt.Sprintf("tasks_%d", tc.tasksDone), func(t *testing.T) {
			m := NewExecModel("install", "1.0.0", nil, nil, nil, nil, 80, 24)
			m.tasksDone = tc.tasksDone
			m.currentTask = tc.task

			view := m.progressBarView()

			if !strings.Contains(view, tc.expectedIn) {
				t.Errorf("expected %q in progress bar view, got: %s", tc.expectedIn, view)
			}
		})
	}
}

func TestExecModelProgressBarView(t *testing.T) {
	// While running: should show spinner + task name (shortened).
	m := NewExecModel("install", "1.0.0", nil, nil, nil, nil, 80, 24)
	m.tasksDone = 10
	m.currentTask = "some : task name"

	view := m.progressBarView()
	// Task name gets shortened to just "task name"
	if !strings.Contains(view, "task name") {
		t.Errorf("expected shortened task name in progress bar, got: %s", view)
	}

	// When done with success: should show checkmark.
	m.done = true
	m.tasksDone = 20
	view = m.progressBarView()
	if !strings.Contains(view, "✔") {
		t.Errorf("expected checkmark in completed bar, got: %s", view)
	}
}

func TestParseJSONEvent(t *testing.T) {
	tests := []struct {
		name            string
		line            string
		wantType        string // "task", "output", "loglines", or "nil"
		wantVal         string // expected taskName (for "task") or buffer content (for "output")
		wantLogLines    int    // minimum number of log lines expected (for "loglines" and "task")
		wantLogContains string // substring expected in joined log lines
	}{
		{
			name:         "task start (lockstep strategy)",
			line:         `{"_event":"v2_playbook_on_task_start","task":{"name":"Gathering Facts","id":"abc"},"_timestamp":"2025-01-01T00:00:00Z"}`,
			wantType:     "task",
			wantVal:      "Gathering Facts",
			wantLogLines: 1,
		},
		{
			name:         "task start (free strategy)",
			line:         `{"_event":"v2_runner_on_start","task":{"name":"Install packages","id":"xyz"},"_timestamp":"2025-01-01T00:00:00Z"}`,
			wantType:     "task",
			wantVal:      "Install packages",
			wantLogLines: 1,
		},
		{
			name:     "runner ok with vsh_stdout",
			line:     `{"_event":"v2_runner_on_ok","task":{"name":"list services"},"hosts":{"localhost":{"changed":true,"vsh_stdout":"service  status\nphp83    running"}},"_timestamp":"2025-01-01T00:00:00Z"}`,
			wantType: "output",
			wantVal:  "service  status\nphp83    running",
		},
		{
			name:     "runner ok without vsh_stdout or warnings",
			line:     `{"_event":"v2_runner_on_ok","task":{"name":"install package"},"hosts":{"localhost":{"changed":false}},"_timestamp":"2025-01-01T00:00:00Z"}`,
			wantType: "fullonly",
		},
		{
			name:            "runner ok with stderr warning",
			line:            `{"_event":"v2_runner_on_ok","task":{"name":"run script"},"hosts":{"localhost":{"changed":false,"stderr":"some warning"}},"_timestamp":"2025-01-01T00:00:00Z"}`,
			wantType:        "loglines",
			wantLogLines:    1,
			wantLogContains: "WARNING",
		},
		{
			name:            "runner failed with msg and stderr",
			line:            `{"_event":"v2_runner_on_failed","task":{"name":"composer install"},"hosts":{"localhost":{"failed":true,"msg":"non-zero return code","rc":1,"stderr":"Problem 1\n  - missing ext-soap","cmd":"composer install"}},"_timestamp":"2025-01-01T00:00:00Z"}`,
			wantType:        "loglines",
			wantLogLines:    3,
			wantLogContains: "FAILED",
		},
		{
			name:            "stats event produces recap",
			line:            `{"_event":"v2_playbook_on_stats","stats":{"localhost":{"ok":5,"failures":1,"unreachable":0,"changed":2,"skipped":0}},"_timestamp":"2025-01-01T00:00:00Z"}`,
			wantType:        "loglines",
			wantLogLines:    3,
			wantLogContains: "PLAY RECAP",
		},
		{
			name:     "play start is ignored",
			line:     `{"_event":"v2_playbook_on_play_start","play":{"name":"service","id":"123"},"_timestamp":"2025-01-01T00:00:00Z"}`,
			wantType: "nil",
		},
		{
			name:     "empty line",
			line:     "",
			wantType: "nil",
		},
		{
			name:     "non-JSON line",
			line:     "not json at all",
			wantType: "nil",
		},
		{
			name:         "task name is shortened",
			line:         `{"_event":"v2_playbook_on_task_start","task":{"name":"some-role : gather info | check conditions"},"_timestamp":"2025-01-01T00:00:00Z"}`,
			wantType:     "task",
			wantVal:      "check conditions",
			wantLogLines: 1,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			msg := parseJSONEvent([]byte(tc.line), &buf)
			switch tc.wantType {
			case "task":
				ev, ok := msg.(ansibleEventMsg)
				if !ok {
					t.Fatalf("expected ansibleEventMsg, got %T", msg)
				}
				if ev.taskName != tc.wantVal {
					t.Errorf("task name: got %q, want %q", ev.taskName, tc.wantVal)
				}
				if len(ev.logLines) < tc.wantLogLines {
					t.Errorf("expected at least %d logLines, got %d", tc.wantLogLines, len(ev.logLines))
				}
			case "output":
				if ev, ok := msg.(ansibleEventMsg); ok && len(ev.logLines) != 0 {
					t.Errorf("expected no concise logLines for vsh_stdout event, got %v", ev.logLines)
				}
				if buf.String() != tc.wantVal {
					t.Errorf("output buffer: got %q, want %q", buf.String(), tc.wantVal)
				}
			case "fullonly":
				ev, ok := msg.(ansibleEventMsg)
				if !ok {
					t.Fatalf("expected ansibleEventMsg (full-only), got %T", msg)
				}
				if len(ev.logLines) != 0 {
					t.Errorf("expected no concise logLines, got %v", ev.logLines)
				}
				if len(ev.fullLines) == 0 {
					t.Errorf("expected fullLines to be populated")
				}
			case "loglines":
				ev, ok := msg.(ansibleEventMsg)
				if !ok {
					t.Fatalf("expected ansibleEventMsg for loglines, got %T", msg)
				}
				if len(ev.logLines) < tc.wantLogLines {
					t.Errorf("expected at least %d logLines, got %d: %v", tc.wantLogLines, len(ev.logLines), ev.logLines)
				}
				if tc.wantLogContains != "" {
					joined := strings.Join(ev.logLines, "\n")
					if !strings.Contains(joined, tc.wantLogContains) {
						t.Errorf("expected %q in logLines, got: %s", tc.wantLogContains, joined)
					}
				}
			case "nil":
				if msg != nil {
					t.Errorf("expected nil, got %T: %v", msg, msg)
				}
				if buf.Len() != 0 {
					t.Errorf("expected empty buffer, got %q", buf.String())
				}
			}
		})
	}
}

func TestReadTaskCmdParsesJSONTaskStart(t *testing.T) {
	// Simulate a pipe that sends: a play_start event (ignored) then a task_start.
	playSt := `{"_event":"v2_playbook_on_play_start","play":{"name":"service"},"_timestamp":"t"}` + "\n"
	taskSt := `{"_event":"v2_playbook_on_task_start","task":{"name":"Gathering Facts"},"_timestamp":"t"}` + "\n"

	pr, pw := io.Pipe()
	go func() {
		pw.Write([]byte(playSt))
		pw.Write([]byte(taskSt))
		pw.Close()
	}()

	msg := readTaskCmd(pr, nil)()

	ev, ok := msg.(ansibleEventMsg)
	if !ok {
		t.Fatalf("expected ansibleEventMsg, got %T", msg)
	}
	if ev.taskName != "Gathering Facts" {
		t.Errorf("expected %q, got %q", "Gathering Facts", ev.taskName)
	}
}

func TestReadTaskCmdWritesVshStdoutToBuffer(t *testing.T) {
	line := `{"_event":"v2_runner_on_ok","task":{"name":"list services"},"hosts":{"localhost":{"vsh_stdout":"table content"}},"_timestamp":"t"}` + "\n"
	taskLine := `{"_event":"v2_playbook_on_task_start","task":{"name":"done"},"_timestamp":"t"}` + "\n"

	pr, pw := io.Pipe()
	go func() {
		pw.Write([]byte(line))
		pw.Write([]byte(taskLine))
		pw.Close()
	}()

	var buf bytes.Buffer
	msg := readTaskCmd(pr, &buf)()
	ev, ok := msg.(ansibleEventMsg)
	if !ok {
		t.Fatalf("expected ansibleEventMsg after vsh_stdout, got %T", msg)
	}
	if len(ev.logLines) != 0 {
		t.Errorf("expected no concise logLines for vsh_stdout event, got %v", ev.logLines)
	}
	if buf.String() != "table content" {
		t.Errorf("expected buffer %q, got %q", "table content", buf.String())
	}

	msg = readTaskCmd(pr, &buf)()
	ev, ok = msg.(ansibleEventMsg)
	if !ok {
		t.Fatalf("expected ansibleEventMsg for task start, got %T", msg)
	}
	if ev.taskName != "done" {
		t.Errorf("expected task name %q, got %q", "done", ev.taskName)
	}
}

func TestIsMetaTask(t *testing.T) {
	// Test meta-task detection
	tests := []struct {
		name       string
		input      string
		isMetaTask bool
	}{
		{
			name:       "include_tasks is meta",
			input:      "valet-init-instance : include_tasks",
			isMetaTask: true,
		},
		{
			name:       "import_tasks is meta",
			input:      "valet-init-instance : import_tasks",
			isMetaTask: true,
		},
		{
			name:       "include_role is meta",
			input:      "some-role : include_role",
			isMetaTask: true,
		},
		{
			name:       "import_role is meta",
			input:      "some-role : import_role",
			isMetaTask: true,
		},
		{
			name:       "include_tasks without role is meta",
			input:      "include_tasks",
			isMetaTask: true,
		},
		{
			name:       "real task is not meta",
			input:      "valet-init-instance : ensure elasticsearch is started",
			isMetaTask: false,
		},
		{
			name:       "task with pipe separator is not meta",
			input:      "valet-init-instance : workflows » magento2 » services » php | ensure php8.3 is started",
			isMetaTask: false,
		},
		{
			name:       "Gathering Facts is not meta",
			input:      "Gathering Facts",
			isMetaTask: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := isMetaTask(tc.input)
			if got != tc.isMetaTask {
				t.Errorf("isMetaTask(%q) = %v, want %v", tc.input, got, tc.isMetaTask)
			}
		})
	}
}

func TestShortTaskName(t *testing.T) {
	// Test task name shortening
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "role-qualified with pipe separator",
			input: "valet-init-instance : workflows » magento2 » services » php | ensure php8.3 is started",
			want:  "ensure php8.3 is started",
		},
		{
			name:  "role-qualified without pipe",
			input: "shared-variables : set 'current_os' var",
			want:  "set 'current_os' var",
		},
		{
			name:  "Gathering Facts",
			input: "Gathering Facts",
			want:  "Gathering Facts",
		},
		{
			name:  "include_tasks with role",
			input: "valet-init-instance : include_tasks",
			want:  "include_tasks",
		},
		{
			name:  "long workflow task",
			input: "valet-init-instance : workflows » magento2 » services » elasticsearch | wait for elasticsearch to be reachable",
			want:  "wait for elasticsearch to be reachable",
		},
		{
			name:  "empty after pipe (keep full)",
			input: "valet-init-instance : some task | ",
			want:  "",
		},
		{
			name:  "multiple pipes (last one wins)",
			input: "valet-init-instance : task | part 1 | part 2",
			want:  "part 2",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := shortTaskName(tc.input)
			if got != tc.want {
				t.Errorf("shortTaskName(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}

func TestParseJSONEventMetaTaskSkipsSpinner(t *testing.T) {
	// Meta-tasks should produce a log line but must NOT update the spinner (taskName empty).
	line := `{"_event":"v2_playbook_on_task_start","task":{"name":"some-role : include_tasks"},"_timestamp":"t"}`
	var buf bytes.Buffer
	msg := parseJSONEvent([]byte(line), &buf)

	ev, ok := msg.(ansibleEventMsg)
	if !ok {
		t.Fatalf("expected ansibleEventMsg, got %T", msg)
	}
	if ev.taskName != "" {
		t.Errorf("meta-task should not set taskName, got %q", ev.taskName)
	}
	if len(ev.logLines) == 0 {
		t.Error("meta-task should still produce a log line")
	}

	// Real task must update taskName.
	realLine := `{"_event":"v2_playbook_on_task_start","task":{"name":"some-role : ensure service is started"},"_timestamp":"t"}`
	msg2 := parseJSONEvent([]byte(realLine), &buf)
	ev2, ok := msg2.(ansibleEventMsg)
	if !ok {
		t.Fatalf("expected ansibleEventMsg for real task, got %T", msg2)
	}
	if ev2.taskName == "" {
		t.Error("real task should set taskName")
	}
	if !strings.Contains(ev2.taskName, "ensure service is started") {
		t.Errorf("unexpected taskName: %q", ev2.taskName)
	}
}

// ---------------------------------------------------------------------------
// waitForFirstJSONTask
// ---------------------------------------------------------------------------

func TestWaitForFirstJSONTask_BlocksOnPlayStart(t *testing.T) {
	// jsonl emits play_start before vars_prompt; task_start fires after.
	// waitForFirstJSONTask must pass through play_start and only unblock
	// on the first task-start event, preserving all bytes.
	playSt := `{"_event":"v2_playbook_on_play_start","play":{"name":"service"},"_timestamp":"t"}` + "\n"
	taskSt := `{"_event":"v2_playbook_on_task_start","task":{"name":"Gathering Facts"},"_timestamp":"t"}` + "\n"
	extra := `{"_event":"v2_runner_on_ok","task":{"name":"list"},"_timestamp":"t"}` + "\n"
	input := playSt + taskSt + extra

	result := waitForFirstJSONTask(strings.NewReader(input))
	got, err := io.ReadAll(result)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if string(got) != input {
		t.Errorf("all bytes should be preserved\ngot:  %q\nwant: %q", string(got), input)
	}
}

func TestWaitForFirstJSONTask_EarlyEOF(t *testing.T) {
	// Only a play_start, then EOF — no task ever starts. Must return without blocking.
	input := `{"_event":"v2_playbook_on_play_start","play":{"name":"service"},"_timestamp":"t"}` + "\n"
	result := waitForFirstJSONTask(strings.NewReader(input))
	got, err := io.ReadAll(result)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if string(got) != input {
		t.Errorf("early EOF: bytes should be preserved\ngot:  %q\nwant: %q", string(got), input)
	}
}

func TestWaitForFirstJSONTask_EmptyInput(t *testing.T) {
	result := waitForFirstJSONTask(strings.NewReader(""))
	got, _ := io.ReadAll(result)
	if len(got) != 0 {
		t.Errorf("expected empty output, got %q", string(got))
	}
}

func TestIsJSONTaskStart(t *testing.T) {
	tests := []struct {
		line string
		want bool
	}{
		{`{"_event":"v2_playbook_on_task_start","task":{"name":"Gathering Facts"},"_timestamp":"t"}`, true},
		{`{"_event":"v2_runner_on_start","task":{"name":"install pkg"},"_timestamp":"t"}`, true},
		{`{"_event":"v2_playbook_on_play_start","play":{"name":"service"},"_timestamp":"t"}`, false},
		{`{"_event":"v2_runner_on_ok","task":{"name":"ok"},"_timestamp":"t"}`, false},
		{`{"_event":"v2_playbook_on_stats","stats":{},"_timestamp":"t"}`, false},
		{`not json`, false},
		{``, false},
	}
	for _, tc := range tests {
		got := isJSONTaskStart([]byte(tc.line))
		if got != tc.want {
			t.Errorf("isJSONTaskStart(%q) = %v, want %v", tc.line, got, tc.want)
		}
	}
}

// ---------------------------------------------------------------------------
// Quit-on-EOF ordering: vsh_stdout must be written before tea.Quit fires
// ---------------------------------------------------------------------------

// TestExecModelQuitAfterStdoutDrained verifies that the model does NOT quit
// when execDoneMsg arrives, and DOES quit only after ansibleTaskMsg("") (EOF)
// — ensuring vsh_stdout has been fully written to the output buffer before
// p.Run() returns.
func TestExecModelQuitAfterStdoutDrained(t *testing.T) {
	m := NewExecModel("service list", "1.0.0", nil, nil, nil, nil, 80, 24)
	m.done = false

	// Simulate execDoneMsg arriving (process exited, success).
	em2, cmd2 := m.Update(execDoneMsg{err: nil})

	if !em2.done {
		t.Error("execDoneMsg: done should be true")
	}
	// Must NOT quit here — stdout pipe not yet drained.
	// execDoneMsg should return nil (not tea.Quit) in success CLI mode.
	if cmd2 != nil {
		t.Errorf("execDoneMsg should not return a cmd before stdout is drained, got %T", cmd2)
	}

	// Simulate ansibleEventMsg{eof:true} — EOF from readTaskCmd (stdout fully drained).
	_, cmd3 := em2.Update(ansibleEventMsg{eof: true})

	// Now tea.Quit MUST be returned (CLI mode, no error, done=true).
	if cmd3 == nil {
		t.Error("ansibleEventMsg{eof:true} after execDoneMsg should return tea.Quit cmd, got nil")
	}
}

// TestExecModelNoQuitOnDoneWithoutEOF verifies that the model does not quit
// when execDoneMsg arrives if ansibleTaskMsg("") hasn't fired yet.
func TestExecModelNoQuitOnDoneWithoutEOF(t *testing.T) {
	// Use a real (blocking) pipe so readTaskCmd is re-queued rather than returning nil.
	pr, _ := io.Pipe()
	m := NewExecModel("service list", "1.0.0", nil, pr, nil, nil, 80, 24)

	// execDoneMsg: should mark done but NOT quit.
	em, cmd := m.Update(execDoneMsg{err: nil})

	if !em.done {
		t.Error("expected done=true after execDoneMsg")
	}
	// execDoneMsg on success must return nil (not tea.Quit).
	if cmd != nil {
		t.Errorf("execDoneMsg should not return a cmd before stdout is drained, got non-nil")
	}

	// Simulate an event arriving while done=true (stdout still being read).
	_, cmd2 := em.Update(ansibleEventMsg{taskName: "Gathering Facts"})

	// Should re-queue readTaskCmd (cmd2 non-nil), not quit.
	if cmd2 == nil {
		t.Error("expected readTaskCmd to be re-queued when task name arrives after done, got nil")
	}
}
