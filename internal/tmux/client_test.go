package tmux

import (
	"errors"
	"strings"
	"testing"
)

func TestArgumentsPrependSocketSelection(t *testing.T) {
	client := NewClient("/usr/bin/tmux", "test-socket", nil)
	arguments := client.Arguments("list-sessions", "-F", "#{session_name}")
	expected := []string{"-L", "test-socket", "list-sessions", "-F", "#{session_name}"}
	if len(arguments) != len(expected) {
		t.Fatalf("arguments = %v, want %v", arguments, expected)
	}
	for i := range expected {
		if arguments[i] != expected[i] {
			t.Fatalf("arguments = %v, want %v", arguments, expected)
		}
	}
}

func TestEnvironmentStripsOnlyTMUX(t *testing.T) {
	client := NewClient("/usr/bin/tmux", "test-socket", func() []string {
		return []string{"TMUX=/private/tmp/tmux-1/default,123,0", "PATH=/usr/bin", "TMUX_PANE=%1"}
	})
	environment := client.Environment()
	joined := strings.Join(environment, "\n")
	if strings.Contains(joined, "TMUX=") && !strings.Contains(joined, "TMUX_PANE=") {
		t.Fatalf("environment still contains TMUX: %v", environment)
	}
	for _, entry := range environment {
		if strings.HasPrefix(entry, "TMUX=") {
			t.Fatalf("environment still contains TMUX: %v", environment)
		}
	}
	if !strings.Contains(joined, "PATH=/usr/bin") {
		t.Fatalf("environment lost PATH: %v", environment)
	}
	if !strings.Contains(joined, "TMUX_PANE=%1") {
		t.Fatalf("environment lost TMUX_PANE (only TMUX itself is stripped): %v", environment)
	}
}

func TestCommandUsesPathSocketAndEnvironment(t *testing.T) {
	client := NewClient("/test/tmux", "sock", func() []string {
		return []string{"TMUX=nested", "HOME=/home/x"}
	})
	command := client.Command("has-session", "-t", "=name")
	if command.Path != "/test/tmux" {
		t.Fatalf("command path = %q, want %q", command.Path, "/test/tmux")
	}
	wantArgs := []string{"/test/tmux", "-L", "sock", "has-session", "-t", "=name"}
	if strings.Join(command.Args, " ") != strings.Join(wantArgs, " ") {
		t.Fatalf("command args = %v, want %v", command.Args, wantArgs)
	}
	for _, entry := range command.Env {
		if strings.HasPrefix(entry, "TMUX=") {
			t.Fatalf("command environment nests the parent session: %v", command.Env)
		}
	}
}

func TestExactTargets(t *testing.T) {
	if got := ExactSessionTarget("name"); got != "=name" {
		t.Fatalf("ExactSessionTarget = %q", got)
	}
	if got := ExactCurrentWindowTarget("name"); got != "=name:" {
		t.Fatalf("ExactCurrentWindowTarget = %q", got)
	}
	if got := ExactWindowTarget("name", 3); got != "=name:3" {
		t.Fatalf("ExactWindowTarget = %q", got)
	}
}

func TestParseWindowTarget(t *testing.T) {
	target, err := ParseWindowTarget([]byte("2\t@5\t4242\n"))
	if err != nil {
		t.Fatal(err)
	}
	if target.Index != 2 || target.ID != "@5" || target.ServerPID != "4242" {
		t.Fatalf("target = %#v", target)
	}
	for _, invalid := range []string{"", "1\t@5", "x\t@5\t42", "1\t\t42", "1\t@5\t0", "1\t@5\t-3", "1\t@5\tnope"} {
		if _, err := ParseWindowTarget([]byte(invalid)); err == nil {
			t.Fatalf("ParseWindowTarget(%q) succeeded, want error", invalid)
		}
	}
}

func TestCommandError(t *testing.T) {
	base := errors.New("exit status 1")
	if err := CommandError("kill session", []byte("  can't find session  \n"), base); err.Error() != "kill session: can't find session" {
		t.Fatalf("error = %q", err)
	}
	err := CommandError("kill session", nil, base)
	if !errors.Is(err, base) {
		t.Fatalf("error does not wrap the exec error: %v", err)
	}
	if err.Error() != "kill session: exit status 1" {
		t.Fatalf("error = %q", err)
	}
}

func TestShellQuoting(t *testing.T) {
	if got := ShellQuote("plain"); got != "'plain'" {
		t.Fatalf("ShellQuote = %q", got)
	}
	if got := ShellQuote("it's"); got != `'it'"'"'s'` {
		t.Fatalf("ShellQuote = %q", got)
	}
	if got := ShellCommand("/bin/sh", []string{"-c", "echo 'hi'"}); got != `'/bin/sh' '-c' 'echo '"'"'hi'"'"''` {
		t.Fatalf("ShellCommand = %q", got)
	}
}
