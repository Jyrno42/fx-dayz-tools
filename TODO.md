# TODO

Open work only. What the tool already does is in [README.md](README.md), and what I
learned the hard way is in [docs/GOTCHAS.md](docs/GOTCHAS.md).

My migration plan sequenced the work smallest repo first, saving the largest for
last. In practice the largest went first, because it is the mod I am actively
developing. That turned out to be the right call anyway: it exercises every feature
the schema has.

## Next

- [ ] Add tool version checking and use the `requires_tool` field in `dayz.yml`. Currently its just parsed.

- [ ] **Widen the pboProject flag vector**, or decide not to. `-R`, `+W`, `-D`, `-G`,
      `-T` and `-$` are still unemitted and still inherited from the GUI registry.
      The finding that 3.91 rejected them sits upstream of the 4.01/4.03/4.19 parser
      fixes, so it may not hold on 4.31. Worth knowing that 4.31 rejects a command
      line by waiting on a keypress prompt, so a bad guess reads as a hang.

- [ ] **Check whether 4.31 writes `mod.cpp`.** 4.22 adds appID to it, 4.21 checks it
      for empty paa names, 4.09 and 4.10 changed and removed its checks. The
      Publisher owns that file, so a pboProject that writes one into the `-M=` folder
      changes what `release` has to clean up.

- [ ] **Land the server-only PBO** in my flagship mod. It is already stubbed in the
      manifest, and it is the only measure that prevents rather than delays.

## Later

- [ ] **Incremental release** (cached PBOs). Designed in
      [docs/PLANS.md](docs/PLANS.md), not started.

- [ ] **`init --pack`.** `init` scaffolds a dev channel only, so I write a pack
      repo's release channel and `vendor/` layout by hand. I only have one pack repo
      so far; if a second appears, generate it.

- [ ] **`md2bb`**, a Go port of the Node converter I keep in one of the older mod
      repos. The lookbehind in `/(?<!\w)_([^_]+?)_(?!\w)/` has no RE2 equivalent and
      needs hand-rolling, and there is a jest snapshot corpus to port over as golden
      files.

- [ ] **Migrate my remaining repos.** Three are still on the copy-pasted Taskfile,
      plus the older Justfile generation.

- [ ] **`doctor` should dump the pboProject registry state**, so option drift is
      visible. Less urgent now that every output-affecting option is stated
      explicitly, but the registry is still what an unstated option would fall back
      to. Note 4.31 moved it to `HKCU\Software\Mikero\pboProject\Settings`; the
      `version` value the tool already reads stays on the parent key.

- [ ] **Workshop publishing stays manual**, and probably should for a while.
      `Publisher.exe` is GUI-only (WPF plus SteamLayerWrap, with no CLI surface in
      its config), so the first publish has to be interactive, and automating
      updates afterwards would still mean driving that GUI. The realistic
      alternative is `steamcmd +workshop_build_item`, which wants Steam credentials
      and 2FA. Not worth building until publishing is routine for me. Until then the
      workflow is `dayzmod md2bb README.md` and a manual paste.

## Done

- [x] `dayz.yml` schema, defaults, validation. All four existing pipeline variants
      check in as parsing fixtures
- [x] Machine config + autodiscovery (Steam libraries, DayZ Tools, Mikero, work
      drive, signing keys)
- [x] Content hashing + build lockfile, keys qualified by addon set
- [x] Work drive resolution, junctions, PBO prefix derivation
- [x] `config init/show`, `doctor`, `pdrive`, `hash`, `lint`
- [x] Git LFS pointer-stub guard
- [x] AddonBuilder packer + `build` (hash → pack → deploy → record)
- [x] Dev loop: `server`, `client`, `dev`, `kill`, `wait`
- [x] `scriptcheck`. Boots headless, scans the script log, exits 4 on Enforce
      errors. Unlike my PowerShell original it requires a log written by THIS run,
      so a server that dies before logging fails instead of silently passing on the
      previous run's log. It tails the log rather than sleeping and stops the moment
      an error appears or the server exits. I measured 16s against a 60s settle on a
      real compile error.
- [x] `hooks run <stage>`, which invokes a repo's own generators, linters and tests
- [x] `-serverMod=` support for server-side-only mods (`launch.mods[].side`)
- [x] `init` / `init --sync`. Scaffold a new mod repo, or refresh the tool-owned
      files in an existing one
- [x] `release`: pre-release hooks, clean pack, signing, payload split by side,
      extra files, zips and a SHA-256 manifest
- [x] pboProject packer. Per-addon invocation with a polarised flag vector,
      `$PBOPREFIX$.txt` and `noscramble.lst` materialised and removed again, and
      `allow_obfuscation: false` on a set as a hard refusal rather than a default.
      (`-R` is *not* emitted; 3.91 rejects the command line when it is present.)
- [x] Packs, meaning one mod folder holding many PBOs. `include:` copies in prebuilt
      third-party PBOs (keeping their own signatures), an addon set can point at
      vendored source, `addon.prefix` states an exact upstream prefix, and
      `ship_keys` governs the `keys/` folder as a whole.
- [x] pboProject 4.31. The upgrade moved `+$` to mean the opposite of what 3.91's
      documentation said, deprecated the extensionless `$PBOPREFIX$`, and made a
      `source\` subfolder a hard stop. `doctor` now reports the DePbo dll version
      separately, since that is where obfuscation actually runs.
- [x] One @mod folder per addon set in `release`. An addon set with its own
      `mod_name` used to pack into the first set's folder, so a server-only PBO
      landed inside the client mod and the server payload came out under the wrong
      folder name. Signing, key distribution and the no-`.bikey` assertion ran
      against the first folder only.

**Verified against real repos**, not just unit tests:

- **My flagship mod** (two addon sets, pre-build hooks, obfuscated release). Argv
  parity with the old Taskfile, identical PBO sizes, correct prefixes inside the
  PBOs. I checked `doctor`, `lint`, `hooks run check`, `hash`, `build --force`,
  `scriptcheck` and `dev` before cutover. `scriptcheck` compiled 5 modules headless
  with no Enforce errors, which doubled as the first automated check of that mod's
  newest subsystem. The dev loop bound its port in 13.5s against the old fixed 25s
  sleep. `Taskfile.legacy.yml` and the five superseded nu scripts are deleted; the
  repo-specific scripts that had no business in a shared tool stay where they are.
- **A server-only mod**, released and signed end to end through AddonBuilder +
  DSSignFile.
- **A pack repo**, scaffolded with `init` and then reshaped by hand into a pack.
- **A full pboProject release**, three PBOs across two mod folders, signed and
  obfuscated, then booted. `ExtractPbo` refuses the two obfuscated PBOs and extracts
  the plain one, so the per-addon `+O`/`-O` polarity is right. The server compiled
  all 5 modules with no Enforce errors, which is the real test: the client PBO
  resolves a class out of the separately obfuscated server PBO, and each one is
  scrambled with its own cipher. A failure there would have looked exactly like a
  missing addon. The binarise-warnings blocker on the model PBO is gone in 4.31.
