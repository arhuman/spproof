//go:build unix

package engine

import (
	"syscall"
	"testing"
)

// mkfifo creates a named pipe at path. It lives behind the unix build tag
// because mkfifo has no portable equivalent, and the tests that call it skip on
// platforms where it is unavailable.
func mkfifo(t *testing.T, path string) {
	t.Helper()
	if err := syscall.Mkfifo(path, 0o600); err != nil {
		t.Fatalf("mkfifo %s: %v", path, err)
	}
}
