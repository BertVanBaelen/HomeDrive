package thumbs

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"

	"golang.org/x/sys/unix"
)

const sandboxSupported = true

// limits a tool runs under. RLIMIT_AS bounds memory (address space), CPU
// the processor time, FSIZE the largest file it may write, NOFILE the
// files it may hold open; no core dumps.
var limits = []struct {
	resource int
	value    uint64
}{
	{unix.RLIMIT_AS, 1 << 30},
	{unix.RLIMIT_CPU, 20},
	{unix.RLIMIT_FSIZE, 64 << 20},
	{unix.RLIMIT_NOFILE, 64},
	{unix.RLIMIT_CORE, 0},
}

// Sandbox is `homedrive sandbox <tool path> <args…>`: it lowers its own
// limits, makes sure the tool can't gain privileges and dies with the
// server, and replaces itself with the tool. The server starts tools only
// this way. It returns only when that fails.
func Sandbox(args []string) int {
	if len(args) < 1 || !allowedTool(args[0]) {
		fmt.Fprintln(os.Stderr, "sandbox: not a thumbnail tool")
		return 2
	}
	for _, l := range limits {
		if err := unix.Setrlimit(l.resource, &unix.Rlimit{Cur: l.value, Max: l.value}); err != nil {
			fmt.Fprintln(os.Stderr, "sandbox: setrlimit:", err)
			return 1
		}
	}
	if err := unix.Prctl(unix.PR_SET_NO_NEW_PRIVS, 1, 0, 0, 0); err != nil {
		fmt.Fprintln(os.Stderr, "sandbox: no_new_privs:", err)
		return 1
	}
	if err := unix.Prctl(unix.PR_SET_PDEATHSIG, uintptr(unix.SIGKILL), 0, 0, 0); err != nil {
		fmt.Fprintln(os.Stderr, "sandbox: pdeathsig:", err)
		return 1
	}
	argv := append([]string{filepath.Base(args[0])}, args[1:]...)
	err := unix.Exec(args[0], argv, os.Environ())
	fmt.Fprintln(os.Stderr, "sandbox: exec:", err)
	return 1
}

// isolate puts a tool in its own process group, and makes a timeout kill
// the whole group: whatever the tool started goes with it.
func isolate(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true, Pdeathsig: syscall.SIGKILL}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
}
