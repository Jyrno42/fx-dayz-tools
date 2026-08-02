// Copyright (C) 2026 Jürno Ader
//
// This program is free software: you can redistribute it and/or modify it under
// the terms of the GNU General Public License as published by the Free Software
// Foundation, either version 3 of the License, or (at your option) any later
// version.
//
// This program is distributed in the hope that it will be useful, but WITHOUT
// ANY WARRANTY; without even the implied warranty of MERCHANTABILITY or FITNESS
// FOR A PARTICULAR PURPOSE. See the GNU General Public License for more details.
//
// You should have received a copy of the GNU General Public License along with
// this program. If not, see <https://www.gnu.org/licenses/>.

// Command dayzmod builds, runs and releases DayZ mods.
//
// It replaces the per-repo Taskfile plus nushell plus PowerShell scripts that
// used to get copy-pasted into every mod. Per-mod settings live in dayz.yml at
// the repo root, and machine paths live in the user config.
package main

import (
	"os"

	"github.com/Jyrno42/fx-dayz-tools/internal/cli"
)

func main() {
	os.Exit(cli.Execute())
}
