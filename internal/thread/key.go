// Package thread defines the shared identity key for a thread within a
// project. It is a leaf package imported by every feature area so that the
// same {projectID, threadID} pair is not redeclared per subsystem.
package thread

// Key identifies a thread within a project.
type Key struct {
	ProjectID string
	ThreadID  string
}
