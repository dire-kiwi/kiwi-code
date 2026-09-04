package server

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
)

// Query the installed CLI so discovery uses the same configuration and account
// as terminal launches. No conversation or model inference is started.
func discoverCodexModels(ctx context.Context, cwd string) ([]codingAgentChoice, error) {
	path, err := exec.LookPath(codingAgentCodex)
	if err != nil {
		return nil, err
	}
	return discoverCodexModelsAtPath(ctx, path, cwd)
}

func discoverCodexModelsAtPath(ctx context.Context, path, cwd string) ([]codingAgentChoice, error) {
	command := exec.CommandContext(ctx, path, "app-server")
	command.Dir = cwd
	stdin, err := command.StdinPipe()
	if err != nil {
		return nil, err
	}
	defer stdin.Close()
	stdout, err := command.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := command.Start(); err != nil {
		return nil, err
	}
	defer func() {
		// App-server stays alive after responding; always reap it, including on
		// malformed responses or cancellation.
		_ = command.Process.Kill()
		_ = command.Wait()
	}()
	encoder := json.NewEncoder(stdin)
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 64*1024), 16<<20)
	request := func(id int, method string, params any, result any) error {
		if err := encoder.Encode(map[string]any{"id": id, "method": method, "params": params}); err != nil {
			return err
		}
		for scanner.Scan() {
			var response struct {
				ID     *int            `json:"id"`
				Result json.RawMessage `json:"result"`
				Error  *struct {
					Message string `json:"message"`
				} `json:"error"`
			}
			if err := json.Unmarshal(scanner.Bytes(), &response); err != nil {
				return err
			}
			if response.ID == nil || *response.ID != id {
				continue
			}
			if response.Error != nil {
				return fmt.Errorf("Codex %s: %s", method, response.Error.Message)
			}
			return json.Unmarshal(response.Result, result)
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := scanner.Err(); err != nil {
			return err
		}
		return io.ErrUnexpectedEOF
	}
	var initialized json.RawMessage
	if err := request(1, "initialize", map[string]any{
		"clientInfo": map[string]string{"name": "kiwi-code", "version": "1.0.0"},
	}, &initialized); err != nil {
		return nil, err
	}
	if err := encoder.Encode(map[string]any{"method": "initialized"}); err != nil {
		return nil, err
	}
	models := make([]codingAgentChoice, 0)
	seen := make(map[string]bool)
	cursors := make(map[string]bool)
	cursor := ""
	for id := 2; ; id++ {
		params := map[string]any{}
		if cursor != "" {
			params["cursor"] = cursor
		}
		var page struct {
			Data []struct {
				Model                     string `json:"model"`
				DisplayName               string `json:"displayName"`
				Hidden                    bool   `json:"hidden"`
				SupportedReasoningEfforts []struct {
					ReasoningEffort string `json:"reasoningEffort"`
				} `json:"supportedReasoningEfforts"`
			} `json:"data"`
			NextCursor *string `json:"nextCursor"`
		}
		if err := request(id, "model/list", params, &page); err != nil {
			return nil, err
		}
		for _, model := range page.Data {
			modelID := strings.TrimSpace(model.Model)
			if model.Hidden || !validCodingAgentModel(modelID) || seen[modelID] {
				continue
			}
			seen[modelID] = true
			label := strings.TrimSpace(model.DisplayName)
			if label == "" {
				label = modelID
			}
			choice := codingAgentChoice{ID: modelID, Label: label}
			for _, effort := range model.SupportedReasoningEfforts {
				choice.ReasoningLevels = append(choice.ReasoningLevels, effort.ReasoningEffort)
			}
			models = append(models, choice)
		}
		if page.NextCursor == nil || *page.NextCursor == "" {
			break
		}
		cursor = *page.NextCursor
		if cursors[cursor] {
			return nil, errors.New("Codex model/list repeated a pagination cursor")
		}
		cursors[cursor] = true
	}
	return models, nil
}
