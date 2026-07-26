package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
)

const (
	stateTopicProjects          = "projects"
	stateTopicProfiles          = "profiles"
	stateTopicAgentActivity     = "agentActivity"
	stateTopicThreadUsage       = "threadUsage"
	stateTopicProcessWebServers = "processWebServers"
	stateTopicThreadStatus      = "thread.status"
	stateTopicSettings          = "settings"
	stateTopicCodingAgents      = "codingAgents"
	stateTopicSandboxConfig     = "sandboxConfig"
	stateTopicCleanup           = "cleanup"
	stateTopicSessionClosures   = "sessionClosures"
	stateTopicGitBranches       = "git.branches"
	stateTopicBrowserStatus     = "browser.status"
	stateTopicBrowserRecordings = "browser.recordings"
	stateTopicTmuxSessions      = "tmuxSessions"
	stateTopicAgentSkills       = "agentSkills"
)

type stateTopicDefinition struct {
	decode func(json.RawMessage) (any, error)
	open   func(context.Context, any, *stateChannel) error
}

func (d stateTopicDefinition) Decode(raw json.RawMessage) (any, error) {
	return d.decode(raw)
}

func (d stateTopicDefinition) Open(ctx context.Context, params any, channel *stateChannel) error {
	return d.open(ctx, params, channel)
}

type stateEmptyTopic struct {
	Tag string `json:"tag"`
}

type stateProjectTopic struct {
	Tag       string `json:"tag"`
	ProjectID string `json:"projectId"`
}

type stateThreadTopic struct {
	Tag       string `json:"tag"`
	ProjectID string `json:"projectId"`
	ThreadID  string `json:"threadId"`
}

type stateCodingAgentsTopic struct {
	Tag       string `json:"tag"`
	ProjectID string `json:"projectId,omitempty"`
}

type stateSandboxTopic struct {
	Tag       string `json:"tag"`
	Scope     string `json:"scope"`
	ProjectID string `json:"projectId,omitempty"`
	ThreadID  string `json:"threadId,omitempty"`
}

func (s *Server) decodeStateTopic(raw json.RawMessage, protectedOrigins []string) (stateTopicHandler, any, error) {
	var header struct {
		Tag string `json:"tag"`
	}
	if err := decodeStateTopicJSON(raw, &header, false); err != nil || strings.TrimSpace(header.Tag) == "" {
		return nil, nil, errors.New("Invalid state topic.")
	}
	definition := s.stateTopicRegistry(protectedOrigins)[header.Tag]
	if definition == nil {
		return nil, nil, fmt.Errorf("Unknown state topic %q.", header.Tag)
	}
	params, err := definition.Decode(raw)
	if err != nil {
		return nil, nil, err
	}
	return definition, params, nil
}

func (s *Server) stateTopicRegistry(protectedOrigins []string) map[string]stateTopicHandler {
	empty := func(tag string, open func(context.Context, *stateChannel) error) stateTopicHandler {
		return stateTopicDefinition{
			decode: func(raw json.RawMessage) (any, error) {
				var params stateEmptyTopic
				if err := decodeStateTopicJSON(raw, &params, true, "tag"); err != nil || params.Tag != tag {
					return nil, errors.New("Invalid state topic parameters.")
				}
				return params, nil
			},
			open: func(ctx context.Context, _ any, channel *stateChannel) error {
				return open(ctx, channel)
			},
		}
	}
	project := func(tag string, open func(context.Context, string, *stateChannel) error) stateTopicHandler {
		return stateTopicDefinition{
			decode: func(raw json.RawMessage) (any, error) {
				var params stateProjectTopic
				if err := decodeStateTopicJSON(raw, &params, true, "tag", "projectId"); err != nil ||
					params.Tag != tag || strings.TrimSpace(params.ProjectID) == "" {
					return nil, errors.New("Invalid state topic parameters.")
				}
				return params, nil
			},
			open: func(ctx context.Context, value any, channel *stateChannel) error {
				params := value.(stateProjectTopic)
				return open(ctx, params.ProjectID, channel)
			},
		}
	}
	thread := func(tag string, open func(context.Context, string, string, *stateChannel) error) stateTopicHandler {
		return stateTopicDefinition{
			decode: func(raw json.RawMessage) (any, error) {
				var params stateThreadTopic
				if err := decodeStateTopicJSON(raw, &params, true, "tag", "projectId", "threadId"); err != nil ||
					params.Tag != tag || strings.TrimSpace(params.ProjectID) == "" ||
					strings.TrimSpace(params.ThreadID) == "" {
					return nil, errors.New("Invalid state topic parameters.")
				}
				return params, nil
			},
			open: func(ctx context.Context, value any, channel *stateChannel) error {
				params := value.(stateThreadTopic)
				return open(ctx, params.ProjectID, params.ThreadID, channel)
			},
		}
	}

	registry := map[string]stateTopicHandler{
		stateTopicProjects:          empty(stateTopicProjects, s.openProjectsTopic),
		stateTopicProfiles:          empty(stateTopicProfiles, s.openProfilesTopic),
		stateTopicAgentActivity:     empty(stateTopicAgentActivity, s.openAgentActivityTopic),
		stateTopicThreadUsage:       empty(stateTopicThreadUsage, s.openThreadUsageTopic),
		stateTopicProcessWebServers: empty(stateTopicProcessWebServers, s.openProcessWebServersTopic),
		stateTopicThreadStatus:      thread(stateTopicThreadStatus, s.openThreadStatusTopic),
		stateTopicSettings:          empty(stateTopicSettings, s.openSettingsTopic),
		stateTopicCleanup:           empty(stateTopicCleanup, s.openCleanupTopic),
		stateTopicSessionClosures:   empty(stateTopicSessionClosures, s.openSessionClosuresTopic),
		stateTopicGitBranches:       project(stateTopicGitBranches, s.openGitBranchesTopic),
		stateTopicBrowserStatus: thread(stateTopicBrowserStatus, func(ctx context.Context, projectID, threadID string, channel *stateChannel) error {
			return s.openBrowserStatusTopic(ctx, projectID, threadID, protectedOrigins, channel)
		}),
		stateTopicBrowserRecordings: thread(stateTopicBrowserRecordings, func(ctx context.Context, projectID, threadID string, channel *stateChannel) error {
			return s.openBrowserRecordingsTopic(ctx, projectID, threadID, protectedOrigins, channel)
		}),
		stateTopicTmuxSessions: empty(stateTopicTmuxSessions, s.openTmuxSessionsTopic),
		stateTopicAgentSkills:  empty(stateTopicAgentSkills, s.openAgentSkillsTopic),
	}
	registry[stateTopicCodingAgents] = stateTopicDefinition{
		decode: func(raw json.RawMessage) (any, error) {
			var params stateCodingAgentsTopic
			if err := decodeStateTopicJSON(raw, &params, true, "tag", "projectId"); err != nil || params.Tag != stateTopicCodingAgents {
				return nil, errors.New("Invalid state topic parameters.")
			}
			return params, nil
		},
		open: func(ctx context.Context, value any, channel *stateChannel) error {
			return s.openCodingAgentsTopic(ctx, value.(stateCodingAgentsTopic).ProjectID, channel)
		},
	}
	registry[stateTopicSandboxConfig] = stateTopicDefinition{
		decode: func(raw json.RawMessage) (any, error) {
			var params stateSandboxTopic
			if err := decodeStateTopicJSON(raw, &params, true, "tag", "scope", "projectId", "threadId"); err != nil || params.Tag != stateTopicSandboxConfig {
				return nil, errors.New("Invalid state topic parameters.")
			}
			switch params.Scope {
			case "global":
				if params.ProjectID != "" || params.ThreadID != "" {
					return nil, errors.New("Global sandbox topics do not accept project or thread ids.")
				}
			case "thread":
				if strings.TrimSpace(params.ProjectID) == "" || strings.TrimSpace(params.ThreadID) == "" {
					return nil, errors.New("Thread sandbox topics require project and thread ids.")
				}
			default:
				return nil, errors.New("Sandbox scope must be global or thread.")
			}
			return params, nil
		},
		open: func(ctx context.Context, value any, channel *stateChannel) error {
			return s.openSandboxConfigTopic(ctx, value.(stateSandboxTopic), channel)
		},
	}
	return registry
}

func decodeStateTopicJSON(raw json.RawMessage, target any, disallowUnknown bool, allowedKeys ...string) error {
	if disallowUnknown {
		if err := validateExactStateTopicKeys(raw, allowedKeys...); err != nil {
			return err
		}
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	if disallowUnknown {
		decoder.DisallowUnknownFields()
	}
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var extra json.RawMessage
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("multiple JSON values")
		}
		return err
	}
	return nil
}

func validateExactStateTopicKeys(raw json.RawMessage, allowedKeys ...string) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	if delimiter, ok := token.(json.Delim); !ok || delimiter != '{' {
		return errors.New("expected a JSON object")
	}
	allowed := make(map[string]struct{}, len(allowedKeys))
	for _, key := range allowedKeys {
		allowed[key] = struct{}{}
	}
	seen := make(map[string]struct{}, len(allowedKeys))
	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			return err
		}
		key, ok := token.(string)
		if !ok {
			return errors.New("expected a JSON object key")
		}
		if _, ok := allowed[key]; !ok {
			return fmt.Errorf("unknown field %q", key)
		}
		if _, duplicate := seen[key]; duplicate {
			return fmt.Errorf("duplicate field %q", key)
		}
		seen[key] = struct{}{}
		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil {
			return err
		}
	}
	token, err = decoder.Token()
	if err != nil {
		return err
	}
	if delimiter, ok := token.(json.Delim); !ok || delimiter != '}' {
		return errors.New("expected the end of a JSON object")
	}
	var extra json.RawMessage
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("multiple JSON values")
		}
		return err
	}
	return nil
}
