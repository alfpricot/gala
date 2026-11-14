package manager

import (
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestHelperProcess is not a test: the hook runner tests re-run the test
// binary as the command they launch, which works on every platform. HELPER_MODE
// is not a GALA_ name, so the runner's environment filtering leaves it alone.
func TestHelperProcess(t *testing.T) {
	switch os.Getenv("HELPER_MODE") {
	case "":
		return
	case "env":
		for _, k := range []string{"GITHUB_TOKEN", "GH_TOKEN", "GALA_VERSION", "GALA_TAG", "KEEP_ME"} {
			fmt.Printf("%s=%s\n", k, os.Getenv(k))
		}
	case "stream":
		fmt.Println("first")
		time.Sleep(400 * time.Millisecond)
		fmt.Println("second")
	case "slow":
		fmt.Println("started")
		time.Sleep(time.Minute)
	case "fail":
		fmt.Println("about to fail")
		os.Exit(3)
	}
	os.Exit(0)
}

func helperSpec(mode string, extra ...string) HookSpec {
	return HookSpec{
		Name: os.Args[0],
		Args: []string{"-test.run=^TestHelperProcess$"},
		Env:  append([]string{"HELPER_MODE=" + mode}, extra...),
	}
}

func TestHookEnviron(t *testing.T) {
	got := HookEnviron([]string{
		"PATH=/bin", "GITHUB_TOKEN=a", "gh_token=b", "GH_TOKEN=c",
		"GALA_VERSION=0.3.1", "gala_tag=x", "KEEP_ME=yes", "GALAXY=stays",
	}, []string{"GALA_TAGS=v1.0.0"})
	want := []string{"PATH=/bin", "KEEP_ME=yes", "GALAXY=stays", "GALA_TAGS=v1.0.0"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("HookEnviron = %v, want %v", got, want)
	}
}

// TestRunHookEnvironment: the tokens and stale GALA_* variables of the parent
// process never reach a hook, while the variables gala sets and any other
// inherited variable do.
func TestRunHookEnvironment(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "secret-github")
	t.Setenv("GH_TOKEN", "secret-gh")
	t.Setenv("GALA_VERSION", "0.3.1-from-the-parent")
	t.Setenv("KEEP_ME", "inherited")

	out, err := ExecRunner{}.RunHook(helperSpec("env", "GALA_TAG=v1.2.3"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"GITHUB_TOKEN=\n", "GH_TOKEN=\n", "GALA_VERSION=\n", "GALA_TAG=v1.2.3\n", "KEEP_ME=inherited\n"} {
		if !strings.Contains(out, want) {
			t.Errorf("hook environment missing %q in:\n%s", want, out)
		}
	}
	if strings.Contains(out, "secret") || strings.Contains(out, "from-the-parent") {
		t.Errorf("a token or stale variable reached the hook:\n%s", out)
	}
}

// timedWriter records writes with their arrival times.
type timedWriter struct {
	mu     sync.Mutex
	lines  []string
	arrive []time.Time
}

func (w *timedWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.lines = append(w.lines, string(p))
	w.arrive = append(w.arrive, time.Now())
	return len(p), nil
}

// TestRunHookStreamsOutput: the first line reaches the writer well before the
// command exits, and a failing command's error leaves the output out because
// it was already shown.
func TestRunHookStreamsOutput(t *testing.T) {
	w := &timedWriter{}
	spec := helperSpec("stream")
	spec.Out = w
	start := time.Now()
	if _, err := (ExecRunner{}).RunHook(spec); err != nil {
		t.Fatal(err)
	}
	total := time.Since(start)
	if len(w.arrive) == 0 {
		t.Fatal("no output was streamed")
	}
	if first := w.arrive[0].Sub(start); first > total-250*time.Millisecond {
		t.Errorf("the first output arrived after %s of a %s run; it must stream while the command runs", first, total)
	}

	w2 := &timedWriter{}
	failing := helperSpec("fail")
	failing.Out = w2
	_, err := ExecRunner{}.RunHook(failing)
	if err == nil {
		t.Fatal("a failing hook must return an error")
	}
	if strings.Contains(err.Error(), "about to fail") {
		t.Errorf("a streamed hook's output must not be repeated in the error: %v", err)
	}
	if !strings.Contains(strings.Join(w2.lines, ""), "about to fail") {
		t.Errorf("the output must reach the writer, got %q", w2.lines)
	}

	// Without a writer the output is captured and quoted in the error.
	_, err = ExecRunner{}.RunHook(helperSpec("fail"))
	if err == nil || !strings.Contains(err.Error(), "about to fail") {
		t.Errorf("an unstreamed hook's error must carry its output: %v", err)
	}
}

func TestRunHookTimeout(t *testing.T) {
	spec := helperSpec("slow")
	spec.Timeout = 400 * time.Millisecond
	start := time.Now()
	_, err := ExecRunner{}.RunHook(spec)
	if err == nil {
		t.Fatal("a hook that outlives its timeout must fail")
	}
	if !strings.Contains(err.Error(), "timed out after 400ms") {
		t.Errorf("the error must say the hook timed out: %v", err)
	}
	if elapsed := time.Since(start); elapsed > 20*time.Second {
		t.Errorf("the hook was not stopped promptly: %s", elapsed)
	}
}
