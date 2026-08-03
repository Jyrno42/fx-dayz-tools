# Gotchas

Things I established by testing rather than assumed. Each one cost me time to find,
and several are silent failures, where the build succeeds and the mod does nothing.

Where the tool already guards against one, I have noted it. The rest are written
down so I do not have to rediscover them.

---

## Engine and loading

### The server's own `Addons\` folder is a legacy route, and not one to use

Two independent reasons:

- **Scripts never compile.** I tested this on 1.29 with a probe addon and no `-mod=`
  at all. The engine adds the package and merges its config, and the mod name even
  appears in the defines list of every script module, but no script module is
  created for it and the probe never ran. Data-only addons work; anything with
  scripts silently does nothing.
- **It must be unsigned.** A signed PBO loaded this way will not load at all. That
  is the legacy "hidden servermod" mechanism, and it rules out signing, which in
  turn rules out the one case worth signing a server mod for.

The mechanism to use is **`-serverMod=`**. It loads the mod normally while keeping
it off clients entirely, so they neither list nor download it. In `dayz.yml` that is
`launch.mods[].side: server`.

### `verifySignatures` does not apply to `-serverMod=`

Measured on a 1.29 dedicated server with `verifySignatures = 2` and no key
whitelisted: an *unsigned* mod loaded through `-serverMod=` runs fine. The setting
makes the server verify the files clients send it, and a server-side mod is never
one of those.

So signing a server-side-only mod is a choice, not a requirement. The reason to do
it anyway is that an operator may load it with `-mod=` instead, where the signature
does matter, and that is a plausible mistake for a mod aimed at operators. If you
sign, ship the key. Signing without distributing the `.bikey` is the one combination
that breaks that case rather than helping it.

### Client and server take different mod paths

The server takes bare names (`@CF;@VPPAdminTools`) while the client needs
`!Workshop\` prefixes. The wrong form silently loads nothing.

*Guarded.* `launch.mods` is one declared list and the tool emits both forms, so I
cannot get the asymmetry wrong by hand.

### The client must launch via `DayZ_BE.exe`

Launching `DayZ_x64.exe` directly while BattlEye is on makes BattlEye report "Game
Restart Required".

*Guarded.* `launch.battleye` selects the executable and patches the server config
together, so the two cannot disagree.

### The diag server is the diag *client* run with `-server`

`DayZDiag_x64.exe` exists only in the client install. It has to be launched with the
**server** directory as its working directory, so its config, mpmissions and
`@`-mod junctions resolve the way the retail server's do.

A diag client can only join a BattlEye-off server, which is why I made `battleye`
its own setting instead of a property of diag mode.

### `instanceId = 1;` is mandatory

Without it the server terminates gracefully after roughly ten seconds, *before*
mission init. So it looks like a mod problem when it is really a config one.

---

## Packing

### AddonBuilder never cleans its temp cache

It syncs sources into `%LOCALAPPDATA%\Temp\<addon lowercased>` and leaves them
there. Files *deleted* from the source tree linger in that cache and keep shipping.

Real incident, 2026-07-26: a never-committed script file shipped inside a logic PBO
from the temp directory alone, and surfaced in game as `Undefined function`.

*Guarded.* `addonbuilder.Preflight` wipes the cache, the staged PBO and the output
PBO before every run.

### pboProject needs a console, because signing shells out

On 3.91 it would not run under CreateProcess at all: it exited 1 immediately having
done nothing, whatever the arguments, and only ShellExecute worked. 4.31 fixed that.
Plain CreateProcess packs correctly, in 254ms against 1.26s through ShellExecute.

What replaced it is narrower. Signing makes pboProject shell out to copy the
`.bikey` into the mod folder's `keys\` directory. Run it in a terminal and cmd's own
`1 File(s) copied` scrolls past. With no console for that child the copy fails,
`keys\` is left empty, and the run exits 1 *before packing anything* and without a
packing log to say why. `-K` works everywhere, `+K` only works with a console.

Ruled out by testing, all against the same addon: captured pipes, NUL handles,
inherited handles, `CREATE_NEW_CONSOLE`, `CREATE_NO_WINDOW` and `DETACHED_PROCESS`
applied to pboProject directly. Every one of them fails. Interposing `cmd.exe` fixes
it, with or without a window.

*Guarded.* `proc.Cmd.NeedsConsole` runs it as a child of `cmd.exe` with
`CREATE_NO_WINDOW`, so it gets a real console and no window appears. The command
line is built by hand via `SysProcAttr.CmdLine` as `cmd /s /c "..."`, because Go's
argv escaping produces something cmd mis-splits.

The alternative, packing unsigned and signing afterwards with DSSignFile, was
rejected. Obfuscation deliberately leaves a PBO malformed to third-party readers and
DSSignFile is one of those, so inline signing keeps an obfuscated release on the
path known to produce working PBOs.

### `-P` does not suppress the pause on a bad command line

`-P` means "do not pause", and it works for a normal run. It does **not** cover the
command-line error path: reject an argument and pboProject prints `Command line
params are probably bad` followed by `press the ANY key`, and waits. It is a
GUI-subsystem binary, so nothing can ever press one.

The result is an indefinite hang rather than a failure, which is worse than 3.91's
instant exit 1. It also makes every other mistake here look like a freeze.

*Guarded.* `packer.DefaultPackTimeout` bounds every pack at 30 minutes, and the
timeout message names this as the likely cause.

### pboProject will not create its own `-M=` folder

It reports `mod folder '...' does not exist`, concludes the command line is bad, and
then hits the keypress prompt above. So a missing output directory presents as a
hang, not as an error.

*Guarded.* `Preflight` creates `<mod>\Addons`, which creates the mod folder above
it.

### pboProject has no console

It is a GUI-subsystem binary, so it never writes to stdout or stderr. Its only
output is `P:\temp\<Addon>.packing.log`, one per addon, which is where the packer's
error message points.

Its Setup dialog also has to be populated: engine, temp folder, signing key. A
failed run blanks them, and `-R` does not help, because it restores *after*
processing, which never happens on a failure.

### pboProject options are sticky

They persist in the GUI registry. From its own documentation: *"If you +Obfuscate a
pbo, all subsequent invocations of pboProject will continue to obfuscate until
turned off."* The registry on this machine holds `m_obfuscate = 1`.

*Guarded.* Every invocation emits an explicitly polarised flag vector (`+O` **or**
`-O`, never neither) for the options it states. Golden tests assert the negative
flags rather than merely the absence of the positive ones.

`-R` is **not** among them, despite being the obvious companion to that rule. 3.91
rejects the whole command line when it is present, and it restores settings *after*
processing, so it cannot help the case that matters, a failed run blanking the Setup
dialog. `restore_gui_settings` in `dayz.yml` is therefore intent, not effect. Options
outside the emitted set still come from the registry; obfuscation is not one of them,
and that is the polarity that actually matters.

### `-P` means "do not pause"

It does not mean "project dir". The path in a pboProject command line is the
positional source folder.

### `-B` is not about models

It concerns `config.cpp` and `mission.sqm` only. Model binarisation is
`policy.binarize`, which defaults **on** because `model.cfg` only gets applied when
binarising.

### "Warnings are errors" is a tri-state dialog, not the `+W` flag

4.05 changed `+W` to mean *ALL* warnings are errors. 4.22 then *"removed the
universal cripple checkbox"* and 4.23 *"removed last vestiges of warnings are forced
to errors"*. The flag's help in 4.31 reads: `+W: ALL warnings are also errors. When
set, disable what you want to ignore`.

The place to ignore one is the Setup dialog's **Warnings & Errors** page, where each
entry cycles through three states. The dialog says `Click on any checkbox multiple
times to change from Error to Warning to Disabled`. That page is the lever for
binarise warnings that originate in vanilla configs rather than in the mod. `-N` and
`m_warnings` are not, and both are the obvious things to try first.

`warnings` defaults to **false**. It defaulted to true for a while, but the flag was
never emitted, so the registry's `m_warnings=0` was doing the real work and the
disagreement went unnoticed.

### The DePbo dll versions separately, and does the actual work

`pboProject.exe` is a front end. Obfuscation, compression and the PBO writing itself
live in `DePbo64.dll`, which ships on its own cadence. 4.31 asks only for a *minimum*
("minimum dll is 10.04") rather than pinning one, so the pair can drift far enough
apart to fail inside the dll while pboProject itself looks current.

*Guarded.* `doctor` reports `HKCU\Software\Mikero\DePbo\version` alongside
pboProject's own, and warns below the stated minimum.

### `-X` takes no quotes here

The documentation's "QUOTES ARE MANDATORY" is about *shell* quoting, so that a batch
file passes the comma-separated list as one token. The tool executes commands
directly with no shell, so embedded quotes would become part of the exclude pattern.

### `$PBOPREFIX$` is optional, and the extensionless spelling is deprecated

pboProject derives the same prefix AddonBuilder gets from `-prefix=`, so
AddonBuilder → Mikero is not a prefix break. The tool materialises the file anyway
(`prefix_file: always`) and removes it afterwards, because relying on derivation is
exactly the silent-failure class this project exists to eliminate.

Write it as **`$PBOPREFIX$.txt`**. pboProject 4.31 carries the string
`$PBOPREFIX$ (no ext) deprecated` in every language it ships, and once warnings are
errors a deprecation notice is a failed pack.

*Guarded.* `Preflight` writes the `.txt` spelling and treats either spelling as
committed, so a repo carrying the old one does not end up with two prefix files for
pboProject to choose between.

### `+$` does not mean "encode prefix", it means "no prefix"

4.31's own help: `+/-$ do/don't allow no prefix for pbo` and `+$: enable no prefix
in pbo`. The 3.91 documentation described the same letter as *"Do/Don't encode prefix
in pbo, when possible. Default is enabled"*, which is the exact opposite. A config
written against the old wording, carried forward literally, ships PBOs the engine
cannot address.

4.31 also refuses two combinations outright, in a GUI nobody is watching:
`you cannot use no prefix if obfuscating` and `you cannot use no prefix AND a rename`
(a rename being `+L=`).

*Guarded.* The setting is named `no_prefix` for what it does rather than for the
flag, the old `encode_prefix` key is rejected with an explanation rather than an
"unknown field", and both refused combinations error in `Argv` before pboProject
gets the chance.

### A `source\` subfolder is a hard stop

4.06: *"now stops if a source \ folder is present"*, with 4.25 adding the warning
that explains it: pboProject will never put its contents in a PBO, so a folder named
that is assumed to be a mistake rather than content.

*Guarded.* `Preflight` refuses, naming the directory, rather than letting it become
a failed pack whose only explanation is a line in an unopened log.

### Obfuscation forces compression, and `init*.*` will not compress

Mikero's DLL refuses to compress `init*.*`. Anything reached from an init path
belongs in `policy.noscramble`.

Worth knowing too: the persisted `wildcard_exclude_from_compression` is the **Arma**
default (`init*.sqf,init*.sqs`) and does not cover DayZ's `init*.c`.

### Obfuscating a model PBO is not automatically fatal

The received wisdom, carried over from another of my mods, is that obfuscation must
never touch a model PBO. Tested directly on 1.29, packing my flagship mod's models
plus configs both ways and booting each:

- **Server side, measurably identical.** Same class count, same entities spawned,
  same warning counts, same mod-side log output. The obfuscated `config.bin` parses
  and the p3d references resolve.
- **Client side, mostly renders correctly.** Meshes, textures and geometry are
  normal. The exception found so far is one group of `rvmat`s that the rescramble
  breaks, and the fix for that is a `policy.noscramble` entry rather than turning
  obfuscation off for the whole PBO.

The dll really does scramble the models. The log lists `obfuscating any paas`,
`any rvmats`, `any p3ds` and `all p3d contents`. It mostly does not break them.

So treat it as per-mod, and test rather than assume in either direction. Two traps
if you do test:

- **Size tells you nothing.** Mikero's docs say obfuscated PBOs run ~15% larger.
  That is about text-heavy content. My model PBO came out 0.27% *smaller*, because
  forced compression roughly cancels the overhead on already-compressed assets. Use
  `ExtractPbo` refusing the file as the signal instead.
- **A server boot cannot see it.** A scrambled mesh or material fails at render, not
  at load, and a headless server never renders. Only a client settles it.
- **Checking one object is not enough.** The broken material got past my first
  client check because the object I happened to be looking at did not use it.
  Anything with a material path of its own needs checking on its own (holograms
  for example).

### Obfuscation writes its own key into your source tree

pboProject drops a `.obf` file beside the source it packed. It pairs every mangled
name with the real one, so it is the deobfuscation map for the release you just
built. Committing it hands back everything `+O` was meant to protect.

It lands in the source tree rather than the output folder, which is what makes it
easy to miss: `git status` shows it as an ordinary new file in a directory full of
ordinary new files, and it survives the pack rather than being cleaned up.

I committed one before noticing. `init` now ignores `*.obf`, but that only helps
repos scaffolded after the fact, so check an existing repo by hand:

```
git log --all --oneline --diff-filter=A -- '*.obf'
```

A hit means a history rewrite, not a `git rm`. The file is still readable in every
commit that carried it, and on any remote it reached.

Worth keeping rather than deleting outright: the map is also how you turn an
obfuscated crash log back into real file names. Keep it out of the repo, not off
the disk.

### Obfuscated output is never reproducible

Mikero uses a fresh cipher each run, so the same folder obfuscated twice gives
different bytes. Byte-comparison is therefore useless for verifying an obfuscated
build, and parity checking has to be `ExtractPbo` plus a content diff.

---

## Environment

### The work drive is a per-logon-session device map

`P:` is a `subst` of `C:\Users\me\Documents\DayZ Projects`. Two consequences:

- It does not survive a reboot. `pdrive.auto_mount` re-creates it instead of failing
  confusingly on the first build after a restart.
- It is invisible to processes in **another** logon session, which once had `doctor`
  reporting five files as missing that were plainly there.

*Guarded.* `doctor` reports an unresolvable drive as one root-cause line and
resolves the remaining paths through the backing directory, so a genuinely absent
file still reads as missing.

### Junctions are not symlinks

A junction reports `os.ModeIrregular`, not `os.ModeSymlink`. Detect one by
`os.Readlink` succeeding. The tool creates junctions rather than symlinks because
they need no elevation.

### Git LFS pointer stubs pack silently

`.gitattributes` routes `*.p3d *.paa *.edds *.fbx *.rtm` through LFS. A fresh clone
without `git lfs pull` packs the text pointer stubs instead of the assets.

*Guarded.* `repo.lfs_guard` refuses to pack a stub.

---

## Publishing

### The Publisher owns `meta.cpp` and `mod.cpp`

`meta.cpp` carries the Workshop `publishedid`, which the Publisher fills in from the
item it created, so the file is generated rather than authored. The Publisher also
tracks its own published items, so it can update an existing entry without a
`meta.cpp` in the folder it publishes from.

Staging a copy from the repo would only risk handing it a stale one, so `release`
neither writes nor preserves either file.

---

## Unverified

- **`-packonly`** is what I use as AddonBuilder's flag for `binarize: false`. Nothing
  sets that today, so it has never actually run.

### pboProject 4.31

**Installed since 2026-08-03: 4.31.10.04 with DePbo dll 10.22** (registry
`version = 431`, `DePbo\version = 1022`), replacing 3.91 with dll 9.46. `doctor`
reports both and warns that the packer is verified against 391.

Established from the 4.31 binary and its change log, without a build:

- The exe is now **x64** (3.91 was x86) and **still GUI subsystem**, so the
  no-console gotcha above still holds and the packing log is still the only output.
- Its settings moved to `HKCU\Software\Mikero\pboProject\Settings`. The `version`
  value the tool reads stays on the parent key.
- The shipped `.docx` **lags the binary**. It documents `+Stop` (removed in 4.13) and
  `+/-Q`, neither of which is in the binary's own option table; it gives `+/-$` the
  3.91 meaning; and it misses `+/-@` entirely. Dump the strings out of the exe rather
  than trusting the docs.

Settled by a full release of my flagship mod through the tool:

- **CreateProcess works**, and signing needs a console. Both above.
- **The emitted vector packs**, including the new `+/-H` and `+/-@`.
- **`-X=` still wants no quotes.**
- **Obfuscation works, per addon.** `ExtractPbo` refuses the two `obfuscate: true`
  PBOs with `DePbo:Pbo unknown header type` and extracts the `obfuscate: false` one
  normally, so the `+O`/`-O` polarity is right addon by addon.
- **The binarise-warnings blocker is gone.** The model PBO packs. 4.22 removed the
  "universal cripple checkbox" and 4.23 the "last vestiges of warnings are forced to
  errors", and with `warnings` defaulting off the vanilla-config terrain-grid
  warnings no longer fail a pack.

Still untested:

- **Does it reject the wider flag vector** (`-R +W -D -G -T -$`)? That finding is
  3.91-specific and sits upstream of the 4.01/4.03/4.19 parser fixes. Untested on
  4.31, and 4.31 rejects a command line by hanging, so widening is an experiment.
- **Does it now touch `mod.cpp`?** 4.22 adds appID to it, 4.21 checks it for empty
  paa names, 4.09/4.10 changed and removed its checks. The Publisher owns that file
  (see above), so a pboProject that writes one into the `-M=` folder changes what
  `release` has to clean up.
- **Whether obfuscation survives a cross-PBO symbol reference.** Packing proves
  nothing here: if a class resolved from another PBO gets scrambled inconsistently
  between the two, the server fails to compile and it looks exactly like a missing
  addon. Only booting a server off the release payload settles it.
