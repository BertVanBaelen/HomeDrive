package thumbs

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/png"
	"os"
	"testing"
)

// These tests run the real tools when they are installed (the Docker
// image, a Linux machine with vips-tools, ffmpeg and poppler-utils), and
// are skipped elsewhere.

func realTools(t *testing.T, names ...string) *Service {
	t.Helper()
	tl := findTools()
	if !tl.has(names) {
		t.Skipf("needs %v on Linux", names)
	}
	src := newSource(t)
	s := newService(t.TempDir(), src, quiet(), tl)
	return s
}

func TestRealPhoto(t *testing.T) {
	s := realTools(t, vips, vipsheader)
	img := image.NewRGBA(image.Rect(0, 0, 1200, 600))
	for x := range 1200 {
		img.Set(x, x/2, color.RGBA{200, 30, 30, 255})
	}
	var buf bytes.Buffer
	png.Encode(&buf, img)
	v := s.src.(*fakeSource).add(t, buf.Bytes())
	p, err := s.Image(context.Background(), "photo.png", v, Grid)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(p)
	if !bytes.HasPrefix(b, []byte("RIFF")) || !bytes.Contains(b[:16], []byte("WEBP")) {
		t.Fatalf("not a WebP: %q", b[:min(len(b), 16)])
	}
	// Garbage behind a valid start fails cleanly.
	bad := s.src.(*fakeSource).add(t, append([]byte("\x89PNG\r\n\x1a\n"), bytes.Repeat([]byte{0xff}, 4096)...))
	if _, err := s.Image(context.Background(), "bad.png", bad, Grid); err == nil {
		t.Fatal("thumbnail of a broken PNG")
	}
}

func TestRealPlaylistRefused(t *testing.T) {
	s := realTools(t, vips, ffmpeg, ffprobe)
	// An HLS playlist pointing at a local file must not be read.
	m3u := "#EXTM3U\n#EXT-X-TARGETDURATION:10\n#EXTINF:10,\nfile:///etc/passwd\n#EXT-X-ENDLIST\n"
	v := s.src.(*fakeSource).add(t, []byte(m3u))
	if _, err := s.Image(context.Background(), "trap.mp4", v, Grid); err == nil {
		t.Fatal("ffmpeg read a playlist")
	}
}

func TestSandboxRefusesOtherPrograms(t *testing.T) {
	for _, args := range [][]string{nil, {"/bin/sh", "-c", "id"}, {"vips"}, {"/tmp/ffmpeg/../sh"}} {
		if code := Sandbox(args); code == 0 {
			t.Fatalf("sandbox started %v", args)
		}
	}
	if sandboxSupported && (!allowedTool("/usr/bin/vips") || allowedTool("vips")) {
		t.Fatal("allowedTool")
	}
}
