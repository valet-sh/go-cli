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
	"encoding/json"
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
)

// ansibleHostResult holds the per-host fields we extract from jsonl events.
type ansibleHostResult struct {
	VshStdout    string              `json:"vsh_stdout"`
	Msg          string              `json:"msg"`
	Stderr       string              `json:"stderr"`
	Stdout       string              `json:"stdout"`
	ModuleStderr string              `json:"module_stderr"`
	ModuleStdout string              `json:"module_stdout"`
	Changed      bool                `json:"changed"`
	Failed       bool                `json:"failed"`
	Unreachable  bool                `json:"unreachable"`
	RC           int                 `json:"rc"`
	Cmd          json.RawMessage     `json:"cmd"`
	Item         json.RawMessage     `json:"item"`
	Results      []ansibleHostResult `json:"results"`
	LoopVar      string              `json:"ansible_loop_var"`
}

func (r *ansibleHostResult) UnmarshalJSON(data []byte) error {
	type plain ansibleHostResult
	var p plain
	if err := json.Unmarshal(data, &p); err != nil {
		return err
	}
	if len(p.Item) == 0 && p.LoopVar != "" {
		var raw map[string]json.RawMessage
		if err := json.Unmarshal(data, &raw); err == nil {
			p.Item = raw[p.LoopVar]
		}
	}
	*r = ansibleHostResult(p)
	return nil
}

func failedResults(r *ansibleHostResult) []*ansibleHostResult {
	if len(r.Results) == 0 {
		return []*ansibleHostResult{r}
	}
	var out []*ansibleHostResult
	for i := range r.Results {
		if r.Results[i].Failed || r.Results[i].Unreachable {
			out = append(out, &r.Results[i])
		}
	}
	if len(out) == 0 {
		return []*ansibleHostResult{r}
	}
	return out
}

func errorSummary(r *ansibleHostResult) string {
	msg := firstLine(r.Msg)
	cmd := formatCmd(r.Cmd)

	if cmd != "" && (msg == "" || msg == "non-zero return code") {
		if l := lastLine(r.Stderr); l != "" {
			msg = l
		} else if l := lastLine(r.Stdout); l != "" {
			msg = l
		}
	}
	if msg == "" {
		msg = firstLine(r.Stderr)
	}
	if msg == "" {
		return ""
	}

	item := formatCmd(r.Item)
	if item == "null" {
		item = ""
	}
	if cmd == "" && item != "" {
		msg += " (item: " + item + ")"
	}
	if r.RC != 0 {
		msg += fmt.Sprintf(" (rc=%d)", r.RC)
	}
	if cmd != "" {
		label := item
		if label == "" {
			label = shortCmd(cmd)
		}
		msg += " · " + label
	}
	return msg
}

func firstLine(s string) string {
	for l := range strings.SplitSeq(s, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			return l
		}
	}
	return ""
}

func lastLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if l := strings.TrimSpace(lines[i]); l != "" {
			return l
		}
	}
	return ""
}

func shortCmd(cmd string) string {
	fields := strings.Fields(cmd)
	for i, f := range fields {
		if !strings.HasPrefix(f, "/") {
			continue
		}
		parts := strings.Split(strings.TrimRight(f, "/"), "/")
		if n := len(parts); n > 2 {
			fields[i] = strings.Join(parts[n-2:], "/")
		}
	}
	return strings.Join(fields, " ")
}

// ansibleJSONEvent is the schema for ansible.posix.jsonl output lines.
// Each line is a complete JSON object with an _event field identifying the hook.
type ansibleJSONEvent struct {
	Event string `json:"_event"`
	Task  struct {
		Name string `json:"name"`
		Path string `json:"path"`
	} `json:"task"`
	Hosts map[string]*ansibleHostResult `json:"hosts"`
	// Stats is populated for v2_playbook_on_stats events.
	Stats map[string]struct {
		Ok          int `json:"ok"`
		Failures    int `json:"failures"`
		Unreachable int `json:"unreachable"`
		Changed     int `json:"changed"`
		Skipped     int `json:"skipped"`
		Rescued     int `json:"rescued"`
		Ignored     int `json:"ignored"`
	} `json:"stats"`
}

// parseJSONEvent parses a single jsonl line and returns an ansibleEventMsg,
// or nil to continue reading without a BubbleTea round-trip.
//
// vsh_stdout content is written directly to out (bypasses BubbleTea queue).
func parseJSONEvent(line []byte, out *bytes.Buffer) tea.Msg {
	if len(line) == 0 || line[0] != '{' {
		return nil
	}
	var ev ansibleJSONEvent
	if err := json.Unmarshal(line, &ev); err != nil {
		return nil
	}

	fullLines := formatFullEventLines(ev)

	switch ev.Event {
	case "v2_playbook_on_task_start", "v2_runner_on_start":
		name := strings.TrimSpace(ev.Task.Name)
		if name == "" {
			return nil
		}
		taskLine := taskLogPrefix + name + "] " + strings.Repeat("*", 20)
		// Meta-tasks (include_tasks, import_tasks, etc.) are logged but do not
		// update the spinner — they execute instantly and the real work happens
		// in the included file.
		if isMetaTask(name) {
			return ansibleEventMsg{logLines: []string{taskLine}, fullLines: fullLines}
		}
		return ansibleEventMsg{
			taskName:  shortTaskName(name),
			logLines:  []string{taskLine},
			fullLines: fullLines,
		}

	case "v2_runner_on_ok":
		var logLines []string
		for _, result := range ev.Hosts {
			// vsh_stdout goes directly to the shared buffer (bypasses BubbleTea).
			if result.VshStdout != "" && out != nil {
				if out.Len() > 0 {
					out.WriteByte('\n')
				}
				out.WriteString(result.VshStdout)
			}
			// stderr / stdout on ok = warnings — show in log.
			logLines = append(logLines, formatWarningLines(ev.Task.Name, result)...)
		}
		if len(logLines) > 0 || len(fullLines) > 0 {
			return ansibleEventMsg{logLines: logLines, fullLines: fullLines}
		}
		return nil

	case "v2_runner_on_failed":
		var logLines []string
		var errMsg string
		for _, host := range ev.Hosts {
			for _, result := range failedResults(host) {
				logLines = append(logLines, formatFailureLines(ev.Task.Name, result)...)
				if errMsg == "" {
					errMsg = errorSummary(result)
				}
			}
		}
		if len(logLines) > 0 || len(fullLines) > 0 {
			return ansibleEventMsg{logLines: logLines, fullLines: fullLines, errMsg: errMsg}
		}
		return nil

	case "v2_runner_on_unreachable":
		var logLines []string
		var errMsg string
		for _, host := range ev.Hosts {
			for _, result := range failedResults(host) {
				logLines = append(logLines, "UNREACHABLE ["+ev.Task.Name+"]")
				logLines = appendItemLine(logLines, result)
				logLines = appendMsgLines(logLines, result.Msg)
				if errMsg == "" {
					errMsg = errorSummary(result)
				}
			}
		}
		if len(logLines) > 0 || len(fullLines) > 0 {
			return ansibleEventMsg{logLines: logLines, fullLines: fullLines, errMsg: errMsg}
		}
		return nil

	case "v2_playbook_on_stats":
		var logLines []string
		logLines = append(logLines, strings.Repeat("─", 60), "PLAY RECAP")
		for host, s := range ev.Stats {
			logLines = append(logLines, fmt.Sprintf(
				"  %-20s ok=%-4d changed=%-4d failed=%-4d unreachable=%-4d skipped=%-4d",
				host, s.Ok, s.Changed, s.Failures, s.Unreachable, s.Skipped,
			))
		}
		return ansibleEventMsg{logLines: logLines, fullLines: fullLines}
	}

	return nil
}

func formatFullEventLines(ev ansibleJSONEvent) []string {
	switch ev.Event {
	case "v2_playbook_on_task_start", "v2_runner_on_start":
		name := strings.TrimSpace(ev.Task.Name)
		if name == "" {
			return nil
		}
		return []string{taskLogPrefix + name + "] " + strings.Repeat("*", 20)}

	case "v2_runner_on_ok":
		var lines []string
		for host, result := range ev.Hosts {
			status := "ok"
			if result.Changed {
				status = "changed"
			}
			lines = append(lines, status+": ["+host+"]")
			lines = append(lines, formatResultFieldLines(result)...)
		}
		return lines

	case "v2_runner_on_failed":
		var lines []string
		for host, result := range ev.Hosts {
			lines = append(lines, "fatal: ["+host+"]: FAILED! =>")
			for _, r := range failedResults(result) {
				lines = append(lines, formatResultFieldLines(r)...)
			}
		}
		if ev.Task.Path != "" {
			lines = append(lines, "Origin: "+ev.Task.Path)
		}
		return lines

	case "v2_runner_on_unreachable":
		var lines []string
		for host, result := range ev.Hosts {
			lines = append(lines, "fatal: ["+host+"]: UNREACHABLE! =>")
			for _, r := range failedResults(result) {
				lines = append(lines, formatResultFieldLines(r)...)
			}
		}
		if ev.Task.Path != "" {
			lines = append(lines, "Origin: "+ev.Task.Path)
		}
		return lines

	case "v2_playbook_on_stats":
		var lines []string
		lines = append(lines, strings.Repeat("─", 60), "PLAY RECAP")
		for host, s := range ev.Stats {
			lines = append(lines, fmt.Sprintf(
				"  %-20s ok=%-4d changed=%-4d unreachable=%-4d failed=%-4d skipped=%-4d rescued=%-4d ignored=%-4d",
				host, s.Ok, s.Changed, s.Unreachable, s.Failures, s.Skipped, s.Rescued, s.Ignored,
			))
		}
		return lines
	}

	return nil
}

func formatResultFieldLines(r *ansibleHostResult) []string {
	lines := []string{fmt.Sprintf("  changed: %t", r.Changed)}
	lines = appendItemLine(lines, r)
	lines = appendMsgLines(lines, r.Msg)
	if r.RC != 0 {
		lines = append(lines, fmt.Sprintf("  rc:  %d", r.RC))
	}
	if cmd := formatCmd(r.Cmd); cmd != "" {
		lines = append(lines, "  cmd: "+cmd)
	}
	lines = appendBlockField(lines, "stderr", r.Stderr)
	lines = appendBlockField(lines, "stdout", r.Stdout)
	lines = appendBlockField(lines, "module_stderr", r.ModuleStderr)
	lines = appendBlockField(lines, "module_stdout", r.ModuleStdout)
	return lines
}

func appendBlockField(lines []string, label, value string) []string {
	value = strings.TrimSpace(value)
	if value == "" {
		return lines
	}
	lines = append(lines, "  "+label+":")
	for l := range strings.SplitSeq(value, "\n") {
		lines = append(lines, "    "+l)
	}
	return lines
}

// formatWarningLines builds log lines for a successful task that emitted
// stderr or stdout (i.e. warnings from the module).
func formatWarningLines(taskName string, r *ansibleHostResult) []string {
	stderr := strings.TrimSpace(r.Stderr)
	stdout := strings.TrimSpace(r.Stdout)
	if stderr == "" && stdout == "" {
		return nil
	}
	var lines []string
	lines = append(lines, "WARNING ["+taskName+"]")
	if stderr != "" {
		lines = append(lines, "  stderr:")
		for l := range strings.SplitSeq(stderr, "\n") {
			lines = append(lines, "    "+l)
		}
	}
	if stdout != "" {
		lines = append(lines, "  stdout:")
		for l := range strings.SplitSeq(stdout, "\n") {
			lines = append(lines, "    "+l)
		}
	}
	return lines
}

// formatFailureLines builds the detailed error block for a failed task.
// Includes msg, rc, cmd, stderr, and stdout so developers can diagnose
// failures (e.g. composer errors) without leaving the TUI.
func formatFailureLines(taskName string, r *ansibleHostResult) []string {
	var lines []string
	lines = append(lines, "FAILED ["+taskName+"]")
	lines = appendItemLine(lines, r)
	lines = appendMsgLines(lines, r.Msg)
	if r.RC != 0 {
		lines = append(lines, fmt.Sprintf("  rc:  %d", r.RC))
	}
	if cmd := formatCmd(r.Cmd); cmd != "" {
		lines = append(lines, "  cmd: "+cmd)
	}
	if stderr := strings.TrimSpace(r.Stderr); stderr != "" {
		lines = append(lines, "  stderr:")
		for l := range strings.SplitSeq(stderr, "\n") {
			lines = append(lines, "    "+l)
		}
	}
	if stdout := strings.TrimSpace(r.Stdout); stdout != "" {
		lines = append(lines, "  stdout:")
		for l := range strings.SplitSeq(stdout, "\n") {
			lines = append(lines, "    "+l)
		}
	}
	return lines
}

func appendItemLine(lines []string, r *ansibleHostResult) []string {
	if item := formatCmd(r.Item); item != "" && item != "null" {
		return append(lines, "  item: "+item)
	}
	return lines
}

func appendMsgLines(lines []string, msg string) []string {
	msg = strings.TrimRight(msg, "\n")
	if msg == "" {
		return lines
	}
	if !strings.Contains(msg, "\n") {
		return append(lines, "  msg: "+msg)
	}
	lines = append(lines, "  msg: |-")
	for l := range strings.SplitSeq(msg, "\n") {
		lines = append(lines, "    "+l)
	}
	return lines
}

// formatCmd renders the cmd field (string or []string) as a single string.
func formatCmd(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	var arr []string
	if err := json.Unmarshal(raw, &arr); err == nil {
		return strings.Join(arr, " ")
	}
	return string(raw)
}

// isMetaTask returns true if the task name represents an Ansible meta-task
// that controls flow (include_tasks, import_tasks, etc.) rather than doing real work.
// These tasks execute instantly and should not be shown as "current task" since
// the real work happens in the included/imported file.
func isMetaTask(taskName string) bool {
	// Extract the task name part (after the last " : " if role-qualified)
	base := taskName
	if i := strings.LastIndex(taskName, " : "); i >= 0 {
		base = taskName[i+3:]
	}

	switch base {
	case "include_tasks", "import_tasks", "include_role", "import_role":
		return true
	}
	return false
}

// shortTaskName extracts the meaningful description part of a task name
// by removing the role prefix and keeping only the task description.
//
// Examples:
//
//	"valet-init-instance : workflows » magento2 » services » php | ensure php8.3 is started"
//	→ "ensure php8.3 is started"
//
//	"shared-variables : set 'current_os' var"
//	→ "set 'current_os' var"
//
//	"Gathering Facts"
//	→ "Gathering Facts"
func shortTaskName(taskName string) string {
	// If the task has a pipe separator, use what comes after it
	// (e.g., "role : workflows » ... | task description" → "task description")
	if i := strings.LastIndex(taskName, " | "); i >= 0 {
		return strings.TrimSpace(taskName[i+3:])
	}

	// No pipe — try to strip the role prefix (e.g., "role : task" → "task")
	if _, after, ok := strings.Cut(taskName, " : "); ok {
		return strings.TrimSpace(after)
	}

	// No role prefix (e.g., "Gathering Facts" or "set variables")
	return taskName
}
