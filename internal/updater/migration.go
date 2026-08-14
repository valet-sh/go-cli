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

	"github.com/valet-sh/cli/constants"
	"github.com/valet-sh/cli/internal/ansible"
)

func CheckMigration(repoDir string) error {
	if PlaybookBranch != "3.x" {
		return nil
	}

	_, err := os.Stat(constants.VshServiceFile)
	serviceExists := err == nil
	_, err = os.Stat(constants.VshBundlesFile)
	bundleExists := err == nil

	if serviceExists || (serviceExists && bundleExists) {
		if os.Getenv("VALET_MIGRATE") != "" {
			ansible.SetVar("vsh_migrate", true)
			return nil
		}
	}

	return nil
}
