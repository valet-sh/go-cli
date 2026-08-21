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
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/valet-sh/cli/constants"
)

func GetPlaybookVersion(repoDir, channel string) string {
	gitDir := filepath.Join(repoDir, ".git")
	if _, err := os.Stat(gitDir); err != nil {
		return "unknown"
	}

	if out, err := exec.Command("git", "-C", repoDir, "describe", "--tags", "--exact-match").Output(); err == nil {
		if tag := strings.TrimSpace(string(out)); tag != "" {
			return tag
		}
	}

	out, err := exec.Command("git", "-C", repoDir, "rev-parse", "--short", "HEAD").Output()
	if err != nil {
		return "unknown"
	}
	return channel + "@" + strings.TrimSpace(string(out))
}

func GetRuntimeVersion() string {
	data, err := os.ReadFile(constants.VshRuntimeVersionFile)
	if err != nil {
		return "unknown"
	}

	version := strings.TrimSpace(string(data))
	if version == "" {
		return "unknown"
	}
	return version
}
