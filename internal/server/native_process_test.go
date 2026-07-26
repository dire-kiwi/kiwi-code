package server

import (
	"bytes"
	"os/exec"
	"reflect"
	"testing"
	"time"
)

func TestNativeProcessRunPreservesExitOrdering(t *testing.T) {
	spec := nativeProcessSpec{
		displayName:         "Test",
		endedMessage:        "ended",
		unexpectedMessage:   "unexpected",
		writeAfterExitError: "closed",
		stopTimeout:         time.Second,
	}
	core, stdout, stderr, err := startNativeCommand(
		nativeProcessKey{ProjectID: "project", ThreadID: "thread"},
		spec,
		exec.Command("sh", "-c", "exit 7"),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer stdout.Close()
	defer stderr.Close()

	var order []string
	var providerExitText string
	result := make(chan struct {
		exitText   string
		doneClosed bool
	}, 1)
	core.run(func(message string) {
		order = append(order, "provider:"+message)
		providerExitText = core.exitMessage()
	}, func() {
		order = append(order, "manager")
		result <- struct {
			exitText   string
			doneClosed bool
		}{exitText: core.exitMessage(), doneClosed: channelClosed(core.done)}
	})

	select {
	case state := <-result:
		if want := []string{"provider:unexpected", "manager"}; !reflect.DeepEqual(order, want) {
			t.Fatalf("exit order = %#v, want %#v", order, want)
		}
		if providerExitText != "ended" {
			t.Fatalf("provider observed stored exit text %q before its exit hook completed", providerExitText)
		}
		if state.exitText != "unexpected" || !state.doneClosed {
			t.Fatalf("manager observed exit state = %#v", state)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("native process exit did not complete")
	}
}

func TestNativeProcessWriteLineFramesAndRejectsAfterExit(t *testing.T) {
	stdin := &nativeProcessTestWriter{}
	core := nativeProcessCore{
		spec:  nativeProcessSpec{writeAfterExitError: "closed"},
		stdin: stdin,
		done:  make(chan struct{}),
	}
	payload := []byte(`{"type":"prompt"}`)
	if err := core.writeLine(payload); err != nil {
		t.Fatal(err)
	}
	if got, want := stdin.String(), string(payload)+"\n"; got != want {
		t.Fatalf("framed payload = %q, want %q", got, want)
	}
	if bytes.HasSuffix(payload, []byte{'\n'}) {
		t.Fatal("writeLine mutated the caller's payload")
	}

	close(core.done)
	if err := core.writeLine(payload); err == nil || err.Error() != "closed" {
		t.Fatalf("write after exit error = %v, want closed", err)
	}
}

type nativeProcessTestWriter struct {
	bytes.Buffer
}

func (*nativeProcessTestWriter) Close() error {
	return nil
}
