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

import "testing"

func TestHighestSemverTag(t *testing.T) {
	tests := []struct {
		name         string
		tags         []string
		majorVersion string
		want         string
	}{
		{
			name:         "picks highest patch",
			tags:         []string{"v3.4.0", "v3.4.1", "v3.3.9"},
			majorVersion: "3",
			want:         "v3.4.1",
		},
		{
			name:         "picks highest minor over lower patch",
			tags:         []string{"v3.4.9", "v3.5.0"},
			majorVersion: "3",
			want:         "v3.5.0",
		},
		{
			name:         "ignores other major versions",
			tags:         []string{"v2.9.19", "v4.0.0"},
			majorVersion: "3",
			want:         "",
		},
		{
			name:         "no v prefix",
			tags:         []string{"3.1.0", "3.2.0"},
			majorVersion: "3",
			want:         "3.2.0",
		},
		{
			name:         "ignores non-semver tags",
			tags:         []string{"latest", "3-beta", ""},
			majorVersion: "3",
			want:         "",
		},
		{
			name:         "no tags",
			tags:         []string{},
			majorVersion: "3",
			want:         "",
		},
		{
			name:         "prerelease and build metadata are not distinguished",
			tags:         []string{"v3.4.0-rc.1", "v3.4.0+build.5"},
			majorVersion: "3",
			want:         "v3.4.0-rc.1",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := highestSemverTag(tc.tags, tc.majorVersion)
			if got != tc.want {
				t.Errorf("highestSemverTag(%v, %q) = %q, want %q", tc.tags, tc.majorVersion, got, tc.want)
			}
		})
	}
}
