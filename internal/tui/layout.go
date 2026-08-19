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

import "strings"

// dividerLine renders a horizontal line of dashes matching the terminal width.
func dividerLine(width int) string {
	return strings.Repeat("─", width)
}

// wordWrap breaks text at word boundaries to fit within maxWidth columns.
func wordWrap(text string, maxWidth int) string {
	if maxWidth <= 0 {
		return text
	}

	inputLines := strings.Split(text, "\n")
	var lines []string

	for _, inputLine := range inputLines {
		words := strings.Fields(inputLine)
		if len(words) == 0 {
			lines = append(lines, "")
			continue
		}

		var current strings.Builder
		for _, word := range words {
			switch {
			case current.Len() == 0:
				current.WriteString(word)
			case current.Len()+1+len(word) <= maxWidth:
				current.WriteByte(' ')
				current.WriteString(word)
			default:
				lines = append(lines, current.String())
				current.Reset()
				current.WriteString(word)
			}
		}
		lines = append(lines, current.String())
	}

	return strings.Join(lines, "\n")
}
