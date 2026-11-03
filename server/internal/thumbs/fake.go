package thumbs

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
)

// FakeTool stands in for a tool in tests, on machines without the tools
// or without Linux. It gets the tool's name, its arguments, the work
// folder and the input file (or nil), and does what the tool would:
// write its output files into dir and return what it prints.
type FakeTool func(tool string, args []string, dir string, input *os.File) ([]byte, error)

// NewWithFakes is New with every tool replaced by fake.
func NewWithFakes(stateDir string, src Source, log *slog.Logger, fake FakeTool) *Service {
	t := tools{paths: map[string]string{}}
	for _, name := range toolNames {
		t.paths[name] = "/fake/" + name
	}
	s := newService(stateDir, src, log, t)
	s.fake = fake
	return s
}

// FakeWebP is what the default fakes write as an image: enough of a WebP
// header for type sniffing.
const FakeWebP = "RIFF\x24\x00\x00\x00WEBPVP8 \x18\x00\x00\x00fake-webp"

// Fakes behave like the real tools on any input: images are 1200×800,
// videos and audio 12.5 s long, audio without a cover.
func Fakes(tool string, args []string, dir string, input *os.File) ([]byte, error) {
	switch tool {
	case vipsheader:
		return []byte("/dev/fd/3: 1200x800 uchar, 3 bands, srgb, jpegload\n"), nil
	case vips:
		// vips thumbnail <src> <dst>[options] <size> …
		dst, _, _ := strings.Cut(args[2], "[")
		return nil, os.WriteFile(dst, []byte(FakeWebP), 0o640)
	case ffprobe:
		return []byte(`{"format": {"duration": "12.500"}, "streams": [{"codec_type": "video", "disposition": {"attached_pic": 0}}]}`), nil
	case ffmpeg:
		return nil, os.WriteFile(args[len(args)-1], []byte("png"), 0o640)
	case pdftoppm:
		return nil, os.WriteFile(filepath.Join(dir, args[len(args)-1]+".png"), []byte("png"), 0o640)
	}
	return nil, fmt.Errorf("no fake for %s", tool)
}
