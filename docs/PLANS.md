# Plans

Designs I have worked out but not built.

Once something is built it moves to [../README.md](../README.md) and
[../TODO.md](../TODO.md). Nothing should sit here that is already true.

---

## Incremental release

**Problem.** `release` clears its staging directory and repacks every addon every
time. That is fine for two addons. It is wrong for a public server pack where most
PBOs are unchanged between releases, and wrong again once third-party source is
vendored in and only one of a dozen addons actually moved.

**What already exists.** The dev channel skips unchanged addons using a content hash
in `.build_lockfile.json`, keyed `<set>/<addon>`. The release channel deliberately
sets `change_detection: none`, because "never ship a stale PBO because a hash
matched" is the right default when the alternative is a silent mistake.

**The design.** Keep that default, and make the cache safe enough to opt into.

A release cache entry is keyed by everything that can change the output, not just by
the source:

    key = sha256(
      content hash of the addon source
      + packer name and version        <- pboProject 3.91 vs 4.31 pack differently
      + obfuscate / binarize / side
      + engine, exclude list, noscramble
      + signing key identity           <- not the key material
      + prefix
    )

Anything left out of that key is a way to ship a PBO built under settings the
manifest no longer describes. The pboProject version belongs in it specifically
because this repo has already been bitten by version-specific behaviour.

    channels:
      release:
        cache:
          enabled: true
          dir: release/pbo-cache      # <key>/<Addon>.pbo (+ .bisign)

`release` then computes the key, copies the PBO and its signature into staging on a
hit, or packs and populates the cache on a miss. Staging stops being wiped wholesale
and instead has each addon's files replaced as they are produced, so a cache hit and
a fresh pack look identical downstream. The manifest still hashes whatever actually
shipped, so a wrong cache hit is at least visible after the fact.

**Obfuscation makes this more valuable, not less.** An obfuscated build has no
reproducible output at all, since Mikero uses a fresh cipher each run, so every
rebuild churns every PBO even when nothing changed. Caching is what would make an
obfuscated release diffable.

**Checked-in PBOs and CI artifacts are the same mechanism.** Both are just other
ways to populate `release/pbo-cache`: commit the directory (LFS) to get
reproducibility on a fresh clone, or restore it from a CI artifact keyed the same
way. Neither needs new concepts, which is the whole point of keying the cache
properly instead of by commit.

**CI is a separate question and probably not worth it yet.** A runner would need
DayZ Tools and Mikero installed, plus the work drive present. The cache design does
not depend on CI existing; it just makes CI cheap later if it ever happens.

**Risks.** The whole design rests on the key being complete. An incomplete key ships
stale artifacts silently, which is the exact failure `change_detection: none` exists
to prevent. Mitigations: keep `enabled: false` the default, record the full key
alongside each cache entry so a mismatch is auditable, and add a `release --no-cache`
that always packs.
