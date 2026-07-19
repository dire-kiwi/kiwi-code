package server

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ivan/dire-mux/internal/project"
)

func TestPrepareTmuxSocketMigrationCreatesVerifiedCompatibilityAlias(t *testing.T) {
	directory := t.TempDir()
	const (
		legacySocket    = "dm-old"
		canonicalSocket = "dm-new"
		serverPID       = 4242
	)
	legacyPath := filepath.Join(directory, legacySocket)
	canonicalPath := filepath.Join(directory, canonicalSocket)
	tmuxPath := writeFakeTmux(t, strings.Join([]string{
		"#!/bin/sh",
		"case \"$2\" in",
		legacySocket + ") printf '%s\\t%s\\n' " + shellQuote(fmt.Sprint(serverPID)) + " " + shellQuote(legacyPath) + ";;",
		canonicalSocket + ")",
		"  if [ -L " + shellQuote(canonicalPath) + " ] && [ \"$(readlink " + shellQuote(canonicalPath) + ")\" = " + shellQuote(legacyPath) + " ]; then",
		"    printf '%s\\t%s\\n' " + shellQuote(fmt.Sprint(serverPID)) + " " + shellQuote(legacyPath),
		"  else",
		"    echo 'no server running' >&2; exit 1",
		"  fi;;",
		"*) echo 'unexpected socket' >&2; exit 2;;",
		"esac",
	}, "\n")+"\n")

	migration, err := prepareTmuxSocketMigration(directory, tmuxPath, canonicalSocket, legacySocket)
	if err != nil {
		t.Fatal(err)
	}
	if migration == nil {
		t.Fatal("live legacy server did not activate socket migration")
	}
	target, err := os.Readlink(canonicalPath)
	if err != nil {
		t.Fatal(err)
	}
	if target != legacyPath {
		t.Fatalf("canonical socket alias = %q, want %q", target, legacyPath)
	}
	marker, found, err := readTmuxSocketMigrationMarker(filepath.Join(directory, tmuxSocketMigrationMarkerName))
	if err != nil || !found {
		t.Fatalf("migration marker: found=%t marker=%#v err=%v", found, marker, err)
	}
	if marker.ServerPID != serverPID || marker.CanonicalSocketPath != canonicalPath || marker.LegacySocketPath != legacyPath {
		t.Fatalf("migration marker = %#v", marker)
	}
	if err := migration.prepareServerStart(); err != nil {
		t.Fatalf("verified alias blocked session creation: %v", err)
	}
	if err := os.Remove(canonicalPath); err != nil {
		t.Fatal(err)
	}
	if err := migration.prepareServerStart(); err == nil || !strings.Contains(err.Error(), "alias") {
		t.Fatalf("missing alias did not fail closed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(directory, tmuxSocketMigrationMarkerName)); err != nil {
		t.Fatalf("fail-closed alias check removed migration marker: %v", err)
	}
}

func TestPrepareTmuxSocketMigrationRejectsTwoLiveServers(t *testing.T) {
	directory := t.TempDir()
	const (
		legacySocket    = "dm-old-conflict"
		canonicalSocket = "dm-new-conflict"
	)
	tmuxPath := writeFakeTmux(t, "#!/bin/sh\ncase \"$2\" in\n"+
		legacySocket+") printf '111\\t%s\\n' "+shellQuote(filepath.Join(directory, legacySocket))+";;\n"+
		canonicalSocket+") printf '222\\t%s\\n' "+shellQuote(filepath.Join(directory, canonicalSocket))+";;\n"+
		"*) exit 2;;\nesac\n")

	migration, err := prepareTmuxSocketMigration(directory, tmuxPath, canonicalSocket, legacySocket)
	if err == nil || !strings.Contains(err.Error(), "both contain sessions") {
		t.Fatalf("conflicting server migration = %#v, %v", migration, err)
	}
	if _, statErr := os.Lstat(filepath.Join(directory, canonicalSocket)); !os.IsNotExist(statErr) {
		t.Fatalf("conflicting migration changed canonical socket path: %v", statErr)
	}
}

func TestPrepareTmuxSocketMigrationFailsClosedForLiveRecordedServer(t *testing.T) {
	directory := t.TempDir()
	const (
		legacySocket    = "dm-old-live"
		canonicalSocket = "dm-new-live"
	)
	legacyPath := filepath.Join(directory, legacySocket)
	canonicalPath := filepath.Join(directory, canonicalSocket)
	markerPath := filepath.Join(directory, tmuxSocketMigrationMarkerName)
	marker := tmuxSocketMigrationMarker{
		Version:             tmuxSocketMigrationMarkerVersion,
		CanonicalSocket:     canonicalSocket,
		LegacySocket:        legacySocket,
		CanonicalSocketPath: canonicalPath,
		LegacySocketPath:    legacyPath,
		ServerPID:           os.Getpid(),
	}
	if err := writeTmuxSocketMigrationMarker(markerPath, marker); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(legacyPath, canonicalPath); err != nil {
		t.Fatal(err)
	}
	tmuxPath := writeFakeTmux(t, "#!/bin/sh\necho 'no server running' >&2\nexit 1\n")

	migration, err := prepareTmuxSocketMigration(directory, tmuxPath, canonicalSocket, legacySocket)
	if err == nil || !strings.Contains(err.Error(), "still alive") {
		t.Fatalf("live recorded server migration = %#v, %v", migration, err)
	}
	if _, statErr := os.Lstat(canonicalPath); statErr != nil {
		t.Fatalf("fail-closed migration removed compatibility alias: %v", statErr)
	}
	if _, statErr := os.Stat(markerPath); statErr != nil {
		t.Fatalf("fail-closed migration removed durable marker: %v", statErr)
	}
}

func TestPrepareTmuxSocketMigrationRetiresDeadCompatibilityAlias(t *testing.T) {
	directory := t.TempDir()
	const (
		legacySocket    = "dm-old-dead"
		canonicalSocket = "dm-new-dead"
		deadPID         = 1073741823
	)
	if processAlive(deadPID) {
		t.Skipf("chosen dead pid %d is unexpectedly alive", deadPID)
	}
	legacyPath := filepath.Join(directory, legacySocket)
	canonicalPath := filepath.Join(directory, canonicalSocket)
	markerPath := filepath.Join(directory, tmuxSocketMigrationMarkerName)
	marker := tmuxSocketMigrationMarker{
		Version:             tmuxSocketMigrationMarkerVersion,
		CanonicalSocket:     canonicalSocket,
		LegacySocket:        legacySocket,
		CanonicalSocketPath: canonicalPath,
		LegacySocketPath:    legacyPath,
		ServerPID:           deadPID,
	}
	if err := writeTmuxSocketMigrationMarker(markerPath, marker); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(legacyPath, canonicalPath); err != nil {
		t.Fatal(err)
	}
	tmuxPath := writeFakeTmux(t, "#!/bin/sh\necho 'no server running' >&2\nexit 1\n")

	migration, err := prepareTmuxSocketMigration(directory, tmuxPath, canonicalSocket, legacySocket)
	if err != nil || migration != nil {
		t.Fatalf("retire dead migration = %#v, %v", migration, err)
	}
	if _, statErr := os.Lstat(canonicalPath); !os.IsNotExist(statErr) {
		t.Fatalf("dead compatibility alias survived: %v", statErr)
	}
	if _, statErr := os.Stat(markerPath); !os.IsNotExist(statErr) {
		t.Fatalf("dead migration marker survived: %v", statErr)
	}
}

func TestTmuxSocketMigrationAliasReachesLiveServer(t *testing.T) {
	tmuxPath, legacySocket := isolatedTmuxServer(t)
	canonicalSocket := legacySocket + "n"
	dataDirectory := t.TempDir()

	migration, err := prepareTmuxSocketMigration(dataDirectory, tmuxPath, canonicalSocket, legacySocket)
	if err != nil {
		t.Fatal(err)
	}
	if migration == nil {
		t.Fatal("live isolated server did not activate migration")
	}
	legacy, legacyFound, err := inspectTmuxServer(tmuxPath, legacySocket)
	if err != nil || !legacyFound {
		t.Fatalf("legacy server identity: found=%t identity=%#v err=%v", legacyFound, legacy, err)
	}
	canonical, canonicalFound, err := inspectTmuxServer(tmuxPath, canonicalSocket)
	if err != nil || !canonicalFound || canonical.PID != legacy.PID {
		t.Fatalf("canonical alias identity: found=%t identity=%#v legacy=%#v err=%v", canonicalFound, canonical, legacy, err)
	}
	aliasPath := filepath.Join(filepath.Dir(legacy.SocketPath), canonicalSocket)
	t.Cleanup(func() { _ = os.Remove(aliasPath) })

	store, err := project.NewStore(filepath.Join(dataDirectory, "projects.json"))
	if err != nil {
		t.Fatal(err)
	}
	item, err := store.Add("Migration", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	thread := item.Threads[0]
	handler := newTerminalHandlerUnreconciledWithOptions(store, originPolicy{}, canonicalSocket)
	handler.tmuxPath = tmuxPath
	handler.tmuxSocketMigration = migration
	canonicalSession, _, created, err := handler.ensureTmuxSession(item, thread, "terminal")
	if err != nil || !created {
		t.Fatalf("create canonical session through migrated socket: session=%q created=%t err=%v", canonicalSession, created, err)
	}
	previousSession := previousTmuxSessionName(item.ID, thread.ID, "terminal")
	canonicalWindows, err := handler.tmuxDetailedWindows(canonicalSession)
	if err != nil {
		t.Fatal(err)
	}
	previousWindows, err := handler.tmuxDetailedWindows(previousSession)
	if err != nil {
		t.Fatal(err)
	}
	if len(canonicalWindows) != 1 || len(previousWindows) != 1 || canonicalWindows[0].Target.ID != previousWindows[0].Target.ID {
		t.Fatalf("new migration sessions are not grouped: canonical=%#v previous=%#v", canonicalWindows, previousWindows)
	}
	for _, sessionName := range []string{canonicalSession, previousSession} {
		command := exec.Command(tmuxPath, "-L", legacySocket, "has-session", "-t", exactTmuxSessionTarget(sessionName))
		command.Env = tmuxEnvironment()
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("legacy socket cannot see session %q created through alias: %v: %s", sessionName, err, output)
		}
	}
}

func TestTmuxSocketMigrationCutsOverAfterLegacyServerExits(t *testing.T) {
	tmuxPath, legacySocket := isolatedTmuxServer(t)
	canonicalSocket := legacySocket + "c"
	dataDirectory := t.TempDir()

	migration, err := prepareTmuxSocketMigration(dataDirectory, tmuxPath, canonicalSocket, legacySocket)
	if err != nil || migration == nil {
		t.Fatalf("prepare live migration = %#v, %v", migration, err)
	}
	legacy, found, err := inspectTmuxServer(tmuxPath, legacySocket)
	if err != nil || !found {
		t.Fatalf("legacy identity before cutover: found=%t identity=%#v err=%v", found, legacy, err)
	}
	kill := exec.Command(tmuxPath, "-L", legacySocket, "kill-server")
	kill.Env = tmuxEnvironment()
	if output, err := kill.CombinedOutput(); err != nil {
		t.Fatalf("stop legacy test server: %v: %s", err, output)
	}
	for attempt := 0; attempt < 100 && processAlive(legacy.PID); attempt++ {
		time.Sleep(10 * time.Millisecond)
	}
	if processAlive(legacy.PID) {
		t.Fatalf("legacy test server pid %d did not exit", legacy.PID)
	}

	t.Cleanup(func() {
		command := exec.Command(tmuxPath, "-L", canonicalSocket, "kill-server")
		command.Env = tmuxEnvironment()
		_ = command.Run()
	})
	handler := &terminalHandler{
		tmuxPath:            tmuxPath,
		tmuxSocket:          canonicalSocket,
		tmuxSocketMigration: migration,
	}
	if _, err := handler.createTmuxSession("canonical-cutover", "/", "probe", "/bin/sleep", []string{"30"}); err != nil {
		t.Fatal(err)
	}
	canonical, found, err := inspectTmuxServer(tmuxPath, canonicalSocket)
	if err != nil || !found || canonical.PID == legacy.PID || filepath.Base(canonical.SocketPath) != canonicalSocket {
		t.Fatalf("canonical identity after cutover: found=%t identity=%#v legacy=%#v err=%v", found, canonical, legacy, err)
	}
	if _, legacyFound, err := inspectTmuxServer(tmuxPath, legacySocket); err != nil || legacyFound {
		t.Fatalf("legacy server after cutover: found=%t err=%v", legacyFound, err)
	}
	if _, err := os.Stat(filepath.Join(dataDirectory, tmuxSocketMigrationMarkerName)); !os.IsNotExist(err) {
		t.Fatalf("cutover migration marker survived: %v", err)
	}
}

func writeFakeTmux(t *testing.T, script string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "tmux")
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}
