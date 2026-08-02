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

### pboProject will not run under CreateProcess

Go's `os/exec` uses CreateProcess, and pboProject then exits 1 immediately having
done nothing: no PBO, no packing log, no message, and its GUI left open with blank
fields. Launched the way Explorer would, via PowerShell's `Start-Process` which uses
ShellExecute, the *identical* command line packs correctly.

It is the creation call, not the command line. I ruled the alternatives out by
testing: the flag vector (both the proven set and an expanded one), `-X` quoted,
unquoted and omitted, the source as an addon folder and as a parent scan root,
P:-native and junctioned repos, an 8.3 executable path, and a hand-built raw command
line via `SysProcAttr.CmdLine`. All of them fail under CreateProcess and all of them
work under ShellExecute.

*Guarded.* `proc.Cmd.ShellExecute` exists for this one tool. Nothing else needs it
and nothing should adopt it without the same evidence. A cleaner refinement would be
to call `ShellExecuteEx` through `x/sys/windows` instead of shelling out to
PowerShell.

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

*Guarded.* Every invocation emits a complete, explicitly polarised flag vector
(`+O` **or** `-O`, never neither) and passes `-R` so the tool never writes its
options back. Golden tests assert the negative flags rather than merely the absence
of the positive ones.

### `-P` means "do not pause"

It does not mean "project dir". The path in a pboProject command line is the
positional source folder.

### `-B` is not about models

It concerns `config.cpp` and `mission.sqm` only. Model binarisation is
`policy.binarize`, which defaults **on** because `model.cfg` only gets applied when
binarising.

### `-X` takes no quotes here

The documentation's "QUOTES ARE MANDATORY" is about *shell* quoting, so that a batch
file passes the comma-separated list as one token. The tool executes commands
directly with no shell, so embedded quotes would become part of the exclude pattern.

### `$PBOPREFIX$` is optional

pboProject derives the same prefix AddonBuilder gets from `-prefix=`, so
AddonBuilder → Mikero is not a prefix break. The tool materialises the file anyway
(`prefix_file: always`) and removes it afterwards, because relying on derivation is
exactly the silent-failure class this project exists to eliminate.

### Obfuscation forces compression, and `init*.*` will not compress

Mikero's DLL refuses to compress `init*.*`. Anything reached from an init path
belongs in `policy.noscramble`.

Worth knowing too: the persisted `wildcard_exclude_from_compression` is the **Arma**
default (`init*.sqf,init*.sqs`) and does not cover DayZ's `init*.c`.

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
- **Everything about pboProject here was established against version 3.91** (registry
  `version = 391`, DePbo dll 9.46). `pboProject.4.31.10.04` exists and is completely
  untested. Several notes above may simply not apply to 4.x, most obviously the
  CreateProcess problem. `dayzmod doctor` reports the installed version and warns
  when it is not the one the packer was verified against.
