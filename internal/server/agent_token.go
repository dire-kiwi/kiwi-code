package server

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const agentTokenFileName = "agent-token"
const agentTokenHeader = "X-Kiwi-Code-Agent-Token"

func loadOrCreateAgentToken(dataDirectory string) (string, error) {
	if err := os.MkdirAll(dataDirectory, 0o700); err != nil {
		return "", fmt.Errorf("create agent token directory: %w", err)
	}
	path := filepath.Join(dataDirectory, agentTokenFileName)
	for attempt := 0; attempt < 2; attempt++ {
		contents, err := os.ReadFile(path)
		if err == nil {
			token := strings.TrimSpace(string(contents))
			if !validAgentToken(token) {
				return "", errors.New("stored agent token is invalid")
			}
			if err := os.Chmod(path, 0o600); err != nil {
				return "", fmt.Errorf("secure agent token: %w", err)
			}
			return token, nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf("read agent token: %w", err)
		}

		bytes := make([]byte, 32)
		if _, err := rand.Read(bytes); err != nil {
			return "", fmt.Errorf("create agent token: %w", err)
		}
		token := hex.EncodeToString(bytes)
		file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if errors.Is(err, os.ErrExist) {
			continue
		}
		if err != nil {
			return "", fmt.Errorf("store agent token: %w", err)
		}
		_, writeErr := file.WriteString(token + "\n")
		syncErr := file.Sync()
		closeErr := file.Close()
		if err := errors.Join(writeErr, syncErr, closeErr); err != nil {
			_ = os.Remove(path)
			return "", fmt.Errorf("store agent token: %w", err)
		}
		return token, nil
	}
	return "", errors.New("could not load the agent token created by another process")
}

func validAgentToken(token string) bool {
	if len(token) != 64 {
		return false
	}
	_, err := hex.DecodeString(token)
	return err == nil
}
