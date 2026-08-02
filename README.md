# fx-dayz-tools

[![CI](https://github.com/Jyrno42/fx-dayz-tools/actions/workflows/ci.yml/badge.svg)](https://github.com/Jyrno42/fx-dayz-tools/actions/workflows/ci.yml)

My modding discord: https://discord.gg/e88GPU4hHP

Buy me a coffee: https://ko-fi.com/jyrno42

For problems with the tool, please open an issue or join the discord. I do not provide support for DayZ itself, or for general modding questions. Those belong in one of the many modding discords, or on the Bohemia forums.

----

`dayzmod` is one binary that builds, runs and releases my DayZ mods.

It replaces the Taskfile + nushell + PowerShell scripts I had been carrying around,
copy-pasted into every mod repo and drifted in each one. Per-mod settings now live
in a `dayz.yml` at the repo root. Machine paths live in a user-level config, so I
never again hardcode where DayZ is installed into a repo.

> Note: I have only been using this myself so far. Things may be broken for others so please open an issue if you run into problems.

## Install

```
go install github.com/Jyrno42/fx-dayz-tools/cmd/dayzmod@latest
dayzmod config init     # probe this machine for the toolchain
dayzmod doctor          # verify it
```

No Go toolchain? Grab `dayzmod_windows_amd64.exe` from
[Releases](https://github.com/Jyrno42/fx-dayz-tools/releases) instead. Windows
will warn that the publisher is unknown, because I do not sign the binary with an
Authenticode certificate. Every release ships `SHA256SUMS` and a build provenance
attestation you can check against:

```
gh attestation verify dayzmod_windows_amd64.exe -R Jyrno42/fx-dayz-tools
```

`config init` reads the registry and Steam's library index to locate DayZ, DayZ
Server, DayZ Tools and Mikero DePboTools. It also detects the work drive and
registers every signing key in the keys directory. Anything it cannot find gets
reported along with the exact config key you need to fill in by hand.

## Status

I use it daily. Building, deploying, the dev loop, `scriptcheck` and AddonBuilder
releases are all verified against real repos. My largest mod is fully migrated, and
a server-only mod ships signed releases through it. The pboProject path is verified
at the argv level and packs correctly, but I have not yet pushed a complete
obfuscated release out through it.

| Area | State |
|---|---|
| `dayz.yml` schema, defaults, validation | done, expresses all four of my existing pipeline variants |
| Machine config + autodiscovery | done |
| Content hashing + build lockfile | done |
| Work drive, junctions, PBO prefix derivation | done |
| `config`, `doctor`, `pdrive`, `hash`, `lint` | done |
| `build` (pack and deploy) | done |
| `server`, `client`, `dev`, `wait`, `kill` | done |
| `scriptcheck` (Enforce compile gate) | done |
| `release` (pack, sign, payload split, zip, manifest) | done via AddonBuilder |
| pboProject packer, per-PBO obfuscation split | done, verified against pboProject 3.91 only |
| `hooks run` for repo-specific generators and tests | done |
| `init` (scaffold a new mod repo) | done |
| packs: `include:` prebuilt PBOs, vendored submodule source | done |
| `md2bb` | not yet |
| workshop publish | deliberately manual, see [TODO.md](TODO.md) |

## Docs

- [TODO.md](TODO.md) — open work, and what is already done
- [docs/GOTCHAS.md](docs/GOTCHAS.md) — things I established by testing. Several of
  them are silent failures, where the build succeeds and the mod does nothing
- [docs/PLANS.md](docs/PLANS.md) — designed but not built

## Why it is shaped this way

A handful of decisions hold up more than they look like they do, and I could easily
undo one by accident:

- **PBO prefixes are derived, never written by hand.** A prefix encodes the repo's
  own folder name, so the tool computes it from one declared path. Get it wrong and
  the mod loads nothing, silently.
- **pboProject options persist in the GUI registry.** Straight from Mikero's docs:
  *"If you +Obfuscate a pbo, all subsequent invocations of pboProject will continue
  to obfuscate until turned off."* So every invocation emits a complete, explicitly
  polarised flag vector (`+O` or `-O`, never neither) and passes `-R` so the tool
  never writes its options back.
- **Binarising is on by default.** That is AddonBuilder's own default, and
  `model.cfg` only gets applied when binarising. pboProject's `-B` is a different
  thing entirely: it concerns `config.cpp` and `mission.sqm` rather than models, and
  it is configured separately.
- **BattlEye is its own setting** rather than a property of diag mode. It selects
  both the client executable and the server config patch.
- **The launch mod list is declared once.** The server takes bare names while the
  client needs `!Workshop\` prefixes, so the tool emits both forms and I cannot get
  the asymmetry wrong. A server-side-only mod is `side: server`, which routes it to
  `-serverMod=`. Dropping its PBO into the server's own `Addons` folder merges the
  config but never compiles its scripts, so the mod silently does nothing.
- **`.bikey` distribution follows `mod.visibility`.** I state it once per mod
  instead of as a flag in a command string, where one typo publishes a private key.
- **Included PBOs are never re-signed.** A pack mixes signed and unsigned PBOs from
  several authors, and re-signing one would throw away its original chain of trust.
  The operator whitelists whichever key they already trust, so an unsigned included
  PBO gets reported rather than treated as an error.
- **Packs carry PBOs, not configs.** `include:` handles `.pbo` files plus any
  `.bisign` beside them and nothing else, because mission files, `types.xml` and
  JSON settings are delivered at runtime by a separate config-sync mod. If a pack
  looks like it needs a config file baked in, that file belongs in the sync instead.

Most of these exist because something failed silently on me first. The evidence is
in [docs/GOTCHAS.md](docs/GOTCHAS.md).

## Development

```
go test ./...          # full unit suite, no DayZ tools required
GOOS=linux go build ./...   # everything except the Windows syscall layer is portable
```

Only `internal/paths`, `internal/proc` and `internal/machine/discover_*` are
Windows-specific. The rest builds and tests anywhere, so CI does not need a DayZ
install.

A handful of tests do skip on Linux, and the skip message says why each one does.
They are the ones needing a path that carries a drive letter *and* exists on
disk, which a POSIX host cannot give them. Nothing about the code under test is
Windows-only; the fixture is.

`task ci` runs the lot before I push: build, cross-build, vet, the formatting and
tidiness checks, and the suite. The GitHub workflow calls those same tasks instead
of repeating the commands, so whatever passes on my machine is what passes in CI.

It runs on both Ubuntu and Windows deliberately. Nine files come in `_windows.go`
and `_other.go` pairs, and a Linux-only run would never compile the Windows half
at all.

## Licence

GPL-3.0-or-later. See [LICENSE](LICENSE).

If you distribute a modified `dayzmod`, ship your source under the same terms.
That covers the tool only — **the mods you build with it are yours**, under
whatever terms you like. Running a program on your files does not put those files
under the program's licence.
