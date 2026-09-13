package server

import "encoding/json"

// Native chat is a provider-neutral browser contract. Provider adapters own
// process startup, session persistence and conversion from their wire events.
type chatItem struct {
	ID     string `json:"id"`
	Kind   string `json:"kind"`
	Text   string `json:"text"`
	Title  string `json:"title,omitempty"`
	Status string `json:"status,omitempty"`
}
type chatRequest struct {
	ID        json.RawMessage `json:"id"`
	Kind      string          `json:"kind"`
	Title     string          `json:"title"`
	Details   string          `json:"details"`
	Questions json.RawMessage `json:"questions,omitempty"`
}
type chatUsage struct {
	InputTokens       int64 `json:"inputTokens"`
	OutputTokens      int64 `json:"outputTokens"`
	CachedInputTokens int64 `json:"cachedInputTokens"`
	TotalTokens       int64 `json:"totalTokens"`
}
type chatState struct {
	Items    []chatItem    `json:"items"`
	Requests []chatRequest `json:"requests"`
	Working  bool          `json:"working"`
	Error    string        `json:"error,omitempty"`
	Model    string        `json:"model,omitempty"`
	Effort   string        `json:"effort,omitempty"`
	Usage    *chatUsage    `json:"usage,omitempty"`
}
type chatClientMessage struct {
	Type      string                `json:"type"`
	Message   string                `json:"message"`
	Model     string                `json:"model"`
	Effort    string                `json:"effort"`
	Images    []piNativeClientImage `json:"images"`
	RequestID json.RawMessage       `json:"requestId"`
	Decision  string                `json:"decision"`
	Answers   map[string]struct {
		Answers []string `json:"answers"`
	} `json:"answers"`
}
