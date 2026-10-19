package cli

import (
	"bytes"
	"strings"
	"testing"
)

func TestRunNoArgsShowsUsage(t *testing.T) {
	var out, errOut bytes.Buffer
	if code := Run(nil, &out, &errOut); code != 2 {
		t.Errorf("exit = %d, want 2", code)
	}
	if !strings.Contains(errOut.String(), "USAGE") {
		t.Errorf("usage missing: %q", errOut.String())
	}
}

func TestRunHelpExitsZero(t *testing.T) {
	var out, errOut bytes.Buffer
	if code := Run([]string{"--help"}, &out, &errOut); code != 0 {
		t.Errorf("exit = %d", code)
	}
	if !strings.Contains(out.String(), "sommelier") {
		t.Error("help text missing")
	}
}

func TestRunVersion(t *testing.T) {
	var out, errOut bytes.Buffer
	if code := Run([]string{"version"}, &out, &errOut); code != 0 {
		t.Errorf("exit = %d", code)
	}
	if !strings.Contains(out.String(), Version) {
		t.Errorf("version missing: %q", out.String())
	}
}

func TestRunUnknownCommandIsTreatedAsQuery(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("SOMMELIER_NO_LLM", "1")

	var out, errOut bytes.Buffer
	// No config exists, so the first-run wizard runs instead of a query.
	if code := Run([]string{"romance"}, &out, &errOut); code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, errOut.String())
	}
	if !strings.Contains(out.String(), "setup") {
		t.Errorf("expected the wizard: %q", out.String())
	}
}

func TestWrapBreaksOnWords(t *testing.T) {
	got := wrap("one two three four five six", 12, "> ")
	for _, line := range strings.Split(got, "\n") {
		if len(line) > 14 {
			t.Errorf("line too long: %q", line)
		}
	}
	if !strings.HasPrefix(got, "> one") {
		t.Errorf("missing prefix: %q", got)
	}
}

func TestWrapHandlesEmpty(t *testing.T) {
	if got := wrap("", 10, "> "); got != "> " {
		t.Errorf("got %q", got)
	}
}

func TestLimit(t *testing.T) {
	values := []string{"a", "b", "c"}
	if got := limit(values, 2); len(got) != 2 {
		t.Errorf("got %v", got)
	}
	if got := limit(values, 10); len(got) != 3 {
		t.Errorf("got %v", got)
	}
}

func TestModelLabel(t *testing.T) {
	if modelLabel(true) != "model" || modelLabel(false) != "heuristic" {
		t.Error("labels wrong")
	}
}

func TestMultiFlagAccumulates(t *testing.T) {
	var m multiFlag
	if err := m.Set("a"); err != nil {
		t.Fatal(err)
	}
	if err := m.Set("b"); err != nil {
		t.Fatal(err)
	}
	if m.String() != "a,b" {
		t.Errorf("got %q", m.String())
	}
}

func TestOrDash(t *testing.T) {
	if orDash("") != "-" || orDash("x") != "x" {
		t.Error("orDash wrong")
	}
}

func TestHasFlag(t *testing.T) {
	if !hasFlag([]string{"a", "--no-setup"}, "--no-setup") {
		t.Error("flag not found")
	}
	if hasFlag([]string{"a"}, "--no-setup") {
		t.Error("false positive")
	}
}
