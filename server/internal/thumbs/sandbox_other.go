//go:build !linux

package thumbs

import (
	"fmt"
	"os"
	"os/exec"
)

// The sandbox needs Linux (setrlimit, process groups, no_new_privs).
// Elsewhere, such as a Windows development machine, no tools are used and
// every file shows its placeholder.
const sandboxSupported = false

func Sandbox(args []string) int {
	fmt.Fprintln(os.Stderr, "sandbox: thumbnail tools only run on Linux")
	return 2
}

func isolate(*exec.Cmd) {}
