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

//go:embed pi-child-threads.ts
var piChildThreadsExtension []byte

//go:embed pi-workflows.ts
var piWorkflowsExtension []byte

//go:embed pi-skill-forks.ts
var piSkillForksExtension []byte

//go:embed pi-browser/extension.ts
var piBrowserExtension []byte

//go:embed pi-browser/chrome-devtools-browser/SKILL.md
var piBrowserSkill []byte

//go:embed pi-planner/kiwi-code-planner/SKILL.md
var piPlannerSkill []byte

func materializePiExtensions(dataDirectory string) ([]string, error) {
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
	childThreadsPath, err := materializePiChildThreadsExtension(dataDirectory)
	if err != nil {
		return nil, err
	}
	workflowsPath, err := materializePiWorkflowsExtension(dataDirectory)
	if err != nil {
		return nil, err
	}
	if err := materializePiPlannerSkill(dataDirectory); err != nil {
		return nil, err
	}
	if _, err := materializePiSkillForksExtension(dataDirectory); err != nil {
		return nil, err
	}
	if _, err := materializePiBrowserExtension(dataDirectory); err != nil {
		return nil, err
	}
	// Usage, browser control, and forked skills are imported by the stable
	// activity extension path so existing Pi terminal sessions can pick them up
	// with /reload after an application update.
	return []string{titlePath, activityPath, contextPath, childThreadsPath, workflowsPath}, nil
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

func materializePiChildThreadsExtension(dataDirectory string) (string, error) {
	return materializePiExtension(dataDirectory, "kiwi-code-child-threads.ts", piChildThreadsExtension)
}

func materializePiWorkflowsExtension(dataDirectory string) (string, error) {
	return materializePiExtension(dataDirectory, "kiwi-code-workflows.ts", piWorkflowsExtension)
}

func materializePiSkillForksExtension(dataDirectory string) (string, error) {
	return materializePiExtension(dataDirectory, "kiwi-code-skill-forks.ts", piSkillForksExtension)
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

func materializePiPlannerSkill(dataDirectory string) error {
	return materializePiSkill(dataDirectory, "kiwi-code-planner", piPlannerSkill)
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
