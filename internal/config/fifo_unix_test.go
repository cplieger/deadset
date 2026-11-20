//go:build unix

package config_test

import (
	"syscall"
	"testing"
)

// mkfifo makes a named pipe at path.
func mkfifo(t *testing.T, path string) {
	t.Helper()
	if err := syscall.Mkfifo(path, 0o600); err != nil {
		t.Fatalf("Setup: %v", err)
	}
}
