package semver

import "testing"

func TestParse(t *testing.T) {
	valid := []struct {
		in   string
		want Version
	}{
		{"1.2.3", Version{Major: 1, Minor: 2, Patch: 3}},
		{"v1.2.3", Version{Major: 1, Minor: 2, Patch: 3}},
		{"0.0.0", Version{}},
		{"10.20.30", Version{Major: 10, Minor: 20, Patch: 30}},
		{"1.2.3-rc.1", Version{Major: 1, Minor: 2, Patch: 3, Prerelease: []string{"rc", "1"}}},
		{"1.2.3-alpha-1", Version{Major: 1, Minor: 2, Patch: 3, Prerelease: []string{"alpha-1"}}},
		{"1.2.3-0", Version{Major: 1, Minor: 2, Patch: 3, Prerelease: []string{"0"}}},
		{"1.2.3+build.5", Version{Major: 1, Minor: 2, Patch: 3, Build: "build.5"}},
	}
	for _, tc := range valid {
		got, err := Parse(tc.in)
		if err != nil {
			t.Errorf("Parse(%q) error: %v", tc.in, err)
			continue
		}
		if Compare(got, tc.want) != 0 || len(got.Prerelease) != len(tc.want.Prerelease) {
			t.Errorf("Parse(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}

	invalid := []string{
		"", "v", "1", "1.2", "1.2.3.4", "01.2.3", "1.02.3", "a.b.c",
		"1.2.3-", "1.2.3-rc..1", "1.2.3-rc.01", "1.2.3-rc.1+", "x1.2.3",
	}
	for _, in := range invalid {
		if v, err := Parse(in); err == nil {
			t.Errorf("Parse(%q) = %v, want error", in, v)
		}
	}
}

func TestString(t *testing.T) {
	cases := map[string]string{
		"1.2.3":       "1.2.3",
		"v1.2.3-rc.1": "1.2.3-rc.1",
		"0.0.0":       "0.0.0",
		"1.2.3+meta":  "1.2.3+meta",
	}
	for in, want := range cases {
		v, err := Parse(in)
		if err != nil {
			t.Fatalf("Parse(%q): %v", in, err)
		}
		if got := v.String(); got != want {
			t.Errorf("Parse(%q).String() = %q, want %q", in, got, want)
		}
	}
}

// TestCompareOrder walks semver.org's worked example end to end.
func TestCompareOrder(t *testing.T) {
	order := []string{
		"1.0.0-alpha", "1.0.0-alpha.1", "1.0.0-alpha.beta", "1.0.0-beta",
		"1.0.0-beta.2", "1.0.0-beta.11", "1.0.0-rc.1", "1.0.0",
		"1.0.1", "1.1.0", "2.0.0", "10.0.0",
	}
	for i := 0; i < len(order)-1; i++ {
		a := MustParse(order[i])
		b := MustParse(order[i+1])
		if Compare(a, b) >= 0 {
			t.Errorf("Compare(%s, %s) = %d, want < 0", order[i], order[i+1], Compare(a, b))
		}
		if Compare(b, a) <= 0 {
			t.Errorf("Compare(%s, %s) = %d, want > 0", order[i+1], order[i], Compare(b, a))
		}
	}
	if Compare(MustParse("1.2.3"), MustParse("1.2.3")) != 0 {
		t.Error("equal versions must compare 0")
	}
}

func TestStable(t *testing.T) {
	if !MustParse("1.0.0").Stable() {
		t.Error("1.0.0 should be stable")
	}
	if MustParse("1.0.0-rc.1").Stable() {
		t.Error("1.0.0-rc.1 should not be stable")
	}
	if MustParse("1.0.0+build").Stable() != true {
		t.Error("build metadata does not affect stability")
	}
}

func TestBump(t *testing.T) {
	cases := []struct {
		in   string
		b    Bump
		want string
	}{
		{"1.2.3", Patch, "1.2.4"},
		{"1.2.3", Minor, "1.3.0"},
		{"1.2.3", Major, "2.0.0"},
		{"0.7.0", Major, "1.0.0"}, // no 0.x special-casing
		{"1.2.3", None, "1.2.3"},
		{"1.2.3-rc.1", Minor, "1.3.0"}, // a prerelease never survives a bump
	}
	for _, tc := range cases {
		got := MustParse(tc.in).Bump(tc.b).String()
		if got != tc.want {
			t.Errorf("%s.Bump(%s) = %s, want %s", tc.in, tc.b, got, tc.want)
		}
	}
}

func TestBumpStringAndMax(t *testing.T) {
	if Major.String() != "major" || Minor.String() != "minor" || Patch.String() != "patch" || None.String() != "none" {
		t.Error("bump names are wrong")
	}
	if Max(None, Minor) != Minor || Max(Major, Patch) != Major || Max(None, None) != None {
		t.Error("Max is wrong")
	}
}
