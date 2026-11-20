//go:build !unix

package documents_test

import "testing"

// mkfifo skips the case: only a Unix file system holds a named pipe.
func mkfifo(t *testing.T, _ string) {
	t.Helper()
	t.Skip("a named pipe needs a Unix file system")
}
