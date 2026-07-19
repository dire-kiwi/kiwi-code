package headless

import (
	"bufio"
	"strings"
	"testing"
)

func TestReadEventParsesNamedMultilineData(t *testing.T) {
	event, err := readEvent(bufio.NewReader(strings.NewReader(
		"event: projects\n" +
			"data: [{\"id\":\"one\"}]\n" +
			"data: \n\n",
	)))
	if err != nil {
		t.Fatal(err)
	}
	if event.name != projectsEventName || string(event.data) != "[{\"id\":\"one\"}]\n" {
		t.Fatalf("event = %#v", event)
	}
}

func TestReadBoundedLineRejectsOversizedInput(t *testing.T) {
	_, err := readBoundedLine(bufio.NewReader(strings.NewReader("12345\n")), 4)
	if err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("oversized line error = %v", err)
	}
}

func TestActivitySnapshotContainsRejectsMalformedJSON(t *testing.T) {
	if activitySnapshotContains([]byte("not-json"), "project", "thread", "") {
		t.Fatal("malformed activity snapshot matched a cleared status")
	}
}
