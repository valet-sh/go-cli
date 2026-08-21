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

// Package updater performs a periodic background check for new valet-sh-cli
// releases and valet-sh playbook updates, prompting the user when available.
package updater

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/valet-sh/cli/constants"
	"github.com/valet-sh/cli/internal/helper"
	"github.com/valet-sh/cli/internal/style"
)

var PlaybookBranch = GetCurrentReleaseChannel()

const (
	checkInterval     = 7 * 24 * time.Hour
	timestampFile     = constants.VshEtcPath + "/.last_update_check"
	apiTimeout        = 3 * time.Second
	upgradeAPITimeout = 15 * time.Second
)

// releaseResponse is the subset of the GitHub releases API we care about.
type releaseResponse struct {
	TagName string `json:"tag_name"`
}

// Check runs the periodic update check for both CLI and Ansible playbook repo.
// It is a no-op when:
//   - the check was run less than checkInterval ago
//   - the GitHub API is unreachable (fails silently)
//   - currentVersion is "dev" (local development build)
//
// originalArgs is os.Args so the command can be re-executed after a CLI update.
// repoDir is the path to the valet-sh Ansible repo (typically platform.RepoDir()).
func Check(currentVersion string, originalArgs []string, repoDir string) {
	if currentVersion == "dev" {
		return
	}

	if !checkDue() {
		return
	}

	// Always write the timestamp first so a network error doesn't cause
	// the check to hammer the API on every subsequent invocation.
	writeTimestamp()

	cliNewer, cliLatest := checkCliUpdate(currentVersion)
	ansibleNewer := checkAnsibleUpdate(repoDir)

	if !cliNewer && !ansibleNewer {
		return
	}

	fmt.Println()

	cliUpdated := false
	if cliNewer {
		printCliUpdatePrompt(currentVersion, cliLatest)
		if askYesNo() {
			fmt.Println()
			cliUpdated = promptSelfUpgrade()
		} else {
			fmt.Println(style.Info(os.Stdout, "Skipping. Run 'valet self-upgrade' to upgrade anytime."))
			fmt.Println()
		}
	}

	if ansibleNewer && !cliUpdated {
		printAnsibleUpdatePrompt()
		if askYesNo() {
			fmt.Println()
			promptSelfUpgrade()
		} else {
			fmt.Println(style.Info(os.Stdout, "Skipping. Run 'valet self-upgrade' to upgrade anytime."))
			fmt.Println()
		}
	}

	if cliUpdated {
		reExec(originalArgs)
	}
}

// checkCliUpdate returns true if a newer CLI version is available, along with the latest tag.
func checkCliUpdate(currentVersion string) (newer bool, latest string) {
	latest, err := fetchLatestCliTag(apiTimeout)
	if err != nil {
		return false, ""
	}
	return isNewer(latest, currentVersion), latest
}

// checkAnsibleUpdate returns true if the Ansible repo has updates available.
func checkAnsibleUpdate(repoDir string) bool {
	gitDir := filepath.Join(repoDir, ".git")
	if _, err := os.Stat(gitDir); err != nil {
		return false
	}

	ref, isBranch, err := resolvePlaybookRef(repoDir, PlaybookBranch, false)
	if err != nil {
		return false
	}

	targetCommit, err := gitCommitOf(repoDir, ref, isBranch)
	if err != nil {
		return false
	}

	localHead, err := gitCommitOf(repoDir, "HEAD", false)
	if err != nil {
		return false
	}

	return localHead != targetCommit
}

// promptSelfUpgrade calls valet self-upgrade and returns true if the CLI was updated.
func promptSelfUpgrade() bool {
	cmd := exec.Command("valet.sh", "self-upgrade")
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "%s self-upgrade failed: %v\n", style.Red(os.Stderr, "✘"), err)
		return false
	}
	return true
}

// checkDue returns true if the timestamp file is missing or older than checkInterval.
func checkDue() bool {
	fi, err := os.Stat(timestampFile)
	if err != nil {
		return true
	}
	return time.Since(fi.ModTime()) >= checkInterval
}

// writeTimestamp touches the timestamp file, creating it if necessary.
func writeTimestamp() {
	if err := helper.EnsureOwnedDir(constants.VshEtcPath, constants.VshRootPath); err != nil {
		return
	}
	f, err := os.OpenFile(timestampFile, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return
	}
	_ = f.Close()
}

func fetchLatestCliTag(timeout time.Duration) (string, error) {
	resp, err := githubGet(constants.VshCliReleaseURL, timeout)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("GitHub API returned %d", resp.StatusCode)
	}

	var rel releaseResponse
	if err := json.NewDecoder(resp.Body).Decode(&rel); err != nil {
		return "", err
	}
	return strings.TrimPrefix(rel.TagName, "v"), nil
}

// githubGet performs a GET request to the GitHub API with the standard headers.
func githubGet(url string, timeout time.Duration) (*http.Response, error) {
	client := &http.Client{Timeout: timeout}
	req, err := http.NewRequest(http.MethodGet, url, http.NoBody)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	return client.Do(req)
}

// isNewer returns true when candidate is a higher semver than current.
// Both strings should be in the form "MAJOR.MINOR.PATCH" (no "v" prefix).
func isNewer(candidate, current string) bool {
	c := parseSemver(candidate)
	v := parseSemver(current)
	for i := range c {
		if c[i] > v[i] {
			return true
		}
		if c[i] < v[i] {
			return false
		}
	}
	return false
}

// parseSemver splits a version string into [major, minor, patch] ints.
// Non-numeric pre-release suffixes (e.g. "-99-gabcdef") are stripped.
func parseSemver(v string) [3]int {
	// Strip any git-describe suffix (e.g. "2.9.19-101-gabcdef").
	v = strings.SplitN(v, "-", 2)[0]
	parts := strings.SplitN(v, ".", 3)
	var result [3]int
	for i, p := range parts {
		if i >= 3 {
			break
		}
		result[i], _ = strconv.Atoi(p)
	}
	return result
}

// IsHelpOrVersionCall returns true when the user is asking for help or
// version info — cases where an interactive update prompt is unwelcome.
func IsHelpOrVersionCall(args []string) bool {
	for _, a := range args[1:] {
		switch a {
		case "--help", "-h", "--version", "-v", "help":
			return true
		}
	}
	return false
}

// IsSelfUpgradeCall returns true when the user is running 'valet self-upgrade'
// directly — the periodic check should not run in this case since self-upgrade
// is already the update mechanism.
func IsSelfUpgradeCall(args []string) bool {
	for _, a := range args[1:] {
		if a == "self-upgrade" {
			return true
		}
	}
	return false
}

// printCliUpdatePrompt displays the CLI update notification.
func printCliUpdatePrompt(current, latest string) {
	fmt.Printf("%s %s → %s\n",
		style.Blue(os.Stdout, "▶ New CLI version available:"),
		current,
		style.Green(os.Stdout, latest),
	)
	fmt.Printf("  %s\n", style.Info(os.Stdout, "Run 'valet self-upgrade' to upgrade anytime."))
	fmt.Print("  Update CLI now? [y/N] ")
}

// printAnsibleUpdatePrompt displays the Ansible playbook update notification.
func printAnsibleUpdatePrompt() {
	fmt.Printf("%s\n", style.Blue(os.Stdout, "▶ valet-sh playbook updates are available"))
	fmt.Printf("  %s\n", style.Info(os.Stdout, "Run 'valet self-upgrade' to upgrade anytime."))
	fmt.Print("  Update playbooks now? [y/N] ")
}

// askYesNo reads a single line from stdin and returns true for "y" or "Y".
func askYesNo() bool {
	scanner := bufio.NewScanner(os.Stdin)
	if !scanner.Scan() {
		return false
	}
	answer := strings.TrimSpace(scanner.Text())
	return strings.EqualFold(answer, "y")
}

// reExec replaces the current process with a fresh invocation of the same
// binary and arguments, so the user's original command runs against the
// newly installed version without them having to retype it.
//
// Note: If Exec fails, we silently continue with the current process.
// This is acceptable because the update already succeeded; we just fall back
// to running the command with the old binary.
func reExec(args []string) {
	self, err := exec.LookPath(args[0])
	if err != nil {
		self = args[0]
	}
	// syscall.Exec replaces the process image — no return on success.
	_ = syscall.Exec(self, args, os.Environ())
}
