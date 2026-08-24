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

package updater

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/valet-sh/cli/constants"
	"github.com/valet-sh/cli/internal/helper"
	"github.com/valet-sh/cli/internal/style"
)

var errNoRelease = errors.New("no tagged release available for this channel")
var semverTagRe = regexp.MustCompile(`^v?(\d+)\.(\d+)\.(\d+)(?:[-+].*)?$`)

// SelfUpgrade checks for updates to both the CLI binary and the Ansible
// playbook repo, and applies them if newer versions are available.
//
// Re-executing the original user command after a CLI update is the
// responsibility of the periodic check caller (check.go), not this function.
// Calling reExec here would cause an infinite loop when the user runs
// 'valet self-upgrade' directly.
func SelfUpgrade(currentVersion, repoDir string) error {
	fmt.Println()
	fmt.Println(style.Blue(os.Stdout, "▶ Checking for updates..."))
	fmt.Println()

	cliUpdated, cliErr := upgradeCliIfNeeded(currentVersion)
	if cliErr != nil {
		fmt.Fprintf(os.Stderr, "%s CLI update check failed: %v\n", style.Red(os.Stderr, "✘"), cliErr)
	}

	ansibleUpdated, ansibleErr := EnsurePlaybooks(repoDir, constants.VshPlaybookRepo, PlaybookBranch)
	if ansibleErr != nil {
		fmt.Fprintf(os.Stderr, "%s Ansible playbook update failed: %v\n", style.Red(os.Stderr, "✘"), ansibleErr)
	}

	// Runtime upgrade runs after the ansible pull so that if .runtime_version
	// changed in the playbook repo, we immediately install the new version.
	runtimeUpdated, runtimeErr := EnsureRuntime(repoDir)
	if runtimeErr != nil {
		fmt.Fprintf(os.Stderr, "%s Runtime update failed: %v\n", style.Red(os.Stderr, "✘"), runtimeErr)
	}

	// Only say "everything is up to date" when all checks succeeded and
	// nothing was updated — not when any check failed.
	if cliErr == nil && ansibleErr == nil && runtimeErr == nil &&
		!cliUpdated && !ansibleUpdated && !runtimeUpdated {
		fmt.Println(style.Green(os.Stdout, "✓ Everything is up to date."))
	}

	fmt.Println()
	return nil
}

// upgradeCliIfNeeded checks for a new CLI version and updates the binary
// if a newer version is available. Returns true if an update was performed.
func upgradeCliIfNeeded(currentVersion string) (bool, error) {
	if currentVersion == "dev" {
		fmt.Println(style.Info(os.Stdout, "Development build detected. Skipping CLI update."))
		return false, nil
	}

	latest, err := fetchLatestCliTag(upgradeAPITimeout)
	if err != nil {
		return false, err
	}

	if !isNewer(latest, currentVersion) {
		fmt.Printf("%s CLI is up to date (%s)\n", style.Green(os.Stdout, "✓"), currentVersion)
		return false, nil
	}

	fmt.Printf("%s New CLI version available: %s → %s\n",
		style.Blue(os.Stdout, "▶"), currentVersion, style.Green(os.Stdout, latest))

	goos := runtime.GOOS
	goarch := runtime.GOARCH
	assetName := fmt.Sprintf("valet-%s-%s", goos, goarch)

	fmt.Printf("  Downloading %s...\n", assetName)
	binPath, tmpDir, err := downloadAndVerifyBinary(latest, assetName)
	if err != nil {
		return false, fmt.Errorf("download failed: %w", err)
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	installPath := "/usr/local/bin/valet.sh"
	fmt.Printf("  Installing to %s...\n", installPath)

	if err := os.MkdirAll(filepath.Dir(installPath), 0o755); err != nil {
		return false, fmt.Errorf("failed to create install directory: %w", err)
	}

	// Try direct install first; fall back to sudo when the path is root-owned.
	if err := installBinary(binPath, installPath); err != nil {
		return false, err
	}

	fmt.Printf("%s CLI updated to %s\n", style.Green(os.Stdout, "✓"), latest)
	return true, nil
}

// EnsureRuntime compares the desired runtime version (from
// {repoDir}/.runtime_version) with the installed version, and downloads
// and extracts the new tarball when they differ or nothing is installed yet.
//
// Note: valet-sh/runtime releases do not publish checksums — the download
// is not checksum-verified. This mirrors the behaviour of install.sh.
func EnsureRuntime(repoDir string) (bool, error) {
	desiredFile := filepath.Join(repoDir, ".runtime_version")
	data, err := os.ReadFile(desiredFile)
	if err != nil {
		return false, fmt.Errorf("could not read .runtime_version: %w", err)
	}
	desired := strings.TrimSpace(string(data))
	if desired == "" {
		return false, fmt.Errorf(".runtime_version is empty")
	}

	installed := ""
	if d, readErr := os.ReadFile(constants.VshRuntimeVersionFile); readErr == nil {
		installed = strings.TrimSpace(string(d))
	}

	fmt.Printf("%s Checking for runtime updates...\n", style.Blue(os.Stdout, "▶"))

	if installed == desired {
		fmt.Printf("%s Runtime is up to date (%s)\n", style.Green(os.Stdout, "✓"), desired)
		return false, nil
	}

	if installed != "" {
		fmt.Printf("%s New runtime version available: %s → %s\n",
			style.Blue(os.Stdout, "▶"), installed, style.Green(os.Stdout, desired))
	} else {
		fmt.Printf("%s Installing runtime %s...\n", style.Blue(os.Stdout, "▶"), style.Green(os.Stdout, desired))
	}

	assetName, err := runtimeAssetName()
	if err != nil {
		return false, fmt.Errorf("could not determine runtime asset: %w", err)
	}

	downloadURL := fmt.Sprintf("https://github.com/%s/releases/download/%s/%s",
		constants.VshRuntimeRepo, desired, assetName)

	fmt.Printf("  Downloading %s...\n", assetName)

	tmpDir, err := os.MkdirTemp("", "valet-runtime-*")
	if err != nil {
		return false, fmt.Errorf("failed to create temp dir: %w", err)
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	tarPath := filepath.Join(tmpDir, assetName)
	if err := downloadFile(downloadURL, tarPath); err != nil {
		return false, fmt.Errorf("failed to download runtime: %w", err)
	}

	fmt.Println("  Extracting runtime...")
	if err := extractTar(tarPath, constants.VshRuntimeInstallBase); err != nil {
		return false, err
	}

	fmt.Printf("%s Runtime updated to %s\n", style.Green(os.Stdout, "✓"), desired)
	return true, nil
}

// runtimeAssetName returns the platform-specific tarball name for the current OS/arch.
// Linux:  ubuntu_{codename}-x86_64.tar.gz  (codename from /etc/os-release)
// macOS:  macos-{arm64|x86_64}.tar.gz
func runtimeAssetName() (string, error) {
	arch := runtime.GOARCH
	if arch == "amd64" {
		arch = "x86_64"
	}

	switch runtime.GOOS {
	case "darwin":
		return fmt.Sprintf("macos-%s.tar.gz", arch), nil
	default:
		codename, err := osCodename()
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("ubuntu_%s-%s.tar.gz", codename, arch), nil
	}
}

// osCodename reads VERSION_CODENAME from /etc/os-release.
func osCodename() (string, error) {
	data, err := os.ReadFile("/etc/os-release")
	if err != nil {
		return "", fmt.Errorf("could not read /etc/os-release: %w", err)
	}
	for line := range strings.SplitSeq(string(data), "\n") {
		if after, ok := strings.CutPrefix(line, "VERSION_CODENAME="); ok {
			return strings.Trim(after, `"`), nil
		}
	}
	return "", fmt.Errorf("VERSION_CODENAME not found in /etc/os-release")
}

// extractTar extracts a .tar.gz archive into destDir.
func extractTar(tarPath, destDir string) error {
	if err := helper.EnsureOwnedDir(destDir, constants.VshRootPath); err != nil {
		return fmt.Errorf("failed to reclaim ownership of %s: %w", destDir, err)
	}

	cmd := exec.Command("tar", "-C", destDir, "-xzf", tarPath)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to extract runtime: %w", err)
	}
	return nil
}

func EnsurePlaybooks(repoDir, repoURL, channel string) (bool, error) {
	gitDir := filepath.Join(repoDir, ".git")
	if _, err := os.Stat(gitDir); err != nil {
		return clonePlaybooks(repoDir, repoURL, channel)
	}
	return updatePlaybooks(repoDir, channel)
}

func updatePlaybooks(repoDir, channel string) (bool, error) {
	fmt.Printf("%s Checking for Ansible playbook updates...\n", style.Blue(os.Stdout, "▶"))

	ref, isBranch, err := resolvePlaybookRef(repoDir, channel, true)
	if errors.Is(err, errNoRelease) {
		fmt.Println(style.Info(os.Stdout, "Skipping playbook update."))
		return false, nil
	}
	if err != nil {
		fmt.Printf("%s Could not check Ansible playbook updates: %v\n", style.Blue(os.Stdout, "ℹ"), err)
		return false, nil
	}

	targetCommit, err := gitCommitOf(repoDir, ref, isBranch)
	if err != nil {
		return false, fmt.Errorf("failed to resolve %s: %w", ref, err)
	}

	localHead, err := gitCommitOf(repoDir, "HEAD", false)
	if err != nil {
		return false, fmt.Errorf("failed to get local HEAD: %w", err)
	}

	if localHead == targetCommit {
		fmt.Printf("%s Ansible playbooks are up to date (%s)\n", style.Green(os.Stdout, "✓"), ref)
		return false, nil
	}

	fmt.Printf("  Checking out %s...\n", ref)
	if err := checkoutPlaybookRef(repoDir, ref, isBranch); err != nil {
		return false, fmt.Errorf("failed to checkout %s: %w", ref, err)
	}

	fmt.Printf("%s Ansible playbooks updated to %s\n", style.Green(os.Stdout, "✓"), ref)
	return true, nil
}

func clonePlaybooks(repoDir, repoURL, channel string) (bool, error) {
	fmt.Printf("%s Cloning Ansible playbooks (%s)...\n", style.Blue(os.Stdout, "▶"), repoURL)

	cloneURL := fmt.Sprintf("https://github.com/%s.git", repoURL)
	if err := gitClonePlaybooks(cloneURL, repoDir); err != nil {
		return false, fmt.Errorf("failed to clone playbooks: %w", err)
	}

	ref, isBranch, err := resolvePlaybookRef(repoDir, channel, true)
	if err != nil {
		return false, fmt.Errorf("failed to resolve %s channel: %w", channel, err)
	}

	if err := checkoutPlaybookRef(repoDir, ref, isBranch); err != nil {
		return false, fmt.Errorf("failed to checkout %s: %w", ref, err)
	}

	fmt.Printf("%s Ansible playbooks cloned (%s)\n", style.Green(os.Stdout, "✓"), ref)
	return true, nil
}

func gitClonePlaybooks(cloneURL, repoDir string) error {
	clone := func() error {
		cmd := exec.Command("git", "clone", "--quiet", cloneURL, repoDir)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		return cmd.Run()
	}

	if err := clone(); err == nil {
		return nil
	}

	fmt.Println("  Requesting sudo to create protected path...")
	parent := filepath.Dir(repoDir)
	if err := helper.EnsureOwnedDir(parent, constants.VshRootPath); err != nil {
		return err
	}

	return clone()
}

func resolvePlaybookRef(repoDir, channel string, interactive bool) (ref string, isBranch bool, err error) {
	if err = exec.Command("git", "-C", repoDir, "fetch", "--quiet", "--tags", "origin").Run(); err != nil {
		return "", false, fmt.Errorf("failed to fetch origin: %w", err)
	}

	if channel == constants.VshNextVersion {
		return channel, true, nil
	}

	majorVersion, ok := strings.CutSuffix(channel, ".x")
	if !ok {
		return "", false, fmt.Errorf("invalid release channel: %q", channel)
	}

	out, err := exec.Command("git", "-C", repoDir, "tag").Output()
	if err != nil {
		return "", false, fmt.Errorf("failed to list tags: %w", err)
	}

	if tag := highestSemverTag(strings.Fields(string(out)), majorVersion); tag != "" {
		return tag, false, nil
	}

	if !interactive {
		return "", false, errNoRelease
	}

	fmt.Printf("%s No release found yet for the %s channel.\n", style.Blue(os.Stdout, "ℹ"), channel)
	fmt.Printf("  Switch to the %s branch for testing without a release? [y/N] ", channel)
	if !askYesNo() {
		return "", false, errNoRelease
	}
	return channel, true, nil
}

func highestSemverTag(tags []string, majorVersion string) string {
	best := ""
	bestMinor, bestPatch := -1, -1
	for _, tag := range tags {
		m := semverTagRe.FindStringSubmatch(tag)
		if len(m) < 4 || m[1] != majorVersion {
			continue
		}
		minor, _ := strconv.Atoi(m[2])
		patch, _ := strconv.Atoi(m[3])
		if best == "" || minor > bestMinor || (minor == bestMinor && patch > bestPatch) {
			best, bestMinor, bestPatch = tag, minor, patch
		}
	}
	return best
}

func gitCommitOf(repoDir, ref string, isBranch bool) (string, error) {
	target := ref
	if isBranch {
		target = "origin/" + ref
	}
	out, err := exec.Command("git", "-C", repoDir, "rev-parse", target+"^{commit}").Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func checkoutPlaybookRef(repoDir, ref string, isBranch bool) error {
	var cmd *exec.Cmd
	if isBranch {
		cmd = exec.Command("git", "-C", repoDir, "checkout", "--quiet", "-B", ref, "origin/"+ref)
	} else {
		cmd = exec.Command("git", "-C", repoDir, "checkout", "--quiet", ref)
	}
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// downloadAndVerifyBinary downloads the binary and checksums.txt from GitHub
// Releases, verifies the SHA256 checksum, and returns the path to the
// downloaded binary and the temp directory that contains it. The caller is
// responsible for cleaning up the temp directory after using the binary.
func downloadAndVerifyBinary(version, assetName string) (binPath, tmpDir string, err error) {
	releaseURL := fmt.Sprintf("https://github.com/%s/releases/download/%s", constants.VshCliRepo, version)

	tmpDir, err = os.MkdirTemp("", "valet-upgrade-*")
	if err != nil {
		return "", "", fmt.Errorf("failed to create temp directory: %w", err)
	}

	checksumsPath := filepath.Join(tmpDir, "checksums.txt")
	if err := downloadFile(releaseURL+"/checksums.txt", checksumsPath); err != nil {
		_ = os.RemoveAll(tmpDir)
		return "", "", fmt.Errorf("failed to download checksums: %w", err)
	}

	binaryPath := filepath.Join(tmpDir, assetName)
	if err := downloadFile(releaseURL+"/"+assetName, binaryPath); err != nil {
		_ = os.RemoveAll(tmpDir)
		return "", "", fmt.Errorf("failed to download binary: %w", err)
	}

	if err := verifySha256(binaryPath, checksumsPath, assetName); err != nil {
		_ = os.RemoveAll(tmpDir)
		return "", "", fmt.Errorf("checksum verification failed: %w", err)
	}
	fmt.Printf("  %s Checksum verified\n", style.Green(os.Stdout, "✓"))

	return binaryPath, tmpDir, nil
}

// installBinary copies src to installPath atomically.
// If the destination is not writable by the current user, it retries with sudo.
func installBinary(src, installPath string) error {
	tmpFile := installPath + ".tmp"

	err := copyFile(src, tmpFile)
	if err == nil {
		if chmodErr := os.Chmod(tmpFile, 0o755); chmodErr != nil {
			_ = os.Remove(tmpFile)
			return fmt.Errorf("failed to chmod binary: %w", chmodErr)
		}
		if renameErr := os.Rename(tmpFile, installPath); renameErr != nil {
			_ = os.Remove(tmpFile)
			return fmt.Errorf("failed to install binary: %w", renameErr)
		}
		return nil
	}

	// Permission error — retry with sudo.
	if !os.IsPermission(err) {
		return fmt.Errorf("failed to stage binary: %w", err)
	}

	fmt.Println("  Requesting sudo to install to protected path...")
	cmd := exec.Command("sudo", "install", "-m", "755", src, installPath)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to install binary (sudo): %w", err)
	}
	return nil
}

// downloadFile downloads a file from the given URL to the destination path.
func downloadFile(url, dest string) error {
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return err
	}
	defer func() {
		_ = resp.Body.Close()
	}()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	f, err := os.Create(dest)
	if err != nil {
		return err
	}
	defer func() {
		_ = f.Close()
	}()

	_, err = io.Copy(f, resp.Body)
	return err
}

// copyFile copies src to dst, creating dst if it does not exist.
// Used instead of os.Rename when src and dst may be on different filesystems.
func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()

	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer func() { _ = out.Close() }()

	_, err = io.Copy(out, in)
	return err
}

// verifySha256 verifies the SHA256 checksum of a file against the checksums file.
// The checksums file should contain lines in the format: "sha256  filename"
func verifySha256(filePath, checksumsPath, expectedFileName string) error {
	f, err := os.Open(filePath)
	if err != nil {
		return fmt.Errorf("failed to open file: %w", err)
	}
	defer func() {
		_ = f.Close()
	}()

	h := sha256.New()
	if _, copyErr := io.Copy(h, f); copyErr != nil {
		return fmt.Errorf("failed to read file: %w", copyErr)
	}
	actualSha := hex.EncodeToString(h.Sum(nil))

	checksumsFile, err := os.Open(checksumsPath)
	if err != nil {
		return fmt.Errorf("failed to open checksums file: %w", err)
	}
	defer func() {
		_ = checksumsFile.Close()
	}()

	scanner := bufio.NewScanner(checksumsFile)
	for scanner.Scan() {
		line := scanner.Text()
		parts := strings.Fields(line)
		if len(parts) >= 2 && parts[1] == expectedFileName {
			expectedSha := parts[0]
			if actualSha != expectedSha {
				return fmt.Errorf("checksum mismatch: expected %s, got %s", expectedSha, actualSha)
			}
			return nil
		}
	}

	return fmt.Errorf("checksum for %s not found in checksums.txt", expectedFileName)
}

func checkoutChannel(repoDir, channel string) error {
	if out, err := exec.Command("git", "-C", repoDir, "status", "--porcelain").Output(); err != nil {
		return fmt.Errorf("failed to check working tree: %w", err)
	} else if strings.TrimSpace(string(out)) != "" {
		return fmt.Errorf("refusing to switch channels: uncommitted changes in %s", repoDir)
	}

	ref, isBranch, err := resolvePlaybookRef(repoDir, channel, true)
	if err != nil {
		return err
	}

	fmt.Printf("  Checking out %s...\n", ref)
	if err := checkoutPlaybookRef(repoDir, ref, isBranch); err != nil {
		return fmt.Errorf("failed to checkout %s: %w", ref, err)
	}
	return nil
}
