package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/dire-kiwi/kiwi-code/internal/wire"
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

func (s *Server) decodeStateTopic(raw json.RawMessage, protectedOrigins []string) (stateTopicHandler, any, error) {
	var header struct {
		Tag string `json:"tag"`
	}
	if err := wire.Decode(raw, &header); err != nil || strings.TrimSpace(header.Tag) == "" {
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
				if err := wire.DecodeExactObject(raw, &params, "tag"); err != nil || params.Tag != tag {
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
				if err := wire.DecodeExactObject(raw, &params, "tag", "projectId"); err != nil ||
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
				if err := wire.DecodeExactObject(raw, &params, "tag", "projectId", "threadId"); err != nil ||
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
			if err := wire.DecodeExactObject(raw, &params, "tag", "projectId"); err != nil || params.Tag != stateTopicCodingAgents {
				return nil, errors.New("Invalid state topic parameters.")
			}
			return params, nil
		},
		open: func(ctx context.Context, value any, channel *stateChannel) error {
			return s.openCodingAgentsTopic(ctx, value.(stateCodingAgentsTopic).ProjectID, channel)
		},
	}
	return registry
}
