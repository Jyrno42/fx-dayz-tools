package pipeline

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/Jyrno42/fx-dayz-tools/internal/modcfg"
	"github.com/Jyrno42/fx-dayz-tools/internal/packer"
	"github.com/Jyrno42/fx-dayz-tools/internal/paths"
	"github.com/Jyrno42/fx-dayz-tools/internal/sign"
)

// ReleaseOptions controls a release run.
type ReleaseOptions struct {
	Channel string
	// SkipHooks omits the pre-release hooks.
	SkipHooks bool
	// SkipSign packs without signing, for a quick check.
	SkipSign bool
	// NoZip stages the payloads without archiving them.
	NoZip bool
	// Payloads narrows the run to the named payloads.
	Payloads []string
	// SkipObfuscation forces every addon plain. It is a panic button, not a
	// setting to leave on.
	SkipObfuscation bool
	// Version is stamped into archive names and the manifest.
	Version string
}

// Payload is one assembled, shippable bundle.
type Payload struct {
	Name string
	// Dir is what gets zipped and hashed. It is the @mod folder itself when the
	// payload holds exactly one, and a directory containing one @mod folder per
	// addon set when it holds several.
	Dir string
	// ModNames are the @mod folders in this payload, in declaration order. More
	// than one means the addon sets that fed it build into different folders,
	// which an operator has to keep separate rather than merge.
	ModNames []string
	// Zip is the archive, when one was produced.
	Zip string
	// Addons are the PBO names it contains.
	Addons []string

	// zipRoot is the folder name to nest Dir under inside the archive. Empty
	// when Dir already contains the @mod folders and its entries go in as-is.
	zipRoot string
}

// modStage is one @mod folder being built. A release has several whenever its
// addon sets declare different mod_names, which is how a server-only addon gets
// its own folder for -serverMod= instead of riding inside the client's.
type modStage struct {
	ModName string
	Dir     string
}

// releaseStages lists the @mod folders this channel builds, in declared order
// and deduplicated, since several sets may share one.
func (b *Builder) releaseStages(ch *modcfg.Channel) []modStage {
	if ch.Out != "" {
		// An explicit out path names one folder, so every set lands in it.
		return []modStage{{ModName: filepath.Base(ch.Out), Dir: ch.Out}}
	}

	var out []modStage
	seen := map[string]bool{}
	for _, set := range b.Mod.SetsFor(ch) {
		name := set.ModName
		if name == "" {
			name = b.Mod.Mod.Name
		}
		if seen[name] {
			continue
		}
		seen[name] = true
		out = append(out, modStage{ModName: name, Dir: filepath.Join(b.releaseDir(ch), name)})
	}
	if len(out) == 0 {
		name := b.Mod.Mod.Name
		seen[name] = true
		out = append(out, modStage{ModName: name, Dir: filepath.Join(b.releaseDir(ch), name)})
	}

	// An include can name its own folder too, and those stages have to exist by
	// the time Release clears and creates them. Appended after the set stages so
	// stages[0] stays the primary, which everything else resolves against.
	for _, inc := range b.Mod.Include {
		if inc.ModName == "" || seen[inc.ModName] {
			continue
		}
		seen[inc.ModName] = true
		out = append(out, modStage{ModName: inc.ModName, Dir: filepath.Join(b.releaseDir(ch), inc.ModName)})
	}
	return out
}

// stageDirFor is where an addon set's PBOs are staged.
func (b *Builder) stageDirFor(ch *modcfg.Channel, set *modcfg.AddonSet) string {
	if ch.Out != "" {
		return ch.Out
	}
	name := set.ModName
	if name == "" {
		name = b.Mod.Mod.Name
	}
	return filepath.Join(b.releaseDir(ch), name)
}

// ReleaseResult describes what a release produced.
type ReleaseResult struct {
	StageDir string
	Payloads []Payload
	Manifest string
	Signed   bool
	// Included counts prebuilt PBOs copied in instead of packed here.
	Included int
}

// Release packs, signs and assembles the shippable payloads for a channel.
func (b *Builder) Release(ctx context.Context, opts ReleaseOptions) (*ReleaseResult, error) {
	ch, err := b.Mod.Channel(opts.Channel)
	if err != nil {
		return nil, err
	}
	p, err := b.packerFor(ch)
	if err != nil {
		return nil, err
	}

	if !opts.SkipHooks {
		if err := b.runHooks(ctx, b.Mod.Hooks.PreRelease); err != nil {
			return nil, err
		}
	}

	// One staging folder per @mod folder the channel builds. A set that declares
	// its own mod_name gets its own, because merging a server-only addon into the
	// client's folder is precisely what -serverMod= exists to avoid.
	stages := b.releaseStages(ch)
	primary := stages[0]

	// A release never reuses whatever happened to be lying around. A stale PBO
	// from a previous run is exactly the thing that ships by accident.
	if !b.Runner.DryRun() {
		for _, st := range stages {
			if err := os.RemoveAll(st.Dir); err != nil {
				return nil, fmt.Errorf("release: clearing %s: %w", st.Dir, err)
			}
			if err := os.MkdirAll(filepath.Join(st.Dir, "Addons"), 0o755); err != nil {
				return nil, fmt.Errorf("release: %w", err)
			}
		}
	}

	for _, st := range stages {
		b.Report.Step("%-28s %s", "staging", st.Dir)
	}

	packed, err := b.packRelease(ctx, ch, p, opts)
	if err != nil {
		return nil, err
	}

	res := &ReleaseResult{StageDir: primary.Dir}

	// Signing runs before the includes get staged, so the signer only ever sees
	// PBOs this repo packed. A prebuilt PBO keeps whatever signature it came
	// with.
	if err := b.signRelease(ctx, ch, stages, packed, opts, res); err != nil {
		return nil, err
	}

	// Includes default to the primary folder but can name their own, so they
	// resolve their own stage. Channel extra files are a property of the mod as a
	// whole and stay on the primary one.
	included, err := b.stageIncludes(b.Mod.Include, stages, primary, packed, b.Mod.ShipKeysEnabled(ch))
	if err != nil {
		return nil, err
	}
	packed = append(packed, included...)
	res.Included = len(included)
	if err := b.stageExtras(ch.ExtraFiles, primary.Dir); err != nil {
		return nil, err
	}

	res.Payloads, err = b.assemblePayloads(ch, stages, packed, opts)
	if err != nil {
		return nil, err
	}

	if ch.Manifest.Enabled {
		res.Manifest, err = b.writeManifest(ch, res, opts)
		if err != nil {
			return nil, err
		}
	}
	return res, nil
}

// packedAddon records where an addon ended up and how it is meant to ship.
type packedAddon struct {
	Name string
	Side modcfg.Side
	PBO  string
	// ModName is the @mod folder this addon belongs to, taken from its set.
	ModName string
	// Included marks a prebuilt PBO copied in instead of packed here. Those keep
	// their own signatures and never get re-signed.
	Included bool
}

func (b *Builder) packRelease(
	ctx context.Context,
	ch *modcfg.Channel,
	p packer.Packer,
	opts ReleaseOptions,
) ([]packedAddon, error) {
	var packed []packedAddon

	for _, set := range b.Mod.SetsFor(ch) {
		stageDir := b.stageDirFor(ch, set)
		for _, addonName := range set.AddonNames() {
			job, err := b.job(ch, set, addonName, p.Caps())
			if err != nil {
				return nil, err
			}
			// Each set packs into its own @mod folder. Which bundles a PBO then
			// reaches is the payload split's job; which FOLDER it lives in is
			// this one, and the two are not the same question.
			job.OutDir = filepath.Join(stageDir, "Addons")
			if opts.SkipObfuscation {
				job.Obfuscate = false
			}
			if ch.Sign.Enabled && !opts.SkipSign && p.Caps().CanSignInline {
				key, err := b.signingKey(ch)
				if err != nil {
					return nil, err
				}
				job.SignKey = key.Private
			}

			b.Report.Step("%-28s packing", set.Name+"/"+addonName)
			if job.Obfuscate {
				b.Report.Detail("obfuscated")
			}

			cleanup, err := p.Preflight(ctx, job)
			if err != nil {
				return nil, err
			}
			_, packErr := p.Pack(ctx, b.Runner, job)
			if cerr := cleanup(); cerr != nil {
				b.Report.Detail("cleanup after %s: %v", addonName, cerr)
			}
			if packErr != nil {
				return nil, packErr
			}

			packed = append(packed, packedAddon{
				Name:    addonName,
				Side:    set.Addons[addonName].Policy.Side,
				PBO:     p.PboPath(job),
				ModName: filepath.Base(stageDir),
			})
		}
	}
	return packed, nil
}

func (b *Builder) signRelease(
	ctx context.Context,
	ch *modcfg.Channel,
	stages []modStage,
	packed []packedAddon,
	opts ReleaseOptions,
	res *ReleaseResult,
) error {
	distribute := b.Mod.DistributeBikey(ch)
	shipKeys := b.Mod.ShipKeysEnabled(ch)

	if !ch.Sign.Enabled || opts.SkipSign {
		if ch.Sign.Enabled {
			b.Report.Detail("signing skipped: this build is not shippable")
		}
		// pboProject writes a keys/ folder whenever it signs. Without signing
		// there should be none, but clear it anyway for a private mod.
		if !distribute && !b.Runner.DryRun() {
			for _, st := range stages {
				if err := sign.RemoveKeysDir(st.Dir); err != nil {
					return err
				}
			}
		}
		return nil
	}

	key, err := b.signingKey(ch)
	if err != nil {
		return err
	}

	// pboProject signs during packing. AddonBuilder needs a separate pass, once
	// per staged folder.
	//
	// Only folders this repo packed into. A folder that exists purely to receive
	// prebuilt includes is still empty here, because includes are staged after
	// signing so the signer never sees a PBO it did not produce. Handing the
	// signer an empty directory would fail the release over nothing.
	p, _ := b.packerFor(ch)
	if !p.Caps().CanSignInline {
		hasPacked := map[string]bool{}
		for _, a := range packed {
			hasPacked[a.ModName] = true
		}
		signer := &sign.Signer{Exe: b.Host.DSSignFile()}
		b.Report.Step("%-28s signing with %q", "release", ch.Sign.Key)
		for _, st := range stages {
			if !hasPacked[st.ModName] {
				b.Report.Detail("nothing packed into %s, so nothing to sign there", st.ModName)
				continue
			}
			if _, err := signer.SignDir(ctx, b.Runner, filepath.Join(st.Dir, "Addons"), sign.Options{
				PrivateKey: key.Private,
				V2:         ch.Sign.V2,
			}); err != nil {
				return err
			}
		}
	}
	res.Signed = true

	if b.Runner.DryRun() {
		return nil
	}

	if distribute {
		// Every folder gets the key. A server-only folder loaded through
		// -serverMod= is a mod in its own right, and an operator pointing -mod=
		// at it instead needs the signature to verify.
		for _, st := range stages {
			dst, err := sign.DistributeKey(key.Public, st.Dir)
			if err != nil {
				return err
			}
			b.Report.Detail("public key shipped: %s", filepath.Base(dst))
		}
		return nil
	}

	// No key ships. Remove the folder pboProject may have created, then prove
	// none survived. Publishing a private mod's .bikey lets anyone whitelist a
	// repack, and a pack that says it ships no keys had better ship no keys.
	for _, st := range stages {
		if err := sign.RemoveKeysDir(st.Dir); err != nil {
			return err
		}
		if err := sign.AssertNoPublicKeys(st.Dir); err != nil {
			return err
		}
	}
	if !shipKeys {
		b.Report.Detail("no keys shipped (ship_keys is off)")
	} else {
		b.Report.Detail("public key withheld (mod is %s)", b.Mod.Mod.Visibility)
	}
	return nil
}

func (b *Builder) signingKey(ch *modcfg.Channel) (keyring, error) {
	k, err := b.Host.Key(ch.Sign.Key)
	if err != nil {
		return keyring{}, fmt.Errorf("release: %w", err)
	}
	return keyring{Private: k.Private, Public: k.Public}, nil
}

type keyring struct{ Private, Public string }

// stageExtras copies channel-level extra files, e.g. mod.cpp, into the payload.
func (b *Builder) stageExtras(extras []modcfg.FileCopy, stage string) error {
	if b.Runner.DryRun() {
		for _, e := range extras {
			b.Report.Detail("would stage %s -> %s", e.From, e.To)
		}
		return nil
	}
	for _, e := range extras {
		src := filepath.Join(b.Mod.Root, filepath.FromSlash(e.From))
		if e.Optional {
			if _, err := os.Stat(src); os.IsNotExist(err) {
				b.Report.Detail("skipped %s (optional, not present)", e.From)
				continue
			}
		}
		dst := filepath.Join(stage, filepath.FromSlash(e.To))
		if err := copyInto(src, dst); err != nil {
			return fmt.Errorf("release: staging %s: %w", e.From, err)
		}
		b.Report.Detail("staged %s", e.To)
	}
	return nil
}

// assemblePayloads splits the staged mod into the declared bundles.
//
// With no payloads declared, the staged folder is the single deliverable. That
// is the common case for a mod that ships in one piece.
func (b *Builder) assemblePayloads(
	ch *modcfg.Channel,
	stages []modStage,
	packed []packedAddon,
	opts ReleaseOptions,
) ([]Payload, error) {
	names := ch.PayloadNames()
	if len(opts.Payloads) > 0 {
		names = opts.Payloads
	}
	primary := stages[0]
	stageDir := map[string]string{}
	for _, st := range stages {
		stageDir[st.ModName] = st.Dir
	}

	if len(ch.Payloads) == 0 {
		// The staged folders are the single deliverable. One folder is the common
		// case and needs no copy. With several, they get gathered under one
		// directory so an include that named its own folder still gets zipped and
		// hashed instead of quietly falling out of a green build.
		//
		// Gathered rather than zipped in place: releaseDir is shared between
		// repos, so archiving it directly would sweep up everything else in there.
		pl := Payload{Name: "all", Dir: primary.Dir, ModNames: []string{primary.ModName}, zipRoot: primary.ModName}
		if len(stages) > 1 {
			root := filepath.Join(b.releaseDir(ch), "payload-all")
			pl.Dir, pl.zipRoot = root, ""
			pl.ModNames = nil
			for _, st := range stages {
				pl.ModNames = append(pl.ModNames, st.ModName)
				if b.Runner.DryRun() {
					continue
				}
				if err := copyTree(st.Dir, filepath.Join(root, st.ModName)); err != nil {
					return nil, fmt.Errorf("release: %w", err)
				}
			}
		}
		for _, a := range packed {
			pl.Addons = append(pl.Addons, a.Name)
		}
		if err := b.zipPayload(ch, &pl, opts); err != nil {
			return nil, err
		}
		return []Payload{pl}, nil
	}

	// One payload that takes everything out of a single staged folder needs no
	// copy, so use the stage as-is. With several folders in play there is no one
	// directory that already holds the answer.
	single := len(stages) == 1 && len(names) == 1 && payloadTakesAll(ch.Payloads[names[0]], packed)

	var out []Payload
	for _, name := range names {
		spec, ok := ch.Payloads[name]
		if !ok {
			return nil, fmt.Errorf("release: no payload %q in channel %s", name, ch.Name)
		}

		pl := Payload{Name: name}
		root := filepath.Join(b.releaseDir(ch), "payload-"+name)

		// Group by @mod folder, so a payload fed by two sets ships two folders
		// rather than one merged folder an operator cannot take apart.
		for _, a := range packed {
			if !sideIn(a.Side, spec.Sides) {
				continue
			}
			pl.Addons = append(pl.Addons, a.Name)
			if !containsString(pl.ModNames, a.ModName) {
				pl.ModNames = append(pl.ModNames, a.ModName)
			}
			if single || b.Runner.DryRun() {
				continue
			}
			if err := copyAddonInto(a.PBO, filepath.Join(root, a.ModName, "Addons")); err != nil {
				return nil, fmt.Errorf("release: %w", err)
			}
		}

		if len(pl.Addons) == 0 {
			return nil, fmt.Errorf("release: payload %q would be empty; no addon has a matching side", name)
		}

		switch {
		case single:
			pl.Dir, pl.zipRoot = primary.Dir, primary.ModName
		case len(pl.ModNames) == 1:
			// Exactly one folder, so the payload directory can be that folder and
			// the archive keeps the shape it has always had.
			pl.Dir, pl.zipRoot = filepath.Join(root, pl.ModNames[0]), pl.ModNames[0]
		default:
			// Several folders: the payload directory holds them side by side and
			// goes into the archive as-is, so they extract unmerged.
			pl.Dir, pl.zipRoot = root, ""
		}

		if !single && !b.Runner.DryRun() {
			for _, mn := range pl.ModNames {
				if err := copySiblings(stageDir[mn], filepath.Join(root, mn)); err != nil {
					return nil, fmt.Errorf("release: %w", err)
				}
			}
			for _, e := range spec.ExtraFiles {
				src := filepath.Join(b.Mod.Root, filepath.FromSlash(e.From))
				if err := copyInto(src, filepath.Join(pl.Dir, filepath.FromSlash(e.To))); err != nil {
					return nil, fmt.Errorf("release: staging %s: %w", e.From, err)
				}
			}
		}

		b.Report.Step("%-28s %d addon(s) in %v", "payload "+name, len(pl.Addons), pl.ModNames)
		if err := b.zipPayload(ch, &pl, opts); err != nil {
			return nil, err
		}
		out = append(out, pl)
	}
	return out, nil
}

func (b *Builder) zipPayload(ch *modcfg.Channel, pl *Payload, opts ReleaseOptions) error {
	if !ch.Zip.Enabled || opts.NoZip {
		return nil
	}
	if len(ch.Zip.Payloads) > 0 && !containsString(ch.Zip.Payloads, pl.Name) {
		return nil
	}

	name := ch.Zip.Out
	if name == "" {
		name = b.Mod.Mod.ID
		if opts.Version != "" {
			name += "-" + opts.Version
		}
		if len(ch.Payloads) > 1 {
			name += "-" + pl.Name
		}
		name += ".zip"
	}
	dst := filepath.Join(b.releaseDir(ch), filepath.FromSlash(name))

	if b.Runner.DryRun() {
		b.Report.Detail("would zip %s -> %s", pl.Dir, dst)
		pl.Zip = dst
		return nil
	}
	// The archive contains the @mod folder(s) themselves, so it extracts straight
	// into a server or client directory. zipRoot is empty when pl.Dir already
	// holds them, which is how a multi-folder payload keeps them separate.
	if err := zipDir(pl.Dir, pl.zipRoot, dst); err != nil {
		return fmt.Errorf("release: %w", err)
	}
	pl.Zip = dst
	b.Report.Detail("zipped %s", filepath.Base(dst))
	return nil
}

// writeManifest records a SHA-256 for every shipped file, so a release can be
// checked after the fact.
func (b *Builder) writeManifest(ch *modcfg.Channel, res *ReleaseResult, opts ReleaseOptions) (string, error) {
	name := ch.Manifest.Out
	if name == "" {
		name = "RELEASE_MANIFEST.txt"
	}
	dst := filepath.Join(b.releaseDir(ch), filepath.FromSlash(name))

	if b.Runner.DryRun() {
		b.Report.Detail("would write %s", dst)
		return dst, nil
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "%s\n", b.Mod.Mod.Name)
	if opts.Version != "" {
		fmt.Fprintf(&sb, "version: %s\n", opts.Version)
	}
	fmt.Fprintf(&sb, "built:   %s\n", time.Now().UTC().Format(time.RFC3339))
	fmt.Fprintf(&sb, "signed:  %v\n", res.Signed)
	fmt.Fprintf(&sb, "algo:    %s\n\n", ch.Manifest.Algo)

	for _, pl := range res.Payloads {
		fmt.Fprintf(&sb, "[%s]\n", pl.Name)
		files, err := hashTree(pl.Dir)
		if err != nil {
			return "", fmt.Errorf("release: %w", err)
		}
		for _, f := range files {
			fmt.Fprintf(&sb, "%s  %s\n", f.sum, f.rel)
		}
		if pl.Zip != "" {
			sum, err := hashFile(pl.Zip)
			if err != nil {
				return "", fmt.Errorf("release: %w", err)
			}
			fmt.Fprintf(&sb, "%s  %s\n", sum, filepath.Base(pl.Zip))
		}
		sb.WriteString("\n")
	}

	if err := os.WriteFile(dst, []byte(sb.String()), 0o644); err != nil {
		return "", fmt.Errorf("release: writing %s: %w", dst, err)
	}
	b.Report.Detail("manifest %s", filepath.Base(dst))
	return dst, nil
}

// --- helpers --------------------------------------------------------------

func (b *Builder) releaseDir(ch *modcfg.Channel) string {
	if ch.Out != "" {
		return filepath.Dir(ch.Out)
	}
	if b.Host.Paths.ReleaseDir != "" {
		return b.Host.Paths.ReleaseDir
	}
	return filepath.Join(b.Mod.Root, "release")
}

func sideIn(side modcfg.Side, wanted []modcfg.Side) bool {
	for _, w := range wanted {
		if w == side {
			return true
		}
	}
	return false
}

func payloadTakesAll(spec *modcfg.Payload, packed []packedAddon) bool {
	for _, a := range packed {
		if !sideIn(a.Side, spec.Sides) {
			return false
		}
	}
	return true
}

func containsString(list []string, want string) bool {
	for _, v := range list {
		if v == want {
			return true
		}
	}
	return false
}

// copyAddonInto copies a PBO and any signatures beside it.
func copyAddonInto(pbo, dstDir string) error {
	if err := os.MkdirAll(dstDir, 0o755); err != nil {
		return err
	}
	if err := copyInto(pbo, filepath.Join(dstDir, filepath.Base(pbo))); err != nil {
		return err
	}
	sigs, err := filepath.Glob(pbo + ".*.bisign")
	if err != nil {
		return err
	}
	for _, s := range sigs {
		if err := copyInto(s, filepath.Join(dstDir, filepath.Base(s))); err != nil {
			return err
		}
	}
	return nil
}

// copySiblings copies everything in the staged mod except Addons, which the
// payload assembles itself.
func copySiblings(stage, dst string) error {
	entries, err := os.ReadDir(stage)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if strings.EqualFold(e.Name(), "Addons") {
			continue
		}
		src := filepath.Join(stage, e.Name())
		out := filepath.Join(dst, e.Name())
		if e.IsDir() {
			if err := copyTree(src, out); err != nil {
				return err
			}
			continue
		}
		if err := copyInto(src, out); err != nil {
			return err
		}
	}
	return nil
}

func copyTree(src, dst string) error {
	return filepath.WalkDir(src, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		out := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(out, 0o755)
		}
		return copyInto(path, out)
	})
}

// copyInto copies src to dst, supporting a trailing /* on src to mean "the
// contents of this directory".
func copyInto(src, dst string) error {
	if strings.HasSuffix(src, string(os.PathSeparator)+"*") || strings.HasSuffix(src, "/*") {
		dir := strings.TrimSuffix(strings.TrimSuffix(src, "*"), string(os.PathSeparator))
		dir = strings.TrimSuffix(dir, "/")
		return copyTree(dir, strings.TrimSuffix(dst, string(os.PathSeparator)))
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, data, 0o644)
}

type hashedFile struct{ rel, sum string }

func hashTree(root string) ([]hashedFile, error) {
	var out []hashedFile
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !d.Type().IsRegular() {
			return nil
		}
		sum, err := hashFile(path)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		out = append(out, hashedFile{rel: paths.ToSlash(rel), sum: sum})
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(out, func(i, j int) bool { return out[i].rel < out[j].rel })
	return out, nil
}

func hashFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// zipDir archives dir with prefix as the top-level folder inside the archive.
func zipDir(dir, prefix, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	f, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer f.Close()

	zw := zip.NewWriter(f)
	err = filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !d.Type().IsRegular() {
			return nil
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		w, err := zw.Create(paths.ToSlash(filepath.Join(prefix, rel)))
		if err != nil {
			return err
		}
		src, err := os.Open(path)
		if err != nil {
			return err
		}
		defer src.Close()
		_, err = io.Copy(w, src)
		return err
	})
	if err != nil {
		zw.Close()
		return err
	}
	return zw.Close()
}
