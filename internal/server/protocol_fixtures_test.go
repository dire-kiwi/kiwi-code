package server

import (
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/dire-kiwi/kiwi-code/internal/project"
	"github.com/dire-kiwi/kiwi-code/internal/wire"
)

var updateProtocolFixture = flag.Bool(
	"update-protocol-fixture",
	false,
	"rewrite web/src/wire/__fixtures__/protocol.json from Go domain structs",
)

func TestProtocolFixtureIsCompleteAndCurrent(t *testing.T) {
	topics := buildStateTopicFixtures(t)
	registry := (&Server{}).stateTopicRegistry(nil)
	if len(topics) != len(registry) {
		t.Fatalf("fixture has %d topics, registry has %d", len(topics), len(registry))
	}
	for tag := range registry {
		if _, exists := topics[tag]; !exists {
			t.Errorf("registered topic %q has no fixture", tag)
		}
	}
	for tag := range topics {
		if _, exists := registry[tag]; !exists {
			t.Errorf("fixture topic %q is not registered", tag)
		}
	}

	expected, err := wire.EncodeProtocolFixture(topics)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join("..", "..", "web", "src", "wire", "__fixtures__", "protocol.json")
	if *updateProtocolFixture {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, expected, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	actual, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read protocol fixture (run go test ./internal/server -run TestProtocolFixtureIsCompleteAndCurrent -update-protocol-fixture to create it): %v", err)
	}
	if !bytes.Equal(actual, expected) {
		t.Fatal("protocol fixture is stale; rerun go test ./internal/server -run TestProtocolFixtureIsCompleteAndCurrent -update-protocol-fixture")
	}
}

func buildStateTopicFixtures(t *testing.T) map[string]json.RawMessage {
	t.Helper()
	fixtureTime := time.Date(2026, time.July, 26, 12, 34, 56, 0, time.UTC)
	usageTotals := threadUsageTotals{
		InputTokens: 1, OutputTokens: 2, CacheReadTokens: 3, CacheWriteTokens: 4,
		TotalTokens: 10, CostUSD: 0.01,
	}
	falseValue := false
	trueValue := true
	contextTokens := int64(32_000)
	contextPercent := float64(25)
	currentTargetID := "page-fixture"
	startedAt := fixtureTime.Add(-time.Minute)
	scheduledDeletionAt := fixtureTime.Add(24 * time.Hour)
	defaultSandbox := defaultEffectiveSandboxConfig()
	defaultTheme := project.DefaultTheme()

	values := map[string]any{
		stateTopicProjects: []project.Project{{
			ID:        "project-1",
			Name:      "Fixture project",
			Path:      "/workspace/fixture",
			ProfileID: project.PersonalProfileID,
			Host:      "fixture-host",
			IsGitRepo: true,
			CreatedAt: fixtureTime,
			Threads: []project.Thread{{
				ID: "thread-1", Title: "Fixture thread", Cwd: "/workspace/fixture", CreatedAt: fixtureTime,
			}},
			WorktreeBranchPrefix: project.DefaultWorktreeBranchPrefix,
			Environment: project.LocalEnvironment{
				Name:      "Local",
				Variables: []project.EnvironmentVariable{},
				Actions:   []project.EnvironmentAction{},
			},
		}},
		stateTopicProfiles: []project.Profile{{
			ID: project.PersonalProfileID, Name: "Personal",
		}},
		stateTopicAgentActivity: []piThreadActivity{{
			ProjectID: "project-1", ThreadID: "thread-1", State: piActivityWorking, UpdatedAt: fixtureTime,
		}},
		stateTopicThreadUsage: []threadUsageSnapshot{{
			ProjectID: "project-1", ThreadID: "thread-1",
			Own: usageTotals, Children: threadUsageTotals{}, Total: usageTotals,
			LimitReached: false,
		}},
		stateTopicThreadStatus: threadStatusSnapshot{
			GitBranches: &gitBranchState{
				IsRepository: true, Current: "main",
				Branches: []gitBranch{{Name: "main", Current: true}},
			},
			ContextStatuses: map[string]agentContextStatus{
				contextStatusSourcePiNative: {
					Source: contextStatusSourcePiNative, Tokens: &contextTokens,
					ContextWindow: 128_000, Percent: &contextPercent,
					Model: "openai/gpt-5", UpdatedAt: fixtureTime,
				},
			},
			Processes: []processWindow{{
				ID: "process-1", Index: 1, Name: "Vite", CurrentCommand: "node",
			}},
			ShellWindows: []tmuxWindow{{
				Index: 0, Name: "shell", Active: true,
			}},
			Workflows: []workflowRunSnapshot{{
				ID: "workflow-1", ProjectID: "project-1", ThreadID: "thread-1",
				State: "running", Attempt: 1, Name: "Fixture workflow",
				Description: "Exercises the workflow snapshot schema.",
				WhenToUse:   "During protocol compatibility tests.",
				Phases: []workflowPhase{{
					Title: "Implement", Detail: "Build the fixture.", Model: "openai/gpt-5",
				}},
				CurrentPhase: "Implement", ScriptPath: "/workspace/workflow.mjs",
				ProcessID: "process-1", CreatedAt: startedAt, StartedAt: &startedAt,
				UpdatedAt: fixtureTime, Result: json.RawMessage(`{"progress":0.5}`),
				Logs: []workflowLogEntry{{
					Message: "Workflow started.", CreatedAt: startedAt,
				}},
				Agents: []workflowAgentSnapshot{{
					ID: "agent-1", Label: "Implementer", Phase: "Implement",
					State: "working", ThreadID: "child-thread-1", ChildRunID: 7,
					StartedAt: startedAt, Value: json.RawMessage(`{"status":"working"}`),
				}},
			}},
			Plans: []threadPlanSnapshot{{
				ID: "plan-1", ProjectID: "project-1", ThreadID: "thread-1",
				SourceThreadID: "thread-1", Title: "Fixture plan",
				CreatedAt: fixtureTime, SizeBytes: 2048,
			}},
			Errors: threadStatusErrors{
				GitBranches: "Fixture error shape.",
			},
		},
		stateTopicSettings: project.Settings{
			WorktreeBasePath:              "/workspace/worktrees",
			DefaultWorktreeBasePath:       "/workspace/worktrees",
			UsingDefault:                  true,
			ArchivedThreadRetentionDays:   30,
			OrphanedWorktreeRetentionDays: 30,
			SubAgentNestingDepth:          project.DefaultSubAgentNestingDepth,
			MaxSubAgentNestingDepth:       project.MaxSubAgentNestingDepth,
			WorkflowKeywordTrigger:        true,
			WorkflowSizeGuideline:         project.DefaultWorkflowSizeGuideline,
			CodingAgents: []project.CodingAgentSetting{{
				ID: project.CodingAgentKindPiNative, Name: "Pi Native",
				Kind: project.CodingAgentKindPiNative, IsDefault: true,
			}},
			Theme:             defaultTheme,
			DefaultTheme:      defaultTheme,
			UsingDefaultTheme: true,
		},
		stateTopicCodingAgents: []codingAgentConfig{{
			ID:    codingAgentPi,
			Label: "Pi",
			Models: []codingAgentChoice{{
				ID: "openai/gpt-5", Label: "GPT-5",
			}},
			ThinkingLevels: []codingAgentChoice{{
				ID: "high", Label: "High",
			}},
		}},
		stateTopicSandboxConfig: sandboxConfigState{
			Scope:     "global",
			Path:      "/workspace/sandbox.json",
			Config:    sandboxConfig{},
			Inherited: defaultSandbox,
			Effective: defaultSandbox,
		},
		stateTopicCleanup: project.CleanupOverview{
			GeneratedAt:                   fixtureTime,
			ArchivedThreadRetentionDays:   30,
			OrphanedWorktreeRetentionDays: 30,
			Threads: []project.ThreadCleanupOverview{{
				ProjectID: "project-1", ProjectName: "Fixture project",
				ThreadID: "thread-archived", ThreadTitle: "Archived fixture thread",
				ArchivedAt: fixtureTime, ScheduledDeletionAt: &scheduledDeletionAt,
			}},
			Worktrees: []project.WorktreeCleanupOverview{{
				ProjectID: "project-1", ProjectName: "Fixture project",
				ThreadID: "thread-orphaned", ThreadTitle: "Orphaned fixture thread",
				WorktreePath: "/workspace/worktrees/orphaned", Branch: "fixture/orphaned",
				DetachedAt: fixtureTime, ScheduledDeletionAt: &scheduledDeletionAt,
				HasUncommittedChanges: true, InspectionError: "Fixture inspection warning.",
			}},
		},
		stateTopicSessionClosures: sessionClosureOverview{
			GeneratedAt: fixtureTime, InactivityHours: 24,
			Events: []sessionClosureEvent{{
				ID: "closure-1", ProjectID: "project-1", ProjectName: "Fixture project",
				ThreadID: "thread-1", ThreadTitle: "Fixture thread",
				SessionNames:   []string{"fixture-terminal", "fixture-tools"},
				LastActivityAt: startedAt, ClosedAt: fixtureTime, Reason: "inactivity",
			}},
		},
		stateTopicGitBranches: gitBranchState{
			IsRepository: true, Current: "main",
			Branches: []gitBranch{{Name: "main", Current: true}},
		},
		stateTopicBrowserStatus: browserStateSnapshot{
			Backend:      "headless-chrome",
			Presentation: "stream",
			Capabilities: browserStateCapabilities{
				NativeView: &falseValue, InteractiveStream: &trueValue,
				Preview: &trueValue,
			},
			Reachable: &trueValue,
			Running:   &trueValue,
			Pages: []browserStatePage{{
				ID: "page-fixture", Title: "Fixture page", URL: "https://example.test/",
			}},
			CurrentTargetID: &currentTargetID,
			Current: &browserStateCurrentPage{
				ID: "page-fixture", Title: "Fixture page", URL: "https://example.test/",
				CanGoBack: &falseValue, CanGoForward: &trueValue, Loading: &falseValue,
			},
		},
		stateTopicTmuxSessions: []tmuxBrowserSession{{
			Name: "fixture-session", Attached: true, Kind: "shell",
			ProjectID: "project-1", ProjectName: "Fixture project",
			ThreadID: "thread-1", ThreadTitle: "Fixture thread",
			Windows: []tmuxBrowserWindow{{
				ID: "@1", Index: 0, Name: "shell", Active: true,
				PaneCount: 1, CurrentCommand: "zsh",
			}},
		}},
		stateTopicAgentSkills: agentSkillStatus{
			Name: agentSkillName, Path: "/workspace/skills", Installed: true, UpToDate: true,
			Skills: []agentSkillItemStatus{{
				Name: "kiwi-code-threads", Path: "/workspace/skills/kiwi-code-threads",
				Installed: true, UpToDate: true,
			}},
		},
	}

	topics := make(map[string]json.RawMessage, len(values))
	for tag, value := range values {
		payload, err := json.Marshal(value)
		if err != nil {
			t.Fatalf("marshal %q fixture: %v", tag, err)
		}
		topics[tag] = payload
	}
	return topics
}
