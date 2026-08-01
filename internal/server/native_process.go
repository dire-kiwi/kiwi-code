package server

import "github.com/dire-kiwi/kiwi-code/internal/agent/native"

// Aliases while the native managers migrate into internal/agent/native.
type (
	nativeProcessKey = native.Key
	// Preserve the existing name while Pi and Claude share the same identity type.
	piNativeProcessKey = native.Key
)
