package tmux

import "strings"

// ShellCommand renders a command and its arguments as a single-quoted POSIX
// shell command line, for use inside `sh -c` strings passed to tmux.
func ShellCommand(command string, args []string) string {
	parts := make([]string, 0, len(args)+1)
	parts = append(parts, ShellQuote(command))
	for _, arg := range args {
		parts = append(parts, ShellQuote(arg))
	}
	return strings.Join(parts, " ")
}

// ShellQuote single-quotes a value for a POSIX shell, escaping embedded
// single quotes.
func ShellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}
