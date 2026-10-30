//go:build !windows

package main

import (
	"os"
	"os/exec"
	"strings"
	"syscall"
	"testing"
)

// TestBinaryLeavesStdinMode checks that the built program leaves the mode of
// a stdin it shares with its parent as it found it, for every command.
func TestBinaryLeavesStdinMode(t *testing.T) {
	bin := buildBinary(t)
	for _, args := range [][]string{{"--version"}, {"lsp", "--stdio"}} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			pr, pw, err := os.Pipe()
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = pr.Close(); _ = pw.Close() }()
			if err := syscall.SetNonblock(int(pr.Fd()), false); err != nil { // Fd may switch the file to blocking; the test sets the mode it checks for
				t.Fatal(err)
			}
			_ = pw.Close()                                        // the program reads end of file at once
			cmd := exec.CommandContext(t.Context(), bin, args...) //nolint:gosec // bin is the binary make test built
			cmd.Stdin = pr
			_ = cmd.Run() // lsp exits 1 for a session without shutdown
			flags, _, errno := syscall.Syscall(syscall.SYS_FCNTL, pr.Fd(), syscall.F_GETFL, 0)
			if errno != 0 {
				t.Fatal(errno)
			}
			if flags&syscall.O_NONBLOCK != 0 {
				t.Errorf("%s left its stdin O_NONBLOCK, want it as it was", strings.Join(args, " "))
			}
		})
	}
}
