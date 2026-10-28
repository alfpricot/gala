package release

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/alfpricot/gala/internal/gittest"
)

// TestDryRunIsByteIdentical is flagship test 1: a dry run runs the checks and
// the preflights and writes nothing at all — same bytes, same HEAD, same tags.
func TestDryRunIsByteIdentical(t *testing.T) {
	r, _, cleanup := fixture(t, appConfig)
	defer cleanup()

	if err := r.CommitFile("app.go", "package app\n", "feat: first feature"); err != nil {
		t.Fatal(err)
	}

	beforeSnap := snapshot(t, r.Dir)
	beforeHEAD, err := r.HeadSHA()
	if err != nil {
		t.Fatal(err)
	}
	beforeTags := tags(t, r)

	opts, out, warn := testOptions(r, nil)
	if err := Run(opts, ModeDryRun); err != nil {
		t.Fatalf("dry run failed: %v", err)
	}

	afterSnap := snapshot(t, r.Dir)
	if diffs := diffMaps(beforeSnap, afterSnap); len(diffs) > 0 {
		t.Errorf("dry run changed the working tree: %v", diffs)
	}
	afterHEAD, err := r.HeadSHA()
	if err != nil {
		t.Fatal(err)
	}
	if afterHEAD != beforeHEAD {
		t.Error("dry run created a commit")
	}
	if got := tags(t, r); got != beforeTags {
		t.Errorf("dry run created a tag: %q → %q", beforeTags, got)
	}

	got := out.String()
	mustContain(t, got, "dry run — preflights passed; nothing was written", "dry-run banner")
	mustContain(t, got, "would tag     v0.1.0", "the dry run states the tag")
	mustContain(t, got, "would push", "the dry run states the push")
	mustContain(t, got, "publish nothing (publish = false)", "publish is off by default")
	if warn.String() != "" {
		t.Errorf("unexpected warnings: %s", warn.String())
	}
}

// TestFullLocalRelease is flagship test 2: a real release against a bare file
// remote — commit, annotated tag, atomic push, and the dev fast-forward all
// exercised for real, with the skip-ci marker in the commit body.
func TestFullLocalRelease(t *testing.T) {
	r, bare, cleanup := fixture(t, appConfig)
	defer cleanup()

	if err := r.CommitFile("app.go", "package app\n", "feat: first feature"); err != nil {
		t.Fatal(err)
	}
	if err := r.CommitFile("doc.md", "docs\n", "docs: mention the flag"); err != nil {
		t.Fatal(err)
	}

	opts, out, warn := testOptions(r, nil)
	if err := Run(opts, ModeRelease); err != nil {
		t.Fatalf("release failed: %v\n%s", err, out.String())
	}

	localHEAD, err := r.HeadSHA()
	if err != nil {
		t.Fatal(err)
	}

	// Both refs in the bare repo point at the release commit.
	for _, branch := range []string{"main", "dev"} {
		sha, err := gittest.RemoteRef(bare, branch)
		if err != nil {
			t.Fatalf("bare repo has no %s: %v", branch, err)
		}
		if sha != localHEAD {
			t.Errorf("bare %s = %s, want the release commit %s", branch, sha, localHEAD)
		}
	}

	// The tag exists locally and on the remote.
	if got := tags(t, r); got != "v0.1.0" {
		t.Errorf("local tags = %q, want v0.1.0", got)
	}
	if sha, err := gittest.Ref(bare, "refs/tags/v0.1.0"); err != nil || sha != localHEAD {
		t.Errorf("remote tag = %s, %v; want %s", sha, err, localHEAD)
	}

	// The commit message: subject plus skip-ci marker in the body.
	msg, err := gittest.CommitMessage(r.Dir, localHEAD)
	if err != nil {
		t.Fatal(err)
	}
	mustContain(t, msg, "chore(release): v0.1.0", "release commit subject")
	mustContain(t, msg, "[skip ci]", "the skip-ci marker is intentional")

	// The changelog was written and committed.
	changelog, err := r.ReadFile("CHANGELOG.md")
	if err != nil {
		t.Fatal(err)
	}
	mustContain(t, changelog, "## 0.1.0 (2026-10-27)", "the entry header")
	mustContain(t, changelog, "### Features", "the features section")
	mustContain(t, changelog, "* first feature", "the commit bullet")
	mustNotContain(t, changelog, "mention the flag", "docs commits are hidden")

	transcript := out.String()
	mustContain(t, transcript, "chore(release): v0.1.0", "the transcript shows the commit")
	mustContain(t, transcript, "tag     v0.1.0", "the transcript shows the tag")
	mustContain(t, transcript, "atomic", "the transcript shows the atomic push")
	mustContain(t, transcript, "sync    main → dev (fast-forward only)", "the transcript shows the sync")
	if warn.String() != "" {
		t.Errorf("unexpected warnings: %s", warn.String())
	}

	// Running again is a no-op: nothing due, exit 0, no writes.
	opts2, out2, _ := testOptions(r, nil)
	if err := Run(opts2, ModeRelease); err != nil {
		t.Fatalf("second release must succeed with nothing to do: %v", err)
	}
	mustContain(t, out2.String(), "nothing to do", "the second run explains itself")
	if got := tags(t, r); got != "v0.1.0" {
		t.Errorf("second run created a tag: %q", got)
	}
}

// TestSyncRejectionWarnsWithoutFailing is flagship test 3: commits landed on
// dev during the release, so the sync push is rejected. The release still
// succeeds, the warning carries the manual merge command, and dev is untouched.
func TestSyncRejectionWarnsWithoutFailing(t *testing.T) {
	r, bare, cleanup := fixture(t, appConfig)
	defer cleanup()

	// A stray commit lands on dev and is pushed before the release.
	if err := r.Checkout("dev"); err != nil {
		t.Fatal(err)
	}
	if err := r.CommitFile("stray.txt", "in flight\n", "feat: work in flight on dev"); err != nil {
		t.Fatal(err)
	}
	stray, err := r.HeadSHA()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.Run("push", "-q", "origin", "dev"); err != nil {
		t.Fatal(err)
	}
	if err := r.Checkout("main"); err != nil {
		t.Fatal(err)
	}
	if err := r.CommitFile("app.go", "package app\n", "feat: releasable work"); err != nil {
		t.Fatal(err)
	}

	opts, out, warn := testOptions(r, nil)
	if err := Run(opts, ModeRelease); err != nil {
		t.Fatalf("a rejected sync must not fail the release: %v", err)
	}

	// The release landed...
	localHEAD, err := r.HeadSHA()
	if err != nil {
		t.Fatal(err)
	}
	mainSHA, err := gittest.RemoteRef(bare, "main")
	if err != nil {
		t.Fatal(err)
	}
	if mainSHA != localHEAD {
		t.Errorf("bare main = %s, want %s", mainSHA, localHEAD)
	}
	// ...but dev was never forced.
	devSHA, err := gittest.RemoteRef(bare, "dev")
	if err != nil {
		t.Fatal(err)
	}
	if devSHA != stray {
		t.Errorf("bare dev = %s, want the untouched stray commit %s", devSHA, stray)
	}

	warnings := warn.String()
	mustContain(t, warnings, "warning:", "the sync rejection is a warning")
	mustContain(t, warnings, "fast-forward", "it explains what failed")
	mustContain(t, warnings, "git fetch origin && git switch dev", "it prints the manual merge command")
	mustContain(t, out.String(), "sync    main → dev", "the transcript shows the sync step")
}

// TestMonorepoRelease covers the monorepo driver: declared packages, path-scoped planning, per
// package tags, one release commit, and publishes in declaration order.
func TestMonorepoRelease(t *testing.T) {
	cfg := `kind = "monorepo"
manager = "cargo"
publish = true
githubRelease = false
releaseBranches = ["main"]

[[package]]
path = "packages/core"

[[package]]
path = "packages/cli"
`
	r, cleanup, err := gittest.New()
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()

	write := func(path, content string) {
		t.Helper()
		if err := r.WriteFile(path, content); err != nil {
			t.Fatal(err)
		}
	}
	commit := func(path, content, msg string) {
		t.Helper()
		if err := r.CommitFile(path, content, msg); err != nil {
			t.Fatal(err)
		}
	}

	write("gala.toml", cfg)
	write("packages/core/Cargo.toml", "[package]\nname = \"core\"\nversion = \"1.0.0\"\nedition = \"2021\"\n")
	write("packages/cli/Cargo.toml", "[package]\nname = \"cli\"\nversion = \"1.0.0\"\nedition = \"2021\"\n")
	if err := r.CommitAll("chore: declare the packages"); err != nil {
		t.Fatal(err)
	}
	if err := r.Tag("core/v1.0.0"); err != nil {
		t.Fatal(err)
	}
	if err := r.Tag("cli/v1.0.0"); err != nil {
		t.Fatal(err)
	}
	bare, _, err := r.BareRemote("origin", "main")
	if err != nil {
		t.Fatal(err)
	}

	// Only core changes: only core releases.
	commit("packages/core/src/lib.rs", "pub fn w() {}\n", "feat: shiny new widget")

	exec := &captureRunner{}
	opts, out, _ := testOptions(r, exec)
	if err := Run(opts, ModePlan); err != nil {
		t.Fatal(err)
	}
	planOut := out.String()
	mustContain(t, planOut, "core → core/v1.1.0", "the changed package plans a minor")
	mustContain(t, planOut, "cli → no release", "the untouched package does not release")
	mustContain(t, planOut, "1 of 2 packages due", "the header counts due packages")

	// Now cli changes too: both release, publishes in declaration order.
	commit("packages/cli/src/lib.rs", "pub fn main() {}\n", "fix: honor the config file")

	exec2 := &captureRunner{}
	opts2, out2, warn2 := testOptions(r, exec2)
	if err := Run(opts2, ModeRelease); err != nil {
		t.Fatalf("monorepo release failed: %v\n%s", err, out2.String())
	}

	// Publish order matches declaration order: core before cli.
	publishes := []string{}
	for _, c := range exec2.calls {
		if c.args[0] == "publish" {
			publishes = append(publishes, c.dir)
		}
	}
	if len(publishes) != 2 {
		t.Fatalf("publishes = %v, want one per released package", publishes)
	}
	if !strings.HasSuffix(filepathSlash(publishes[0]), "packages/core") ||
		!strings.HasSuffix(filepathSlash(publishes[1]), "packages/cli") {
		t.Errorf("publish order must follow declaration order (core, cli): %v", publishes)
	}

	// Both manifests were written by the cargo adapter's text edit.
	coreToml, err := r.ReadFile("packages/core/Cargo.toml")
	if err != nil {
		t.Fatal(err)
	}
	mustContain(t, coreToml, `version = "1.1.0"`, "core was bumped minor")
	cliToml, err := r.ReadFile("packages/cli/Cargo.toml")
	if err != nil {
		t.Fatal(err)
	}
	mustContain(t, cliToml, `version = "1.0.1"`, "cli was bumped patch")
	mustContain(t, cliToml, `edition = "2021"`, "unrelated bytes survive")

	// One release commit, two annotated tags, one push.
	head, err := r.HeadSHA()
	if err != nil {
		t.Fatal(err)
	}
	msg, err := gittest.CommitMessage(r.Dir, head)
	if err != nil {
		t.Fatal(err)
	}
	mustContain(t, msg, "chore(release): 2 packages", "monorepo commit subject")
	mustContain(t, msg, "[skip ci]", "skip-ci marker")
	mustContain(t, msg, "- core 1.1.0", "the body lists core")
	mustContain(t, msg, "- cli 1.0.1", "the body lists cli")

	for _, tag := range []string{"core/v1.1.0", "cli/v1.0.1"} {
		if !strings.Contains(tags(t, r), tag) {
			t.Errorf("missing tag %s (tags: %q)", tag, tags(t, r))
		}
	}
	if strings.Contains(tags(t, r), "cli/v1.1.0") {
		t.Error("cli must not get a minor tag; it had no minor change")
	}

	// One root changelog, package entries in declaration order.
	changelog, err := r.ReadFile("CHANGELOG.md")
	if err != nil {
		t.Fatal(err)
	}
	mustContain(t, changelog, "## core 1.1.0 (2026-10-27)", "core entry")
	mustContain(t, changelog, "## cli 1.0.1 (2026-10-27)", "cli entry")
	if strings.Index(changelog, "## core") > strings.Index(changelog, "## cli") {
		t.Errorf("entries must follow declaration order:\n%s", changelog)
	}

	// Everything is on the remote.
	remoteMain, err := gittest.RemoteRef(bare, "main")
	if err != nil {
		t.Fatalf("bare repo has no main: %v", err)
	}
	if remoteMain != head {
		t.Errorf("bare main = %s, want the release commit %s", remoteMain, head)
	}
	if warn2.String() != "" {
		t.Errorf("unexpected warnings: %s", warn2.String())
	}
	mustContain(t, out2.String(), "publish core (cargo)", "the transcript shows each publish")
}

func filepathSlash(p string) string { return strings.ReplaceAll(p, "\\", "/") }

// TestReleasePerManager checks that `gala release` completes on an
// app fixture for every registry manager, real git underneath and the
// argv-capture runner standing in for the lock and publish commands.
func TestReleasePerManager(t *testing.T) {
	cases := []struct {
		manager  string
		files    map[string]string
		manifest string // repo-relative file whose version the adapter writes by hand; "" when npm owns it
		want     [][]string
	}{
		{
			manager: "npm",
			files:   map[string]string{"package.json": "{\n  \"name\": \"widget\",\n  \"version\": \"1.0.0\"\n}\n"},
			want: [][]string{
				{"npm", "whoami"},
				{"npm", "version", "1.1.0", "--no-git-tag-version"},
				{"npm", "publish"},
			},
		},
		{
			manager: "cargo",
			files: map[string]string{
				"Cargo.toml": "[package]\nname = \"widget\"\nversion = \"1.0.0\" # bumped by gala\n",
				"Cargo.lock": "version = 4\n",
			},
			manifest: "Cargo.toml",
			want: [][]string{
				{"cargo", "update", "--workspace"},
				{"cargo", "publish"},
			},
		},
		{
			manager: "pypi",
			files: map[string]string{
				"pyproject.toml": "[project]\nname = \"widget\"\nversion = \"1.0.0\"\n",
				"uv.lock":        "version = 1\n",
			},
			manifest: "pyproject.toml",
			want: [][]string{
				{"uv", "lock"},
				{"uv", "build"},
				{"uv", "publish"},
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.manager, func(t *testing.T) {
			cfg := "kind = \"app\"\nmanager = \"" + tc.manager + "\"\npublish = true\ngithubRelease = false\n"
			r, cleanup, err := gittest.New()
			if err != nil {
				t.Fatal(err)
			}
			defer cleanup()
			if err := r.WriteFile("gala.toml", cfg); err != nil {
				t.Fatal(err)
			}
			for path, content := range tc.files {
				if err := r.WriteFile(path, content); err != nil {
					t.Fatal(err)
				}
			}
			if err := r.CommitAll("chore: scaffold"); err != nil {
				t.Fatal(err)
			}
			if err := r.Tag("v1.0.0"); err != nil {
				t.Fatal(err)
			}
			bare, bareCleanup, err := r.BareRemote("origin", "main")
			if err != nil {
				t.Fatal(err)
			}
			defer bareCleanup()
			if err := r.CommitFile("src.txt", "x\n", "feat: a widget"); err != nil {
				t.Fatal(err)
			}

			exec := &captureRunner{}
			opts, out, warn := testOptions(r, exec)
			if err := Run(opts, ModeRelease); err != nil {
				t.Fatalf("release failed: %v\n%s", err, out.String())
			}

			var got [][]string
			for _, c := range exec.calls {
				got = append(got, append([]string{c.name}, c.args...))
			}
			if strings.Join(flatten(got), " | ") != strings.Join(flatten(tc.want), " | ") {
				t.Errorf("argv = %v, want %v", got, tc.want)
			}

			if tc.manifest != "" {
				m, err := r.ReadFile(tc.manifest)
				if err != nil {
					t.Fatal(err)
				}
				mustContain(t, m, `version = "1.1.0"`, tc.manifest+" is bumped by the text edit")
			}
			if !strings.Contains(tags(t, r), "v1.1.0") {
				t.Errorf("missing tag v1.1.0 (tags: %q)", tags(t, r))
			}
			head, err := r.HeadSHA()
			if err != nil {
				t.Fatal(err)
			}
			remoteMain, err := gittest.RemoteRef(bare, "main")
			if err != nil {
				t.Fatal(err)
			}
			if remoteMain != head {
				t.Errorf("bare main = %s, want the release commit %s", remoteMain, head)
			}
			if warn.String() != "" {
				t.Errorf("unexpected warnings: %s", warn.String())
			}
		})
	}
}

func flatten(argvs [][]string) []string {
	out := make([]string, 0, len(argvs))
	for _, a := range argvs {
		out = append(out, strings.Join(a, " "))
	}
	return out
}

// TestReleaseNuget runs a nuget release end to end: MSBuild is asked for the
// version before and after the write, the push source is checked before the
// write, the project file is bumped in place and committed, and the package is
// packed and pushed after the tag.
func TestReleaseNuget(t *testing.T) {
	cfg := "kind = \"app\"\nmanager = \"nuget\"\npublish = true\ngithubRelease = false\n"
	r, cleanup, err := gittest.New()
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	proj := "<Project Sdk=\"Microsoft.NET.Sdk\">\n  <PropertyGroup>\n    <PackageId>Martin.Widget</PackageId>\n" +
		"    <Version>1.0.0</Version>\n  </PropertyGroup>\n</Project>\n"
	if err := r.WriteFile("gala.toml", cfg); err != nil {
		t.Fatal(err)
	}
	if err := r.WriteFile("Widget.csproj", proj); err != nil {
		t.Fatal(err)
	}
	if err := r.CommitAll("chore: scaffold"); err != nil {
		t.Fatal(err)
	}
	if err := r.Tag("v1.0.0"); err != nil {
		t.Fatal(err)
	}
	_, bareCleanup, err := r.BareRemote("origin", "main")
	if err != nil {
		t.Fatal(err)
	}
	defer bareCleanup()
	if err := r.CommitFile("Widget.cs", "namespace Widget;\n", "feat: a widget"); err != nil {
		t.Fatal(err)
	}

	versionRe := regexp.MustCompile(`<Version>([^<]*)</Version>`)
	exec := &captureRunner{respond: func(dir, _ string, args []string) (string, error) {
		switch args[0] {
		case "msbuild":
			data, err := os.ReadFile(args[1])
			if err != nil {
				return "", err
			}
			v := versionRe.FindStringSubmatch(string(data))[1]
			return `{"Properties": {"PackageVersion": "` + v + `", "IsPackable": ""}}` + "\n", nil
		case "pack":
			out := args[len(args)-1]
			return "", os.WriteFile(filepath.Join(out, "Martin.Widget.1.1.0.nupkg"), nil, 0o644)
		}
		return "", nil
	}}
	opts, out, warn := testOptions(r, exec)
	if err := Run(opts, ModeRelease); err != nil {
		t.Fatalf("release failed: %v\n%s", err, out.String())
	}

	var got []string
	for _, c := range exec.calls {
		got = append(got, c.name+" "+c.args[0])
	}
	want := []string{"dotnet msbuild", "dotnet nuget", "dotnet msbuild", "dotnet pack", "dotnet nuget"}
	if strings.Join(got, " | ") != strings.Join(want, " | ") {
		t.Errorf("commands = %v, want %v", got, want)
	}
	push := exec.calls[len(exec.calls)-1].args
	if len(push) != 4 || push[1] != "push" || !strings.HasSuffix(push[2], "Martin.Widget.1.1.0.nupkg") || push[3] != "--skip-duplicate" {
		t.Errorf("push argv = %v", push)
	}

	m, err := r.ReadFile("Widget.csproj")
	if err != nil {
		t.Fatal(err)
	}
	if m != strings.Replace(proj, "1.0.0", "1.1.0", 1) {
		t.Errorf("Widget.csproj changed beyond the version:\n%s", m)
	}
	if !strings.Contains(tags(t, r), "v1.1.0") {
		t.Errorf("missing tag v1.1.0 (tags: %q)", tags(t, r))
	}
	if status, err := r.Run("status", "--porcelain"); err != nil || strings.TrimSpace(status) != "" {
		t.Errorf("the bumped project file must be committed: %q, %v", status, err)
	}
	if warn.String() != "" {
		t.Errorf("unexpected warnings: %s", warn.String())
	}
}
