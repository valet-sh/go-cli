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
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/charmbracelet/huh"
	"github.com/valet-sh/cli/constants"
	"github.com/valet-sh/cli/internal/helper"
	"github.com/valet-sh/cli/internal/style"
)

type channelConfig struct {
	label string
	value string
}

var availableChannels = []channelConfig{
	{label: constants.VshStableVersion + " (Stable)", value: constants.VshStableVersion},
	{label: constants.VshNextVersion + " (development)", value: constants.VshNextVersion},
}

func ReleaseChannel(repoDir string) error {
	currentChannel := GetCurrentReleaseChannel()
	selectedChannel := currentChannel

	options := make([]huh.Option[string], 0, len(availableChannels))
	for _, ch := range availableChannels {
		label := ch.label
		if ch.value == currentChannel {
			label += " - current"
		}
		options = append(options, huh.NewOption(label, ch.value))
	}

	form := huh.NewForm(
		huh.NewGroup(
			huh.NewSelect[string]().
				Title("Select release channel to update from").
				Options(options...).
				Value(&selectedChannel),
		),
	)

	if err := form.Run(); err != nil {
		return fmt.Errorf("failed to select release channel: %w", err)
	}

	return setChannel(selectedChannel, repoDir)
}

func setChannel(channel, repoDir string) error {
	var valid bool
	for _, ch := range availableChannels {
		if ch.value == channel {
			valid = true
			break
		}
	}
	if !valid {
		return fmt.Errorf("invalid release channel: %q", channel)
	}

	if channel == GetCurrentReleaseChannel() {
		fmt.Printf("Already on %s channel.\n", channel)
		return nil
	}

	fmt.Printf("Switching to %s channel...\n", channel)

	if err := checkoutChannel(repoDir, channel); err != nil {
		if errors.Is(err, errNoRelease) {
			fmt.Println(style.Info(os.Stdout, "Channel switch cancelled."))
			return nil
		}
		return fmt.Errorf("failed to switch to %s channel: %w", channel, err)
	}

	if _, err := EnsureRuntime(repoDir); err != nil {
		fmt.Fprintf(os.Stderr, "  warning: runtime update failed: %v\n", err)
	}

	if err := helper.EnsureOwnedDir(constants.VshEtcPath, constants.VshRootPath); err != nil {
		return fmt.Errorf("failed to persist %s channel: %w", channel, err)
	}

	if err := os.WriteFile(constants.VshReleaseChannelFilePath, []byte(channel), 0o644); err != nil {
		return fmt.Errorf("failed to persist %s channel: %w", channel, err)
	}

	fmt.Printf("\nSuccessfully switched to %s channel\n\n", channel)
	return nil
}

func GetCurrentReleaseChannel() string {
	content, err := os.ReadFile(constants.VshReleaseChannelFilePath)
	if err != nil {
		return constants.VshStableVersion
	}
	return strings.TrimSpace(string(content))
}
