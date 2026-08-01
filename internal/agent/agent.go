// Package agent defines the pluggable coding-agent contract: how an agent
// identifies itself, which launch options it accepts, and how it builds its
// terminal launch command. Each supported agent implements Agent in its own
// file; everything else in the backend works against this interface and the
// Registry instead of switching on agent names.
package agent

import (
	"errors"
	"strings"

	"github.com/dire-kiwi/kiwi-code/internal/project"
)

// Well-known agent IDs and the prefixes of settings-derived Claude profile
// agents. These strings appear in API requests and tmux pane metadata; they
// are stable identifiers.
const (
	IDPi        = "pi"
	IDCodex     = "codex"
	IDClaude    = "claude"
	IDClaudeGPT = "claude-gpt"

	ClaudeProfilePrefix    = "claude-profile-"
	ClaudeGPTProfilePrefix = "claude-gpt-profile-"

	MaxProfileAgentIDLength = 64
)

// LaunchOptions is the flat option set a launch request may carry. Agents
// validate and translate the parts they understand.
type LaunchOptions struct {
	Model                 string
	ThinkingLevel         string
	InitialPrompt         string
	AppendSystemPrompt    string
	AllowPendingCreation  bool
	BrowserThreadEndpoint string
	// FigmaMCPURL is set for projects with Figma MCP support enabled. Empty
	// means the agent launches without the Figma MCP server.
	FigmaMCPURL string
}

// LaunchContext carries everything an agent may need to build its command.
// The runtime assembles it; agents never reach back into server state.
type LaunchContext struct {
	ProjectID      string
	ThreadID       string
	ThreadEndpoint string
	SessionName    string
	WindowName     string
	// FigmaMCPURL is the resolved per-project Figma bridge URL ("" when the
	// project has Figma MCP disabled).
	FigmaMCPURL string
	// RelatedDirectories resolves the project's related directories for
	// --add-dir style flags. Lazy so agents that don't need it never pay for
	// (or fail on) resolution.
	RelatedDirectories func() ([]string, error)
}

// Command describes an agent's terminal launch. The runtime assembles the
// final argv as:
//
//	env [-u Unset]... KIWI_CODE_TMUX_* [thread env] Env... Program Prefix... BaseArgs... Suffix...
//
// Order within each slice is meaningful and preserved exactly.
type Command struct {
	// Program is the resolved agent binary, or the fallback shell when the
	// binary is missing (Notice is set in that case).
	Program  string
	BaseArgs []string
	// Notice is user-facing terminal output explaining a fallback shell.
	// When set, agent-specific arguments and environment are omitted, same
	// as launching the plain tool.
	Notice string
	// Prefix holds agent arguments that must precede BaseArgs.
	Prefix []string
	// Suffix holds option-derived arguments (model, thinking level, initial
	// prompt) appended after everything else. The initial prompt must stay
	// positional and last.
	Suffix []string
	// Env holds KEY=VALUE additions appended after the generic thread
	// environment.
	Env []string
	// Unset names environment variables removed via `env -u` ahead of every
	// assignment.
	Unset []string
}

// Agent is one pluggable coding agent.
type Agent interface {
	// ID returns the agent's stable identifier.
	ID() string
	// TerminalCommand builds the tmux-pane launch command.
	TerminalCommand(lc LaunchContext, opts LaunchOptions) (Command, error)
}

// Identity helpers shared by the registry, HTTP validation, and pane
// metadata handling.

func validProfileAgentWithPrefix(agent, prefix string) bool {
	profileID := strings.TrimPrefix(agent, prefix)
	if profileID == agent || profileID == "" || len(profileID) > MaxProfileAgentIDLength {
		return false
	}
	for _, character := range profileID {
		if (character >= 'a' && character <= 'z') ||
			(character >= 'A' && character <= 'Z') ||
			(character >= '0' && character <= '9') || character == '-' || character == '_' {
			continue
		}
		return false
	}
	return true
}

// ValidClaudeProfileID reports whether id names a (possibly unconfigured)
// Claude profile agent.
func ValidClaudeProfileID(id string) bool {
	return validProfileAgentWithPrefix(id, ClaudeProfilePrefix)
}

// ValidClaudeGPTProfileID reports whether id names a Claude-GPT profile agent.
func ValidClaudeGPTProfileID(id string) bool {
	return validProfileAgentWithPrefix(id, ClaudeGPTProfilePrefix)
}

// ValidConfiguredClaudeID reports whether id has profile-agent shape.
func ValidConfiguredClaudeID(id string) bool {
	return ValidClaudeProfileID(id) || ValidClaudeGPTProfileID(id)
}

// IsClaudeGPT reports whether id is the claude-gpt agent or one of its
// profiles.
func IsClaudeGPT(id string) bool {
	return id == IDClaudeGPT || ValidClaudeGPTProfileID(id)
}

// IsClaude reports whether id is any member of the Claude family.
func IsClaude(id string) bool {
	return id == IDClaude || IsClaudeGPT(id) || ValidClaudeProfileID(id)
}

// IsTerminalAgent reports whether id is an agent that runs in a tmux pane.
func IsTerminalAgent(id string) bool {
	return id == IDPi || id == IDCodex || IsClaude(id)
}

// ProfileAgentID derives the public agent ID for a configured Claude-family
// setting.
func ProfileAgentID(setting project.CodingAgentSetting) string {
	if setting.Kind == project.CodingAgentKindClaudeGPT {
		return ClaudeGPTProfilePrefix + setting.ID
	}
	return ClaudeProfilePrefix + setting.ID
}

// Registry resolves agent IDs to implementations. The built-in agents are
// fixed; Claude profile agents are derived from live settings at resolve
// time so settings edits apply without a restart.
type Registry struct {
	pi       Agent
	codex    Agent
	claude   ClaudeFactory
	settings func() project.Settings
}

// ClaudeFactory builds a Claude-family agent. profile carries the configured
// setting when configured is true; gpt selects the CLIProxyAPI variant.
type ClaudeFactory func(id string, profile project.CodingAgentSetting, configured, gpt bool) Agent

func NewRegistry(pi, codex Agent, claude ClaudeFactory, settings func() project.Settings) *Registry {
	return &Registry{pi: pi, codex: codex, claude: claude, settings: settings}
}

// ErrNotConfigured reports a profile-shaped agent ID with no matching
// settings entry. The message is part of observable API error text.
var ErrNotConfigured = errors.New("Claude Code agent is not configured")

// Resolve returns the agent for id, or ok=false when id does not name a
// coding agent at all (plain terminal tools, unknown strings). A
// valid-shaped but unconfigured profile ID resolves to an agent whose
// TerminalCommand fails with ErrNotConfigured, matching historic behavior.
func (r *Registry) Resolve(id string) (Agent, bool) {
	switch {
	case id == IDPi:
		return r.pi, true
	case id == IDCodex:
		return r.codex, true
	case id == IDClaude:
		return r.claude(id, project.CodingAgentSetting{}, true, false), true
	case id == IDClaudeGPT:
		return r.claude(id, project.CodingAgentSetting{}, true, true), true
	case ValidConfiguredClaudeID(id):
		gpt := ValidClaudeGPTProfileID(id)
		if r.settings != nil {
			for _, configured := range r.settings().CodingAgents {
				if (configured.Kind == project.CodingAgentKindClaude || configured.Kind == project.CodingAgentKindClaudeGPT) &&
					ProfileAgentID(configured) == id {
					return r.claude(id, configured, true, gpt), true
				}
			}
		}
		return r.claude(id, project.CodingAgentSetting{}, false, gpt), true
	default:
		return nil, false
	}
}

// ActivityRoute describes an agent-named activity endpoint.
type ActivityRoute struct {
	Segment string // path segment in the route (frozen; see api_routes.txt)
	AgentID string // agent recorded in the activity tracker
	Label   string // human label used in error text
}

// ActivityRoutes lists the agents that report working-state over their own
// activity endpoint. Registering routes from this list means adding an agent
// updates the route table without touching HTTP code.
func ActivityRoutes() []ActivityRoute {
	return []ActivityRoute{
		{Segment: IDPi, AgentID: IDPi, Label: "Pi"},
		{Segment: IDCodex, AgentID: IDCodex, Label: "Codex"},
		{Segment: IDClaude, AgentID: IDClaude, Label: "Claude"},
	}
}

// ContextStatusSources lists the sources allowed to report context-window
// usage for a thread.
func ContextStatusSources() []string {
	return []string{"pi-terminal", "pi-native"}
}
