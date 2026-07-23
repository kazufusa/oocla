//go:build !unix

package claudecli

import "os/exec"

// isolateGroup is a no-op where process groups are not available.
func isolateGroup(*exec.Cmd) {}

// killGroup reports that no group signal was sent, so the caller falls back to
// killing the process alone.
func killGroup(int) bool { return false }
