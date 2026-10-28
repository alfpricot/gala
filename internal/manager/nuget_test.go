package manager

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alfpricot/gala/internal/semver"
)

const widgetProj = `<Project Sdk="Microsoft.NET.Sdk">
  <PropertyGroup>
    <TargetFramework>net10.0</TargetFramework>
    <PackageId>Martin.Widget</PackageId>
    <Version>1.0.0</Version>
  </PropertyGroup>
</Project>
`

// msbuildAnswers stands in for `dotnet msbuild -getProperty` with a fixed
// answer, and accepts every other command.
func msbuildAnswers(version string) *captureRunner {
	return &captureRunner{respond: func(_, _ string, args []string) (string, error) {
		if args[0] == "msbuild" {
			return evaluation(version, "true"), nil
		}
		return "", nil
	}}
}

// evaluation is the JSON `dotnet msbuild` prints for two -getProperty
// switches, after a warning the decoder must skip.
func evaluation(version, packable string) string {
	return "P.csproj(3,3): warning MSB4011: a.props cannot be imported again.\r\n" +
		"{\r\n  \"Properties\": {\r\n    \"PackageVersion\": \"" + version + "\",\r\n" +
		"    \"IsPackable\": \"" + packable + "\"\r\n  }\r\n}\r\n"
}

func TestNugetManifestRead(t *testing.T) {
	dir := tempWith(t, "Widget.csproj", widgetProj)
	n, _ := New("nuget", &captureRunner{})
	v, ok, err := n.ManifestVersion(dir)
	if err != nil || !ok || v.String() != "1.0.0" {
		t.Fatalf("ManifestVersion = %v, %v, %v", v, ok, err)
	}
	name, err := n.PackageName(dir)
	if err != nil || name != "Martin.Widget" {
		t.Errorf("PackageName = %q, %v (want the PackageId)", name, err)
	}
	if got := n.Manifest(dir, dir); got != "Widget.csproj" {
		t.Errorf("Manifest = %q", got)
	}
	if tools := n.Tools(); len(tools) != 1 || tools[0] != "dotnet" {
		t.Errorf("Tools = %v", tools)
	}
}

func TestNugetPackageNameDefaults(t *testing.T) {
	n, _ := New("nuget", &captureRunner{})

	dir := tempWith(t, "Widget.fsproj", "<Project><PropertyGroup><AssemblyName>Widget.Core</AssemblyName>"+
		"<Version>1.0.0</Version></PropertyGroup></Project>")
	if name, err := n.PackageName(dir); err != nil || name != "Widget.Core" {
		t.Errorf("without PackageId the AssemblyName is the id: %q, %v", name, err)
	}

	dir = tempWith(t, "Widget.vbproj", "<Project><PropertyGroup><PackageId>$(AssemblyName).Pkg</PackageId>"+
		"<Version>1.0.0</Version></PropertyGroup></Project>")
	if name, err := n.PackageName(dir); err != nil || name != "Widget" {
		t.Errorf("a computed PackageId falls back to the project name: %q, %v", name, err)
	}
}

func TestNugetSetVersionPreservesBytes(t *testing.T) {
	before := "\ufeff" + strings.Join([]string{
		`<Project Sdk="Microsoft.NET.Sdk">`,
		`  <!-- bumped by the release -->`,
		`  <PropertyGroup Label="Packaging">`,
		`    <Version> 1.0.0 </Version>`,
		`    <PackageVersion>1.0.0</PackageVersion>`,
		`    <Description>Version 1.0.0 of the widget</Description>`,
		`  </PropertyGroup>`,
		`</Project>`,
		``,
	}, "\r\n")
	dir := tempWith(t, "Widget.csproj", before)
	r := msbuildAnswers("1.1.0")
	n, _ := New("nuget", r)
	if err := n.SetVersion(dir, semver.MustParse("1.1.0")); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "Widget.csproj"))
	if err != nil {
		t.Fatal(err)
	}
	want := strings.Replace(strings.Replace(before, "<Version> 1.0.0 </Version>", "<Version>1.1.0</Version>", 1),
		"<PackageVersion>1.0.0</PackageVersion>", "<PackageVersion>1.1.0</PackageVersion>", 1)
	if string(data) != want {
		t.Errorf("byte-exact write failed:\n--- got ---\n%q\n--- want ---\n%q", data, want)
	}

	proj := filepath.Join(dir, "Widget.csproj")
	wantArgv := "dotnet msbuild " + proj + " -p:Configuration=Release -getProperty:PackageVersion -getProperty:IsPackable"
	if got := r.argvs(); len(got) != 1 || got[0] != wantArgv {
		t.Errorf("argv = %v, want [%s]", got, wantArgv)
	}
}

// TestNugetSetVersionRestoresOnOverride covers a version set outside the
// project file: the write is refused and undone before anything is tagged.
func TestNugetSetVersionRestoresOnOverride(t *testing.T) {
	dir := tempWith(t, "Widget.csproj", widgetProj)
	n, _ := New("nuget", msbuildAnswers("9.9.9"))
	err := n.SetVersion(dir, semver.MustParse("1.1.0"))
	if err == nil {
		t.Fatal("a version MSBuild does not honor must be refused")
	}
	if !strings.Contains(err.Error(), "Directory.Build.targets") {
		t.Errorf("error should name the likely cause: %v", err)
	}
	data, _ := os.ReadFile(filepath.Join(dir, "Widget.csproj"))
	if string(data) != widgetProj {
		t.Errorf("the project file must be restored:\n%s", data)
	}

	failing := &captureRunner{respond: func(string, string, []string) (string, error) {
		return "", errors.New("MSB1009")
	}}
	n, _ = New("nuget", failing)
	if err := n.SetVersion(dir, semver.MustParse("1.1.0")); err == nil {
		t.Fatal("a failed evaluation must fail the write")
	}
	data, _ = os.ReadFile(filepath.Join(dir, "Widget.csproj"))
	if string(data) != widgetProj {
		t.Errorf("the project file must be restored after a failed evaluation:\n%s", data)
	}
}

func TestNugetPreflight(t *testing.T) {
	dir := tempWith(t, "Widget.csproj", widgetProj)
	r := msbuildAnswers("1.0.0")
	n, _ := New("nuget", r)
	if err := n.Preflight(dir, false); err != nil {
		t.Errorf("an agreeing evaluation passes: %v", err)
	}
	if len(r.calls) != 1 {
		t.Errorf("publishing off checks the version only: %v", r.argvs())
	}
	if err := n.Preflight(dir, true); err != nil {
		t.Errorf("a packable project with a push source passes: %v", err)
	}
	if got := r.argvs(); got[len(got)-1] != "dotnet nuget config get defaultPushSource" {
		t.Errorf("publishing on must check the push source: %v", got)
	}

	n, _ = New("nuget", msbuildAnswers("2.0.0"))
	if err := n.Preflight(dir, false); err == nil {
		t.Error("an override must fail the preflight, so --dry-run reports it")
	}
}

// TestNugetPreflightPublishFailures covers what would otherwise fail only
// after the tag is pushed.
func TestNugetPreflightPublishFailures(t *testing.T) {
	dir := tempWith(t, "Widget.csproj", widgetProj)
	unpackable := &captureRunner{respond: func(_, _ string, args []string) (string, error) {
		return evaluation("1.0.0", "false"), nil
	}}
	n, _ := New("nuget", unpackable)
	if err := n.Preflight(dir, false); err != nil {
		t.Errorf("an unpackable project is fine when nothing publishes: %v", err)
	}
	if err := n.Preflight(dir, true); err == nil || !strings.Contains(err.Error(), "IsPackable") {
		t.Errorf("an unpackable project must fail before the tag: %v", err)
	}

	noSource := &captureRunner{respond: func(_, _ string, args []string) (string, error) {
		if args[0] == "nuget" {
			return "error: Key 'defaultPushSource' not found.", errors.New("exit status 2")
		}
		return evaluation("1.0.0", ""), nil
	}}
	n, _ = New("nuget", noSource)
	if err := n.Preflight(dir, true); err == nil || !strings.Contains(err.Error(), "defaultPushSource") {
		t.Errorf("a missing push source must fail before the tag: %v", err)
	}
}

func TestNugetUnreleasableShapesRefused(t *testing.T) {
	cases := map[string]string{
		"conditional group": `<Project><PropertyGroup Condition="'$(Configuration)'=='Release'">` +
			`<Version>1.0.0</Version></PropertyGroup></Project>`,
		"conditional element": `<Project><PropertyGroup><Version Condition="true">1.0.0</Version>` +
			`</PropertyGroup></Project>`,
		"choose branch": `<Project><Choose><When Condition="true"><PropertyGroup><Version>1.0.0</Version>` +
			`</PropertyGroup></When></Choose></Project>`,
		"prefix":       `<Project><PropertyGroup><VersionPrefix>1.0.0</VersionPrefix></PropertyGroup></Project>`,
		"inherited":    `<Project><PropertyGroup><TargetFramework>net10.0</TargetFramework></PropertyGroup></Project>`,
		"disagreement": `<Project><PropertyGroup><Version>1.0.0</Version><PackageVersion>1.0.1</PackageVersion></PropertyGroup></Project>`,
		"computed":     `<Project><PropertyGroup><Version>$(Major).0.0</Version></PropertyGroup></Project>`,
	}
	for name, content := range cases {
		t.Run(name, func(t *testing.T) {
			dir := tempWith(t, "Widget.csproj", content)
			n, _ := New("nuget", &captureRunner{})
			_, _, err := n.ManifestVersion(dir)
			if err == nil {
				t.Fatal("must be refused at plan time")
			}
			if !strings.Contains(err.Error(), "Widget.csproj") {
				t.Errorf("error must name the file: %v", err)
			}
		})
	}
}

func TestNugetProjectFileDiscovery(t *testing.T) {
	n, _ := New("nuget", &captureRunner{})
	dir := tempWith(t, "README.md", "x")
	if _, _, err := n.ManifestVersion(dir); err == nil || !strings.Contains(err.Error(), "no .csproj") {
		t.Errorf("no project file: %v", err)
	}
	dir = tempWith(t, "A.csproj", widgetProj)
	if err := os.WriteFile(filepath.Join(dir, "B.fsproj"), []byte(widgetProj), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := n.ManifestVersion(dir); err == nil || !strings.Contains(err.Error(), "A.csproj, B.fsproj") {
		t.Errorf("several project files must be refused by name: %v", err)
	}
}

func TestNugetPublish(t *testing.T) {
	dir := tempWith(t, "Widget.csproj", strings.Replace(widgetProj, "1.0.0", "1.1.0", 1))
	var out string
	r := &captureRunner{respond: func(_, _ string, args []string) (string, error) {
		if args[0] == "pack" {
			out = args[len(args)-1]
			for _, f := range []string{"Martin.Widget.1.1.0.nupkg", "Martin.Widget.1.1.0.snupkg", "Martin.Widget.1.1.0.symbols.nupkg"} {
				if err := os.WriteFile(filepath.Join(out, f), nil, 0o644); err != nil {
					return "", err
				}
			}
		}
		return "", nil
	}}
	n, _ := New("nuget", r)
	if err := n.Publish(dir); err != nil {
		t.Fatal(err)
	}
	proj := filepath.Join(dir, "Widget.csproj")
	want := []string{
		"dotnet pack " + proj + " --configuration Release --output " + out,
		"dotnet nuget push " + filepath.Join(out, "Martin.Widget.1.1.0.nupkg") + " --skip-duplicate",
	}
	if got := r.argvs(); strings.Join(got, " | ") != strings.Join(want, " | ") {
		t.Errorf("argv = %v, want %v", got, want)
	}
	if _, err := os.Stat(out); !os.IsNotExist(err) {
		t.Errorf("the pack directory must be removed: %v", err)
	}

	n, _ = New("nuget", &captureRunner{})
	if err := n.Publish(dir); err == nil || !strings.Contains(err.Error(), "no .nupkg") {
		t.Errorf("a pack that produced nothing must fail: %v", err)
	}
}

// TestNugetPublishRefusesAnotherVersion covers a version set during the
// build: nothing is pushed when the package is not the released version.
func TestNugetPublishRefusesAnotherVersion(t *testing.T) {
	dir := tempWith(t, "Widget.csproj", strings.Replace(widgetProj, "1.0.0", "1.1.0", 1))
	r := &captureRunner{respond: func(_, _ string, args []string) (string, error) {
		if args[0] == "pack" {
			out := args[len(args)-1]
			for _, f := range []string{"Martin.Widget.1.1.0.nupkg", "Martin.Widget.1.1.1-alpha.0.5.nupkg"} {
				if err := os.WriteFile(filepath.Join(out, f), nil, 0o644); err != nil {
					return "", err
				}
			}
		}
		return "", nil
	}}
	n, _ := New("nuget", r)
	err := n.Publish(dir)
	if err == nil || !strings.Contains(err.Error(), "1.1.1-alpha.0.5") {
		t.Fatalf("a package at another version must be refused: %v", err)
	}
	for _, argv := range r.argvs() {
		if strings.Contains(argv, "nuget push") {
			t.Errorf("nothing may be pushed: %v", r.argvs())
		}
	}
}

func TestCheckPackedVersion(t *testing.T) {
	v := semver.MustParse("2.0.0-RC.1+build.7")
	if err := checkPackedVersion([]string{"/out/Martin.Widget.win-x64.2.0.0-rc.1.nupkg"}, v); err != nil {
		t.Errorf("build metadata and case are not part of the file name: %v", err)
	}
	if err := checkPackedVersion([]string{"/out/Martin.Widget.12.0.0-rc.1.nupkg"}, v); err == nil {
		t.Error("a version that only ends the same way must be refused")
	}
}

func TestNugetRecoveryAndLock(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "src", "Widget")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "Widget.csproj"), []byte(widgetProj), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "packages.lock.json"), []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	r := &captureRunner{}
	n, _ := New("nuget", r)
	want := `cd src/Widget && dotnet pack Widget.csproj --configuration Release --output nupkg && ` +
		`dotnet nuget push "nupkg/*.nupkg" --skip-duplicate`
	if got := n.Recovery(dir, root); got != want {
		t.Errorf("Recovery = %q", got)
	}
	locks, err := n.RefreshLock(dir, root)
	if err != nil || len(locks) != 0 || len(r.calls) != 0 {
		t.Errorf("packages.lock.json carries no project version: locks=%v calls=%v err=%v", locks, r.argvs(), err)
	}
	if got := n.Manifest(dir, root); got != "src/Widget/Widget.csproj" {
		t.Errorf("Manifest = %q", got)
	}
}
