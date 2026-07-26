package wire

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"unicode"
	"unicode/utf8"
)

const MaxClientNameRunes = 128

func DecodeClientMessage(payload []byte) (ClientMessage, error) {
	var header struct {
		Type string `json:"t"`
	}
	if err := decodeStrict(payload, &header, false); err != nil {
		return ClientMessage{}, invalidMessage(err)
	}

	switch header.Type {
	case ClientOpen:
		var value struct {
			Type     string `json:"t"`
			Protocol *int   `json:"protocol"`
			Client   string `json:"client"`
		}
		if err := decodeStrict(payload, &value, true, "t", "protocol", "client"); err != nil {
			return ClientMessage{}, invalidMessage(err)
		}
		if value.Protocol == nil || !validClientName(value.Client) {
			return ClientMessage{}, invalidMessage(errors.New("client is invalid"))
		}
		return ClientMessage{Type: value.Type, Protocol: *value.Protocol, Client: value.Client}, nil
	case ClientSub:
		var value struct {
			Type  string          `json:"t"`
			ID    uint32          `json:"id"`
			Topic json.RawMessage `json:"topic"`
		}
		if err := decodeStrict(payload, &value, true, "t", "id", "topic"); err != nil {
			return ClientMessage{}, invalidMessage(err)
		}
		if value.ID == 0 || !jsonObject(value.Topic) {
			return ClientMessage{}, invalidMessage(errors.New("subscription id and topic are required"))
		}
		return ClientMessage{Type: value.Type, ID: value.ID, Topic: append(json.RawMessage(nil), value.Topic...)}, nil
	case ClientUnsub, ClientResnap:
		var value struct {
			Type string `json:"t"`
			ID   uint32 `json:"id"`
		}
		if err := decodeStrict(payload, &value, true, "t", "id"); err != nil {
			return ClientMessage{}, invalidMessage(err)
		}
		if value.ID == 0 {
			return ClientMessage{}, invalidMessage(errors.New("subscription id is required"))
		}
		return ClientMessage{Type: value.Type, ID: value.ID}, nil
	case ClientPing:
		var value struct {
			Type      string `json:"t"`
			Timestamp *int64 `json:"ts"`
		}
		if err := decodeStrict(payload, &value, true, "t", "ts"); err != nil {
			return ClientMessage{}, invalidMessage(err)
		}
		if value.Timestamp == nil {
			return ClientMessage{}, invalidMessage(errors.New("timestamp is required"))
		}
		return ClientMessage{Type: value.Type, Timestamp: *value.Timestamp}, nil
	default:
		return ClientMessage{}, invalidMessage(fmt.Errorf("unknown message type %q", header.Type))
	}
}

func Encode(value any) ([]byte, error) {
	return json.Marshal(value)
}

func decodeStrict(payload []byte, target any, disallowUnknown bool, allowedKeys ...string) error {
	if disallowUnknown {
		if err := validateExactObjectKeys(payload, allowedKeys...); err != nil {
			return err
		}
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
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

// validateExactObjectKeys closes two gaps in encoding/json's struct decoder:
// field names are otherwise matched case-insensitively, and duplicate members
// silently replace earlier values. Wire objects require one exact occurrence.
func validateExactObjectKeys(payload []byte, allowedKeys ...string) error {
	decoder := json.NewDecoder(bytes.NewReader(payload))
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

func jsonObject(raw json.RawMessage) bool {
	var object map[string]json.RawMessage
	return len(raw) > 0 && json.Unmarshal(raw, &object) == nil && object != nil
}

func validClientName(value string) bool {
	if strings.TrimSpace(value) == "" || !utf8.ValidString(value) || utf8.RuneCountInString(value) > MaxClientNameRunes {
		return false
	}
	for _, character := range value {
		if unicode.IsControl(character) {
			return false
		}
	}
	return true
}

func invalidMessage(err error) error {
	return fmt.Errorf("invalid state message: %w", err)
}
