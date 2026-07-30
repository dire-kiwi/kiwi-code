package server

import (
	"bytes"
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
)

//go:embed pi-thread-title.ts
var piThreadTitleExtension []byte

//go:embed pi-thread-activity.ts
var piThreadActivityExtension []byte

//go:embed pi-thread-usage.ts
var piThreadUsageExtension []byte

//go:embed pi-thread-context.ts
var piThreadContextExtension []byte

//go:embed pi-browser/extension.ts
var piBrowserExtension []byte

//go:embed pi-browser/chrome-devtools-browser/SKILL.md
var piBrowserSkill []byte

func materializePiExtensions(dataDirectory string) ([]string, error) {
	if err := removeObsoletePiOrchestration(dataDirectory); err != nil {
		return nil, err
	}
	titlePath, err := materializePiThreadTitleExtension(dataDirectory)
	if err != nil {
		return nil, err
	}
	activityPath, err := materializePiThreadActivityExtension(dataDirectory)
	if err != nil {
		return nil, err
	}
	if _, err := materializePiThreadUsageExtension(dataDirectory); err != nil {
		return nil, err
	}
	contextPath, err := materializePiThreadContextExtension(dataDirectory)
	if err != nil {
		return nil, err
	}
	if _, err := materializePiBrowserExtension(dataDirectory); err != nil {
		return nil, err
	}
	// Usage and browser control are imported by the stable activity extension
	// path so existing Pi terminal sessions can pick them up with /reload.
	return []string{titlePath, activityPath, contextPath}, nil
}

func removeObsoletePiOrchestration(dataDirectory string) error {
	paths := []string{
		filepath.Join(dataDirectory, "extensions", "kiwi-code-child-threads.ts"),
		filepath.Join(dataDirectory, "extensions", "kiwi-code-workflows.ts"),
		filepath.Join(dataDirectory, "extensions", "kiwi-code-skill-forks.ts"),
		filepath.Join(dataDirectory, "skills", "kiwi-code-planner"),
	}
	for _, path := range paths {
		if err := os.RemoveAll(path); err != nil {
			return fmt.Errorf("remove obsolete Pi orchestration %q: %w", filepath.Base(path), err)
		}
	}
	return nil
}

func materializePiThreadTitleExtension(dataDirectory string) (string, error) {
	return materializePiExtension(dataDirectory, "kiwi-code-thread-title.ts", piThreadTitleExtension)
}

func materializePiThreadActivityExtension(dataDirectory string) (string, error) {
	return materializePiExtension(dataDirectory, "kiwi-code-thread-activity.ts", piThreadActivityExtension)
}

func materializePiThreadUsageExtension(dataDirectory string) (string, error) {
	return materializePiExtension(dataDirectory, "kiwi-code-thread-usage.ts", piThreadUsageExtension)
}

func materializePiThreadContextExtension(dataDirectory string) (string, error) {
	return materializePiExtension(dataDirectory, "kiwi-code-thread-context.ts", piThreadContextExtension)
}

func materializePiBrowserExtension(dataDirectory string) (string, error) {
	if err := materializePiBrowserSkill(dataDirectory); err != nil {
		return "", err
	}
	return materializePiExtension(dataDirectory, "kiwi-code-browser.ts", piBrowserExtension)
}

func materializePiBrowserSkill(dataDirectory string) error {
	return materializePiSkill(dataDirectory, "kiwi-code-in-app-browser", piBrowserSkill)
}

func materializePiSkill(dataDirectory, name string, contents []byte) error {
	skillDirectory := filepath.Join(dataDirectory, "skills", name)
	if err := os.MkdirAll(skillDirectory, 0o700); err != nil {
		return fmt.Errorf("create Pi skill directory %q: %w", name, err)
	}
	path := filepath.Join(skillDirectory, "SKILL.md")
	if current, err := os.ReadFile(path); err == nil && bytes.Equal(current, contents) {
		return nil
	}
	if err := writeFileAtomically(path, contents, serverAtomicFileOptions{
		Mode:     0o600,
		SyncFile: true,
	}); err != nil {
		return fmt.Errorf("write Pi skill %q: %w", name, err)
	}
	return nil
}

func materializePiExtension(dataDirectory, name string, contents []byte) (string, error) {
	extensionDirectory := filepath.Join(dataDirectory, "extensions")
	if err := os.MkdirAll(extensionDirectory, 0o700); err != nil {
		return "", fmt.Errorf("create Pi extension directory: %w", err)
	}
	path := filepath.Join(extensionDirectory, name)
	if current, err := os.ReadFile(path); err == nil && bytes.Equal(current, contents) {
		return path, nil
	}
	if err := writeFileAtomically(path, contents, serverAtomicFileOptions{
		Mode:     0o600,
		SyncFile: true,
	}); err != nil {
		return "", fmt.Errorf("write Pi extension: %w", err)
	}
	return path, nil
}
