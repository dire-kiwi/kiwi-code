package server

import (
	"strings"
	"testing"
)

func TestNormalizeProcessName(t *testing.T) {
	if got, err := normalizeProcessName("  web server  "); err != nil || got != "web server" {
		t.Fatalf("normalizeProcessName() = %q, %v", got, err)
	}
	for _, invalid := range []string{"", "bad\nname"} {
		if _, err := normalizeProcessName(invalid); err == nil {
			t.Fatalf("normalizeProcessName(%q) did not fail", invalid)
		}
	}
}

func TestParseProcessWindowsFiltersFixedTools(t *testing.T) {
	output := []byte(
		"0\t@1\tpi\tpi\t\tpi\t421\n" +
			"1\t@2\tweb\tprocess\tabc123\tnode\t421\n" +
			"2\t@3\tlegacy-process\tprocess\t\tzsh\t421\n",
	)
	windows, err := parseProcessWindows(output)
	if err != nil {
		t.Fatal(err)
	}
	if len(windows) != 1 {
		t.Fatalf("process windows = %#v", windows)
	}
	window := windows[0]
	if window.ID != "abc123" || window.TmuxID != "@2" || window.TmuxServerPID != "421" || window.Index != 1 || window.Name != "web" || window.CurrentCommand != "node" {
		t.Fatalf("unexpected process window: %#v", window)
	}
}

func TestParseProcessWindowsRequiresServerPIDColumn(t *testing.T) {
	if _, err := parseProcessWindows([]byte("1\t@2\tweb\tprocess\tabc123\tnode\n")); err == nil {
		t.Fatal("expected process metadata without a tmux server PID column to fail")
	}
	if _, err := parseProcessWindows([]byte("1\t@2\tweb\tprocess\tabc123\tnode\tbogus\n")); err == nil {
		t.Fatal("expected invalid tmux server PID metadata to fail")
	}
}

func TestTmuxProcessIncarnationConditionRequiresCapturedIdentity(t *testing.T) {
	target := tmuxWindowTarget{ID: "@12", ServerPID: "456", ProcessID: "abc123"}
	condition, err := tmuxProcessIncarnationCondition(target)
	if err != nil {
		t.Fatal(err)
	}
	for _, identity := range []string{"#{pid},456", "#{window_id},@12", "#{@dire-mux-process-id},abc123"} {
		if !strings.Contains(condition, identity) {
			t.Fatalf("process condition %q does not contain %q", condition, identity)
		}
	}

	for _, invalid := range []tmuxWindowTarget{
		{ID: "@12", ServerPID: "", ProcessID: "abc123"},
		{ID: "12", ServerPID: "456", ProcessID: "abc123"},
		{ID: "@12", ServerPID: "456", ProcessID: ""},
		{ID: "@12", ServerPID: "456", ProcessID: "unsafe,format"},
	} {
		if _, err := tmuxProcessIncarnationCondition(invalid); err == nil {
			t.Fatalf("tmuxProcessIncarnationCondition(%#v) did not fail", invalid)
		}
	}
}
