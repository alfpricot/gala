package manager

import (
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/alfpricot/gala/internal/semver"
)

// nugetManager drives the .NET SDK. The version lives in the package's one
// project file (.csproj, .fsproj, or .vbproj) and is written with a targeted
// text edit; publishing is `dotnet pack` then `dotnet nuget push`.
//
// MSBuild resolves Version across an import graph a text edit cannot see — a
// Directory.Build.props or .targets, a custom SDK, an environment variable —
// so the version MSBuild evaluates is checked against the project file before
// anything is written and again after the write. Without that, a release could
// tag one version and pack another, and a feed never lets a published version
// be replaced.
type nugetManager struct {
	r Runner
}

// packConfiguration is the configuration `dotnet pack` builds, and so the one
// every evaluation asks about: a Release-only version override is invisible to
// an evaluation under MSBuild's Debug default.
const packConfiguration = "Release"

// projectExts are the SDK-style project files that pack to a NuGet package.
var projectExts = []string{".csproj", ".fsproj", ".vbproj"}

func (n *nugetManager) Name() string { return "nuget" }

func (n *nugetManager) Tools() []string { return []string{"dotnet"} }

func (n *nugetManager) Manifest(dir, root string) string {
	proj, err := projectFile(dir)
	if err != nil {
		return ""
	}
	return filepath.ToSlash(filepath.Join(rel(dir, root), filepath.Base(proj)))
}

// projectFile finds the package directory's one project file. Zero or several
// is an error rather than a guess: the wrong one would release a different
// package.
func projectFile(dir string) (string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", err
	}
	var found []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		ext := strings.ToLower(filepath.Ext(e.Name()))
		for _, want := range projectExts {
			if ext == want {
				found = append(found, e.Name())
			}
		}
	}
	switch len(found) {
	case 1:
		return filepath.Join(dir, found[0]), nil
	case 0:
		return "", fmt.Errorf("%s: no .csproj, .fsproj, or .vbproj project file found", filepath.ToSlash(dir))
	default:
		return "", fmt.Errorf("%s: several project files (%s); gala releases one project per package path",
			filepath.ToSlash(dir), strings.Join(found, ", "))
	}
}

// project is a project file's bytes and the properties gala reads from it.
type project struct {
	path    string
	content string
	props   []xmlProp
}

func (n *nugetManager) read(dir string) (*project, error) {
	path, err := projectFile(dir)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	content := string(data)
	props, err := scanProps(content)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", filepath.ToSlash(path), err)
	}
	return &project{path: path, content: content, props: props}, nil
}

// versionProps returns the <Version> and <PackageVersion> declarations a
// release rewrites, refusing any shape whose value gala cannot know: a
// conditional or nested declaration, a version built from VersionPrefix, or
// none at all (an inherited version).
func (p *project) versionProps() ([]xmlProp, error) {
	file := filepath.ToSlash(p.path)
	var out []xmlProp
	prefix := false
	for _, pr := range p.props {
		switch pr.name {
		case "Version", "PackageVersion":
			if pr.conditional {
				return nil, fmt.Errorf("%s: <%s> is declared under a Condition or inside <Choose>, so the "+
					"version a build uses depends on properties gala does not see; declare it "+
					"unconditionally in a top-level <PropertyGroup>", file, pr.name)
			}
			out = append(out, pr)
		case "VersionPrefix":
			prefix = true
		}
	}
	if len(out) == 0 {
		if prefix {
			return nil, fmt.Errorf("%s: the version is built from <VersionPrefix>; gala writes <Version>, "+
				"so declare <Version> in the project file instead", file)
		}
		return nil, fmt.Errorf("%s: no <Version> found — gala releases packages independently, so the "+
			"project file needs an explicit <Version> rather than one inherited from Directory.Build.props", file)
	}
	return out, nil
}

// ManifestVersion reads <Version>. A <PackageVersion> beside it must agree:
// pack stamps PackageVersion, so a disagreement leaves no single version to
// release from.
func (n *nugetManager) ManifestVersion(dir string) (semver.Version, bool, error) {
	p, err := n.read(dir)
	if err != nil {
		return semver.Version{}, false, err
	}
	props, err := p.versionProps()
	if err != nil {
		return semver.Version{}, false, err
	}
	file := filepath.ToSlash(p.path)
	raw := p.content[props[0].start:props[0].end]
	value := strings.TrimSpace(raw)
	for _, pr := range props[1:] {
		if other := strings.TrimSpace(p.content[pr.start:pr.end]); other != value {
			return semver.Version{}, false, fmt.Errorf("%s: <%s>%s</%s> disagrees with <%s>%s</%s>; make them match",
				file, pr.name, other, pr.name, props[0].name, value, props[0].name)
		}
	}
	v, err := semver.Parse(value)
	if err != nil {
		return semver.Version{}, false, fmt.Errorf("%s: <%s>: %w", file, props[0].name, err)
	}
	return v, true, nil
}

// PackageName follows MSBuild's defaults: PackageId, else AssemblyName, else
// the project file's name.
func (n *nugetManager) PackageName(dir string) (string, error) {
	p, err := n.read(dir)
	if err != nil {
		return "", err
	}
	for _, key := range []string{"PackageId", "AssemblyName"} {
		if v := p.literal(key); v != "" {
			return v, nil
		}
	}
	return strings.TrimSuffix(filepath.Base(p.path), filepath.Ext(p.path)), nil
}

// literal returns the last unconditional declaration of key, or "" when it is
// absent, conditional, or built from other properties.
func (p *project) literal(key string) string {
	value := ""
	for _, pr := range p.props {
		if pr.name != key || pr.conditional {
			continue
		}
		value = strings.TrimSpace(p.content[pr.start:pr.end])
	}
	if strings.Contains(value, "$(") {
		return ""
	}
	return value
}

// SetVersion rewrites every <Version> and <PackageVersion> value in place,
// then asks MSBuild what pack would stamp. A disagreement restores the file
// and fails, before the tag exists.
func (n *nugetManager) SetVersion(dir string, v semver.Version) error {
	p, err := n.read(dir)
	if err != nil {
		return err
	}
	props, err := p.versionProps()
	if err != nil {
		return err
	}
	edited := p.content
	for i := len(props) - 1; i >= 0; i-- {
		edited = edited[:props[i].start] + v.String() + edited[props[i].end:]
	}
	if err := os.WriteFile(p.path, []byte(edited), 0o644); err != nil {
		return err
	}
	if err := n.confirm(dir, p.path, v.String()); err != nil {
		if restoreErr := os.WriteFile(p.path, []byte(p.content), 0o644); restoreErr != nil {
			return errors.Join(err, fmt.Errorf("cannot restore %s: %w", filepath.ToSlash(p.path), restoreErr))
		}
		return err
	}
	return nil
}

// confirm checks that MSBuild evaluates the project at want under the pack
// configuration. PackageVersion is asked for because it is what reaches the
// package; it defaults to Version.
func (n *nugetManager) confirm(dir, path, want string) error {
	out, err := n.r.Run(dir, "dotnet", "msbuild", path,
		"-p:Configuration="+packConfiguration, "-getProperty:PackageVersion")
	if err != nil {
		return fmt.Errorf("dotnet msbuild: %w", err)
	}
	got := lastLine(out)
	if got != want {
		return fmt.Errorf("%s: MSBuild evaluates PackageVersion as %q, but the project file says %q — "+
			"something outside the project file sets the version (a Directory.Build.props or .targets, a "+
			"custom SDK, or an environment variable); releasing would tag one version and publish another",
			filepath.ToSlash(path), got, want)
	}
	return nil
}

func (n *nugetManager) CheckVersion(string, semver.Version) error { return nil }

// RefreshLock stages nothing: packages.lock.json records what a restore
// resolved, never the project's own version, and gala rewrites no references.
func (n *nugetManager) RefreshLock(string, string) ([]string, error) { return nil, nil }

// Preflight confirms the project file's version is the one MSBuild evaluates,
// so an override is caught by --dry-run and before any write.
func (n *nugetManager) Preflight(dir string, _ bool) error {
	p, err := n.read(dir)
	if err != nil {
		return err
	}
	v, _, err := n.ManifestVersion(dir)
	if err != nil {
		return err
	}
	return n.confirm(dir, p.path, v.String())
}

// Publish packs into a temporary directory, so the push uploads exactly what
// this release built, then pushes each package. The feed and its credentials
// come from the repository's NuGet.config (defaultPushSource), the way npm's
// registry comes from .npmrc. --skip-duplicate makes a re-run after a partial
// push a no-op rather than a 409.
func (n *nugetManager) Publish(dir string) error {
	proj, err := projectFile(dir)
	if err != nil {
		return err
	}
	out, err := os.MkdirTemp("", "gala-nupkg-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(out) }()

	if _, err := n.r.Run(dir, "dotnet", "pack", proj, "--configuration", packConfiguration, "--output", out); err != nil {
		return fmt.Errorf("dotnet pack: %w", err)
	}
	pkgs, err := packedArtifacts(out)
	if err != nil {
		return err
	}
	if len(pkgs) == 0 {
		return fmt.Errorf("dotnet pack produced no .nupkg for %s — check <IsPackable>", filepath.ToSlash(proj))
	}
	for _, pkg := range pkgs {
		if _, err := n.r.Run(dir, "dotnet", "nuget", "push", pkg, "--skip-duplicate"); err != nil {
			return fmt.Errorf("dotnet nuget push: %w", err)
		}
	}
	return nil
}

// packedArtifacts lists the .nupkg files in dir. A legacy .symbols.nupkg is
// left out: `dotnet nuget push` uploads symbols beside their package, and
// pushing one on its own would publish debug symbols under the package's id.
func packedArtifacts(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range entries {
		name := strings.ToLower(e.Name())
		if strings.HasSuffix(name, ".nupkg") && !strings.HasSuffix(name, ".symbols.nupkg") {
			out = append(out, filepath.Join(dir, e.Name()))
		}
	}
	sort.Strings(out)
	return out, nil
}

func (n *nugetManager) Recovery(dir, root string) string {
	proj := "<project file>"
	if p, err := projectFile(dir); err == nil {
		proj = filepath.Base(p)
	}
	return fmt.Sprintf("cd %s && dotnet pack %s --configuration %s --output nupkg && "+
		"dotnet nuget push \"nupkg/*.nupkg\" --skip-duplicate", rel(dir, root), proj, packConfiguration)
}

// lastLine is the last non-empty line of a command's output: the value
// `-getProperty` prints, after anything the SDK wrote first.
func lastLine(out string) string {
	lines := strings.Split(strings.TrimSpace(out), "\n")
	return strings.TrimSpace(lines[len(lines)-1])
}

// xmlProp is one MSBuild property declaration: its element name, the byte
// range of its value, and whether its value depends on a condition.
type xmlProp struct {
	name        string
	start, end  int
	conditional bool
}

// readProps are the properties gala reads or writes.
var readProps = map[string]bool{
	"Version": true, "PackageVersion": true, "VersionPrefix": true,
	"PackageId": true, "AssemblyName": true,
}

// scanProps locates property declarations by byte offset, so a write
// replaces a value and leaves every other byte — comments, attributes,
// whitespace, line endings — as it was. A declaration is conditional when it
// or its <PropertyGroup> carries a Condition, or when it sits anywhere other
// than Project > PropertyGroup (a <Choose>/<When> branch).
func scanProps(content string) ([]xmlProp, error) {
	// encoding/xml rejects a byte-order mark; offsets stay in the original.
	body := strings.TrimPrefix(content, "\ufeff")
	base := len(content) - len(body)

	type frame struct {
		name        string
		conditional bool
		prop        int // index into out, or -1
	}
	d := xml.NewDecoder(strings.NewReader(body))
	var stack []frame
	var out []xmlProp
	for {
		before := int(d.InputOffset())
		tok, err := d.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("not well-formed XML: %w", err)
		}
		switch t := tok.(type) {
		case xml.StartElement:
			cond := false
			for _, a := range t.Attr {
				if a.Name.Local == "Condition" {
					cond = true
				}
			}
			if len(stack) > 0 && stack[len(stack)-1].conditional {
				cond = true
			}
			f := frame{name: t.Name.Local, conditional: cond, prop: -1}
			if readProps[f.name] && len(stack) > 0 && stack[len(stack)-1].name == "PropertyGroup" {
				topLevel := len(stack) == 2 && stack[0].name == "Project"
				start := base + int(d.InputOffset())
				out = append(out, xmlProp{name: f.name, start: start, end: start, conditional: cond || !topLevel})
				f.prop = len(out) - 1
			}
			stack = append(stack, f)
		case xml.EndElement:
			f := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			if f.prop >= 0 {
				out[f.prop].end = base + before
			}
		}
	}
	return out, nil
}
