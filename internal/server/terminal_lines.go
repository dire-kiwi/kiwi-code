package server

import (
	"errors"
	"net/http"
	"os/exec"
	"strconv"
	"strings"

	"github.com/dire-kiwi/kiwi-code/internal/project"
)

var errInvalidTmuxLinesTarget = errors.New("invalid terminal target")

type tmuxLinesResponse struct {
	Tool      string         `json:"tool"`
	Agent     string         `json:"agent,omitempty"`
	Process   *processWindow `json:"process,omitempty"`
	Window    *int           `json:"window,omitempty"`
	LineLimit int            `json:"lineLimit"`
	Output    string         `json:"output"`
}

func (h *terminalHandler) readTmuxLines(w http.ResponseWriter, r *http.Request) {
	item, thread, ok := h.tmuxThread(w, r)
	if !ok {
		return
	}

	rawTool := strings.TrimSpace(r.URL.Query().Get("tool"))
	if rawTool == "" {
		writeError(w, http.StatusBadRequest, "A terminal tool is required.")
		return
	}
	tool, err := normalizeTerminalTool(rawTool)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	lines, err := tmuxLineLimit(r.URL.Query().Get("lines"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	response := tmuxLinesResponse{Tool: tool, LineLimit: lines}
	target, found, err := h.tmuxLinesTarget(item, thread, tool, r, &response)
	if errors.Is(err, errInvalidTmuxLinesTarget) {
		writeError(w, http.StatusBadRequest, "Invalid terminal target.")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Could not read terminal output.")
		return
	}
	if !found {
		writeError(w, http.StatusNotFound, "Terminal target not found.")
		return
	}

	output, err := h.tmuxCommand(
		"capture-pane", "-p", "-J",
		"-S", "-"+strconv.Itoa(lines),
		"-t", target,
	).CombinedOutput()
	if err != nil {
		var exitError *exec.ExitError
		if errors.As(err, &exitError) {
			writeError(w, http.StatusNotFound, "Terminal target not found.")
			return
		}
		writeError(w, http.StatusInternalServerError, "Could not read terminal output.")
		return
	}
	response.Output = string(output)
	writeJSON(w, http.StatusOK, response)
}

func tmuxLineLimit(raw string) (int, error) {
	if raw == "" {
		return 200, nil
	}
	lines, err := strconv.Atoi(raw)
	if err != nil || lines < 1 || lines > maxProcessLogLines {
		return 0, errors.New("Line count must be between 1 and 5000.")
	}
	return lines, nil
}

func (h *terminalHandler) tmuxLinesTarget(
	item project.Project,
	thread project.Thread,
	tool string,
	r *http.Request,
	response *tmuxLinesResponse,
) (string, bool, error) {
	agentQuery := strings.TrimSpace(r.URL.Query().Get("agent"))
	processID := strings.TrimSpace(r.URL.Query().Get("processId"))
	windowQuery := strings.TrimSpace(r.URL.Query().Get("window"))

	agent := ""
	shellWindow := -1
	switch tool {
	case "pi":
		if processID != "" || windowQuery != "" {
			return "", false, errInvalidTmuxLinesTarget
		}
		var err error
		agent, err = normalizeCodingAgent(agentQuery)
		if err != nil {
			return "", false, errInvalidTmuxLinesTarget
		}
		response.Agent = agent
	case "process":
		if agentQuery != "" || windowQuery != "" || processID == "" {
			return "", false, errInvalidTmuxLinesTarget
		}
	case "terminal":
		if agentQuery != "" || processID != "" {
			return "", false, errInvalidTmuxLinesTarget
		}
		if windowQuery != "" {
			window, err := strconv.Atoi(windowQuery)
			if err != nil || window < 0 {
				return "", false, errInvalidTmuxLinesTarget
			}
			shellWindow = window
			response.Window = &window
		}
	default:
		if agentQuery != "" || processID != "" || windowQuery != "" {
			return "", false, errInvalidTmuxLinesTarget
		}
	}

	sessionName := tmuxSessionName(item.ID, thread.ID, tool)
	exists, err := h.tmuxSessionExists(sessionName)
	if err != nil || !exists {
		return "", false, err
	}

	switch tool {
	case "pi":
		window, found, err := h.tmuxToolWindow(sessionName, "pi")
		if err != nil || !found {
			return "", false, err
		}
		panes, err := h.tmuxAgentPanes(window.ID)
		if err != nil {
			return "", false, err
		}
		for _, pane := range panes {
			if pane.Agent == agent {
				return pane.ID, true, nil
			}
		}
		// Sessions created before coding-agent pane metadata are safe to
		// observe as Pi. The regular attach path adopts the same pane.
		if agent == codingAgentPi {
			for _, pane := range panes {
				if pane.Agent == "" {
					return pane.ID, true, nil
				}
			}
		}
		return "", false, nil
	case "process":
		process, target, found, err := h.tmuxProcessWindow(sessionName, processID)
		if err != nil || !found {
			return "", false, err
		}
		response.Process = &process
		return target.ID, true, nil
	case "terminal":
		target := sessionName
		if shellWindow >= 0 {
			target += ":" + strconv.Itoa(shellWindow)
		}
		return h.tmuxPaneID(target)
	default:
		target, found, err := h.tmuxToolWindow(sessionName, tool)
		if err != nil || !found {
			return "", false, err
		}
		return target.ID, true, nil
	}
}

func (h *terminalHandler) tmuxPaneID(target string) (string, bool, error) {
	output, err := h.tmuxCommand("display-message", "-p", "-t", target, "#{pane_id}").CombinedOutput()
	if err != nil {
		var exitError *exec.ExitError
		if errors.As(err, &exitError) {
			return "", false, nil
		}
		return "", false, err
	}
	paneID := strings.TrimSpace(string(output))
	return paneID, paneID != "", nil
}
