package thumbs

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"
)

// The tools, by the names of their commands.
const (
	vips       = "vips"
	vipsheader = "vipsheader"
	ffmpeg     = "ffmpeg"
	ffprobe    = "ffprobe"
	pdftoppm   = "pdftoppm"
)

var toolNames = []string{vips, vipsheader, ffmpeg, ffprobe, pdftoppm}

// SandboxCommand is the hidden homedrive subcommand that starts a tool:
// `homedrive sandbox <tool path> <args…>` lowers its own limits and then
// becomes the tool (sandbox_linux.go).
const SandboxCommand = "sandbox"

// toolTimeout ends a tool run, its whole process group included.
const toolTimeout = 20 * time.Second

// maxOutput is the most a tool may print before it is cut off.
const maxOutput = 1 << 20

// tools are the tools found at startup, by name.
type tools struct {
	paths map[string]string
}

// findTools looks for the tools on the PATH. They only run in the
// sandbox, which needs Linux; elsewhere none are used.
func findTools() tools {
	t := tools{paths: map[string]string{}}
	if !sandboxSupported {
		return t
	}
	if _, err := os.Executable(); err != nil {
		return t
	}
	for _, name := range toolNames {
		if p, err := exec.LookPath(name); err == nil {
			if abs, err := filepath.Abs(p); err == nil {
				t.paths[name] = abs
			}
		}
	}
	return t
}

func (t tools) has(names []string) bool {
	for _, n := range names {
		if t.paths[n] == "" {
			return false
		}
	}
	return true
}

func (t tools) missing() []string {
	var out []string
	for _, n := range toolNames {
		if t.paths[n] == "" {
			out = append(out, n)
		}
	}
	return out
}

// affected lists the kinds of file that show placeholders for lack of
// tools, for the startup log line.
func (t tools) affected() string {
	set := map[string]bool{}
	for _, ft := range types {
		if !t.has(ft.needs) {
			switch {
			case ft.link:
				set["saved links"] = true
			case ft.image == "photo":
				set["photos"] = true
			case ft.image != "":
				set[ft.image] = true
			}
		}
	}
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return strings.Join(out, ", ")
}

// sandboxed starts a tool through `homedrive sandbox`.
func sandboxed(ctx context.Context, tool string, args []string) *exec.Cmd {
	self, _ := os.Executable()
	return exec.CommandContext(ctx, self, append([]string{SandboxCommand, tool}, args...)...)
}

// errTool is a tool that failed or ran too long.
var errTool = errors.New("tool failed")

// run starts a tool on a file and waits for it. The file is the tool's
// file descriptor 3 (/dev/fd/3; fd: for ffmpeg), so no name chosen by a
// user is ever on a command line. dir is the job's work folder, where the
// tool writes. It returns what the tool printed on stdout.
func (s *Service) run(ctx context.Context, input *os.File, dir, tool string, args ...string) ([]byte, error) {
	path := s.tools.paths[tool]
	if path == "" {
		return nil, fmt.Errorf("%w: %s is missing", errTool, tool)
	}
	select {
	case s.slots <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	defer func() { <-s.slots }()

	if input != nil {
		// ffmpeg reads the descriptor itself, from where it stands.
		if _, err := input.Seek(0, 0); err != nil {
			return nil, err
		}
	}
	if s.fake != nil {
		return s.fake(tool, args, dir, input)
	}
	ctx, cancel := context.WithTimeout(ctx, toolTimeout)
	defer cancel()
	cmd := sandboxed(ctx, path, args)
	cmd.Dir = dir
	cmd.Env = toolEnv(dir)
	if input != nil {
		cmd.ExtraFiles = []*os.File{input}
	}
	var stdout, stderr capped
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	cmd.WaitDelay = 2 * time.Second
	isolate(cmd)
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if len(msg) > 300 {
			msg = msg[:300] + "…"
		}
		if ctx.Err() != nil {
			return nil, fmt.Errorf("%w: %s took longer than %s", errTool, tool, toolTimeout)
		}
		return nil, fmt.Errorf("%w: %s: %v: %s", errTool, tool, err, msg)
	}
	return stdout.Bytes(), nil
}

// toolEnv is the whole environment of a tool: nothing of the server's.
func toolEnv(dir string) []string {
	return []string{
		"PATH=/usr/bin:/bin",
		"HOME=" + dir,
		"TMPDIR=" + dir,
		"LC_ALL=C",
		// libvips refuses its loaders that aren't fuzzed and hardened
		// (ImageMagick, MATLAB, FITS, …), and works on one thread.
		"VIPS_BLOCK_UNTRUSTED=1",
		"VIPS_CONCURRENCY=1",
	}
}

// capped keeps the first maxOutput bytes written to it.
type capped struct{ bytes.Buffer }

func (c *capped) Write(p []byte) (int, error) {
	if room := maxOutput - c.Len(); room > 0 {
		c.Buffer.Write(p[:min(len(p), room)])
	}
	return len(p), nil
}

// allowedTool reports whether the sandbox may start a program: one of the
// tools, by absolute path.
func allowedTool(path string) bool {
	return filepath.IsAbs(path) && slices.Contains(toolNames, filepath.Base(path))
}
