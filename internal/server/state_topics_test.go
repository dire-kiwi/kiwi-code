package server

import "testing"

const legacyBrowserRecordingsTopic = "browser.recordings"

func TestStateTopicRegistryDecodesEveryCatalogTopic(t *testing.T) {
	server := &Server{}
	valid := map[string]string{
		stateTopicProjects:           `{"tag":"projects"}`,
		stateTopicProfiles:           `{"tag":"profiles"}`,
		stateTopicAgentActivity:      `{"tag":"agentActivity"}`,
		stateTopicThreadUsage:        `{"tag":"threadUsage"}`,
		stateTopicThreadStatus:       `{"tag":"thread.status","projectId":"project","threadId":"thread"}`,
		stateTopicSettings:           `{"tag":"settings"}`,
		stateTopicCodingAgents:       `{"tag":"codingAgents","projectId":"project"}`,
		stateTopicSandboxConfig:      `{"tag":"sandboxConfig","scope":"global"}`,
		stateTopicCleanup:            `{"tag":"cleanup"}`,
		stateTopicSessionClosures:    `{"tag":"sessionClosures"}`,
		stateTopicGitBranches:        `{"tag":"git.branches","projectId":"project"}`,
		stateTopicBrowserStatus:      `{"tag":"browser.status","projectId":"project","threadId":"thread"}`,
		legacyBrowserRecordingsTopic: `{"tag":"browser.recordings","projectId":"project","threadId":"thread"}`,
		stateTopicTmuxSessions:       `{"tag":"tmuxSessions"}`,
		stateTopicAgentSkills:        `{"tag":"agentSkills"}`,
	}
	registry := server.stateTopicRegistry(nil)
	if len(registry) != len(valid) {
		t.Fatalf("registry has %d topics, test has %d", len(registry), len(valid))
	}
	fixtures := buildStateTopicFixtures(t)
	if len(fixtures) != len(registry) {
		t.Fatalf("fixture has %d topics, registry has %d", len(fixtures), len(registry))
	}
	for tag := range registry {
		if _, covered := valid[tag]; !covered {
			t.Errorf("topic %q has no decode fixture", tag)
		}
		if _, covered := fixtures[tag]; !covered {
			t.Errorf("topic %q has no protocol fixture", tag)
		}
	}
	for tag := range fixtures {
		if _, registered := registry[tag]; !registered {
			t.Errorf("protocol fixture topic %q is not registered", tag)
		}
	}
	for tag, raw := range valid {
		t.Run(tag, func(t *testing.T) {
			handler, _, err := server.decodeStateTopic([]byte(raw), []string{"http://localhost:4000"})
			if err != nil {
				t.Fatal(err)
			}
			if handler == nil {
				t.Fatal("decoded topic has a nil handler")
			}
		})
	}
}

func TestStateTopicRegistryRejectsInvalidParameters(t *testing.T) {
	server := &Server{}
	tests := []string{
		`null`,
		`{}`,
		`{"tag":"unknown"}`,
		`{"tag":"projects","extra":true}`,
		`{"Tag":"projects"}`,
		`{"tag":"projects","tag":"projects"}`,
		`{"tag":"projects"} {}`,
		`{"tag":"thread.status","projectId":"","threadId":"thread"}`,
		`{"tag":"thread.status","ProjectId":"project","threadId":"thread"}`,
		`{"tag":"thread.status","projectId":"project","threadId":"thread","threadId":"thread"}`,
		`{"tag":"thread.status","projectId":"project"}`,
		`{"tag":"git.branches","projectId":""}`,
		`{"tag":"browser.status","projectId":"project","threadId":""}`,
		`{"tag":"sandboxConfig","scope":"unknown"}`,
		`{"tag":"sandboxConfig","scope":"global","projectId":"project"}`,
		`{"tag":"sandboxConfig","scope":"thread","projectId":"project"}`,
	}
	for _, raw := range tests {
		t.Run(raw, func(t *testing.T) {
			if _, _, err := server.decodeStateTopic([]byte(raw), nil); err == nil {
				t.Fatalf("decodeStateTopic(%s) succeeded", raw)
			}
		})
	}
}
