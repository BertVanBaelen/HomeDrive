package thumbs

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/xuri/excelize/v2"

	"github.com/BertVanBaelen/HomeDrive/server/internal/db"
)

// The tests start tools through the test binary, which then has to act as
// `homedrive sandbox`.
func TestMain(m *testing.M) {
	if len(os.Args) > 1 && os.Args[1] == SandboxCommand {
		os.Exit(Sandbox(os.Args[2:]))
	}
	os.Exit(m.Run())
}

// fakeSource keeps versions as files in a folder.
type fakeSource struct {
	dir       string
	mu        sync.Mutex
	durations map[string]int64
	gone      map[string]bool
}

func newSource(t *testing.T) *fakeSource {
	return &fakeSource{dir: t.TempDir(), durations: map[string]int64{}, gone: map[string]bool{}}
}

func (f *fakeSource) add(t *testing.T, contents []byte) string {
	t.Helper()
	id := db.NewID()
	if err := os.WriteFile(filepath.Join(f.dir, id), contents, 0o640); err != nil {
		t.Fatal(err)
	}
	return id
}

func (f *fakeSource) OpenVersion(ctx context.Context, id string) (*os.File, error) {
	return os.Open(filepath.Join(f.dir, id))
}

func (f *fakeSource) SetDuration(ctx context.Context, id string, ms int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.durations[id] = ms
	return nil
}

func (f *fakeSource) duration(id string) int64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.durations[id]
}

func (f *fakeSource) LiveVersions(ctx context.Context, ids []string) (map[string]bool, error) {
	live := map[string]bool{}
	for _, id := range ids {
		if !f.gone[id] {
			live[id] = true
		}
	}
	return live, nil
}

// recorder wraps the fakes and keeps every command line.
type recorder struct {
	mu    sync.Mutex
	calls [][]string
	fake  FakeTool
}

func (r *recorder) run(tool string, args []string, dir string, input *os.File) ([]byte, error) {
	r.mu.Lock()
	r.calls = append(r.calls, append([]string{tool}, args...))
	r.mu.Unlock()
	if r.fake != nil {
		return r.fake(tool, args, dir, input)
	}
	return Fakes(tool, args, dir, input)
}

func (r *recorder) ran(tool string) [][]string {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out [][]string
	for _, c := range r.calls {
		if c[0] == tool {
			out = append(out, c[1:])
		}
	}
	return out
}

func quiet() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func newFaked(t *testing.T) (*Service, *fakeSource, *recorder) {
	src := newSource(t)
	rec := &recorder{}
	return NewWithFakes(t.TempDir(), src, quiet(), rec.run), src, rec
}

var (
	jpegBytes = []byte("\xff\xd8\xff\xe0\x00\x10JFIF\x00rest of a photo")
	heicBytes = []byte("\x00\x00\x00\x18ftypheic\x00\x00\x00\x00mif1heic")
	mp4Bytes  = []byte("\x00\x00\x00\x18ftypisom\x00\x00\x02\x00isomiso2")
)

func TestKind(t *testing.T) {
	s, src, _ := newFaked(t)
	v := src.add(t, jpegBytes)
	none := NewWithoutTools(t.TempDir(), src, quiet())
	for _, c := range []struct{ name, faked, without string }{
		{"Photo.JPG", KindImage, ""},
		{"IMG_1234.heic", KindImage, ""},
		{"clip.mov", KindImage, ""},
		{"song.mp3", KindImage, ""},
		{"scan.pdf", KindImage, ""},
		{"Cats.url", KindImage, ""},
		{"notes.md", KindSnippet, KindSnippet},
		{"main.go", KindSnippet, KindSnippet},
		{"budget.csv", KindSnippet, KindSnippet},
		{"budget.xlsx", KindSnippet, KindSnippet},
		{"photos.zip", KindSnippet, KindSnippet},
		{"logo.svg", "", ""},
		{"letter.docx", "", ""},
		{"old.xls", "", ""},
		{"backup.tar.gz", "", ""},
		{"README", "", ""},
	} {
		if got := s.Kind(c.name, v); got != c.faked {
			t.Errorf("Kind(%s) = %q, want %q", c.name, got, c.faked)
		}
		if got := none.Kind(c.name, v); got != c.without {
			t.Errorf("without tools Kind(%s) = %q, want %q", c.name, got, c.without)
		}
	}
	if s.Kind("Photo.jpg", "../../etc/passwd") != "" {
		t.Error("invalid version has a thumbnail")
	}
	if !s.HasPreview("IMG_1234.HEIC", v) || !s.HasPreview("scan.tiff", v) || s.HasPreview("Photo.jpg", v) {
		t.Error("HasPreview")
	}
	if none.HasPreview("IMG_1234.heic", v) {
		t.Error("preview without tools")
	}
	ctx := context.Background()
	if _, err := none.Image(ctx, "Photo.jpg", v, Grid); !errors.Is(err, ErrNone) {
		t.Errorf("image without tools: %v", err)
	}
}

func TestPhoto(t *testing.T) {
	s, src, rec := newFaked(t)
	ctx := context.Background()
	v := src.add(t, jpegBytes)
	p, err := s.Image(ctx, "My holiday; rm -rf.jpg", v, Grid)
	if err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(p); string(b) != FakeWebP {
		t.Fatalf("grid = %q", b)
	}
	if _, err := s.Image(ctx, "x.jpg", v, List); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Image(ctx, "x.jpg", v, Preview); !errors.Is(err, ErrNone) {
		t.Fatalf("JPEG has a preview: %v", err)
	}
	// The header is checked first, through the descriptor; then the grid
	// size is made from the file and the list size from the grid one.
	if h := rec.ran(vipsheader); len(h) != 1 || !slices.Equal(h[0], []string{"/dev/fd/3"}) {
		t.Fatalf("vipsheader %v", h)
	}
	thumbs := rec.ran(vips)
	if len(thumbs) != 2 || thumbs[0][1] != "/dev/fd/3" || thumbs[0][3] != "400" ||
		!strings.HasSuffix(thumbs[1][1], "grid.webp") || thumbs[1][3] != "80" {
		t.Fatalf("vips %v", thumbs)
	}
	// Nothing a user chose is on a command line.
	for _, c := range rec.calls {
		if strings.Contains(strings.Join(c, " "), "holiday") {
			t.Fatalf("file name on a command line: %v", c)
		}
	}
	// Made once.
	if _, err := s.Image(ctx, "x.jpg", v, Grid); err != nil || len(rec.ran(vips)) != 2 {
		t.Fatalf("not cached: %v %d", err, len(rec.ran(vips)))
	}
}

func TestPhotoRefused(t *testing.T) {
	s, src, rec := newFaked(t)
	ctx := context.Background()
	// Bytes that aren't an image on the list never reach libvips.
	svg := src.add(t, []byte("<svg xmlns='http://www.w3.org/2000/svg'/>"))
	if _, err := s.Image(ctx, "logo.jpg", svg, Grid); !errors.Is(err, ErrNone) {
		t.Fatal(err)
	}
	if len(rec.calls) != 0 {
		t.Fatalf("ran %v", rec.calls)
	}
	if s.Kind("logo.jpg", svg) != "" {
		t.Fatal("a failed version still has a thumbnail")
	}
	// Images over 100 megapixels, by their header, get none.
	rec.fake = func(tool string, args []string, dir string, input *os.File) ([]byte, error) {
		if tool == vipsheader {
			return []byte("/dev/fd/3: 20000x10000 uchar, 3 bands, srgb, pngload"), nil
		}
		return Fakes(tool, args, dir, input)
	}
	big := src.add(t, []byte("\x89PNG\r\n\x1a\nbomb"))
	if _, err := s.Image(ctx, "bomb.png", big, Grid); !errors.Is(err, ErrNone) {
		t.Fatal(err)
	}
	if len(rec.ran(vips)) != 0 {
		t.Fatal("libvips ran on a huge image")
	}
	// A tool that fails leaves the placeholder, for good.
	rec.fake = func(tool string, args []string, dir string, input *os.File) ([]byte, error) {
		return nil, errors.New("crashed")
	}
	broken := src.add(t, jpegBytes)
	if _, err := s.Image(ctx, "broken.jpg", broken, Grid); !errors.Is(err, ErrNone) {
		t.Fatal(err)
	}
	n := len(rec.calls)
	s.Image(ctx, "broken.jpg", broken, Grid)
	if len(rec.calls) != n {
		t.Fatal("a failed version was tried again")
	}
}

func TestHEIC(t *testing.T) {
	s, src, rec := newFaked(t)
	ctx := context.Background()
	v := src.add(t, heicBytes)
	p, err := s.Image(ctx, "IMG_0001.HEIC", v, Preview)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(p, "-preview.webp") {
		t.Fatal(p)
	}
	thumbs := rec.ran(vips)
	// The preview is decoded from the file; the grid image from the
	// preview, so the HEIC is decoded once.
	if len(thumbs) != 3 || thumbs[0][1] != "/dev/fd/3" || thumbs[0][3] != "2048" ||
		!strings.HasSuffix(thumbs[1][1], "-preview.webp") || thumbs[1][3] != "400" {
		t.Fatalf("vips %v", thumbs)
	}
}

func TestVideoAndAudio(t *testing.T) {
	s, src, rec := newFaked(t)
	ctx := context.Background()
	v := src.add(t, mp4Bytes)
	if _, err := s.Image(ctx, "clip.mp4", v, Grid); err != nil {
		t.Fatal(err)
	}
	if src.duration(v) != 12500 {
		t.Fatalf("duration %d", src.duration(v))
	}
	probe, frame := rec.ran(ffprobe), rec.ran(ffmpeg)
	if len(probe) != 1 || len(frame) != 1 {
		t.Fatalf("ffprobe %v ffmpeg %v", probe, frame)
	}
	// ffmpeg only reads its descriptor and the listed formats; the frame
	// is at 10 % of the length.
	for _, args := range [][]string{probe[0], frame[0]} {
		line := strings.Join(args, " ")
		if !strings.Contains(line, "-protocol_whitelist fd ") || !strings.Contains(line, "-format_whitelist "+demuxers) ||
			!strings.Contains(line, "-fd 3") || strings.Contains(line, "concat") || strings.Contains(line, "hls") {
			t.Fatalf("ffmpeg input options: %s", line)
		}
	}
	if !slices.Contains(frame[0], "1.250") || !slices.Contains(frame[0], "fd:") {
		t.Fatalf("frame %v", frame[0])
	}

	// Audio without cover art keeps its waveform card, with its length.
	a := src.add(t, []byte("ID3\x04\x00\x00\x00\x00\x00\x00audio"))
	if _, err := s.Image(ctx, "song.mp3", a, Grid); !errors.Is(err, ErrNone) {
		t.Fatal(err)
	}
	if src.duration(a) != 12500 || s.Kind("song.mp3", a) != "" {
		t.Fatalf("audio: %d %q", src.duration(a), s.Kind("song.mp3", a))
	}
	// With a cover, the cover.
	rec.fake = func(tool string, args []string, dir string, input *os.File) ([]byte, error) {
		if tool == ffprobe {
			return []byte(`{"format": {"duration": "200.0"}, "streams": [{"codec_type": "audio"}, {"codec_type": "video", "disposition": {"attached_pic": 1}}]}`), nil
		}
		return Fakes(tool, args, dir, input)
	}
	c := src.add(t, []byte("ID3\x04\x00\x00\x00\x00\x00\x00cover"))
	if _, err := s.Image(ctx, "album.m4a", c, Grid); err != nil {
		t.Fatal(err)
	}
	if last := rec.ran(ffmpeg); slices.Contains(last[len(last)-1], "-ss") {
		t.Fatalf("cover art seeks: %v", last)
	}
}

func TestPDF(t *testing.T) {
	s, src, rec := newFaked(t)
	v := src.add(t, []byte("%PDF-1.7\n"))
	if _, err := s.Image(context.Background(), "scan.pdf", v, Grid); err != nil {
		t.Fatal(err)
	}
	p := rec.ran(pdftoppm)
	if len(p) != 1 || !slices.Equal(p[0][:4], []string{"-f", "1", "-l", "1"}) || !slices.Contains(p[0], "/dev/fd/3") {
		t.Fatalf("pdftoppm %v", p)
	}
}

func snippetOf(t *testing.T, s *Service, name string, v string) Snippet {
	t.Helper()
	b, err := s.Snippet(context.Background(), name, v)
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	var sn Snippet
	if err := json.Unmarshal(b, &sn); err != nil {
		t.Fatal(err)
	}
	return sn
}

func TestTextSnippets(t *testing.T) {
	s := NewWithoutTools(t.TempDir(), nil, quiet())
	src := newSource(t)
	s.src = src
	var md strings.Builder
	md.WriteString("\xef\xbb\xbf# Shopping\r\n\n- milk\t2\n- \x1b[31mbread\n")
	for i := range 20 {
		md.WriteString("line " + strings.Repeat("x", i) + "\n")
	}
	sn := snippetOf(t, s, "list.md", src.add(t, []byte(md.String())))
	if sn.Kind != "text" || len(sn.Lines) != snippetLines || sn.Lines[0] != "# Shopping" || sn.Lines[2] != "- milk    2" ||
		strings.ContainsRune(sn.Lines[3], 0x1b) {
		t.Fatalf("text %+v", sn)
	}
	sn = snippetOf(t, s, "main.go", src.add(t, []byte("package main\n\nfunc main() {}\n\n\n")))
	if sn.Kind != "code" || len(sn.Lines) != 3 {
		t.Fatalf("code %+v", sn)
	}
	// Only the first 2 KB are read; the line it cuts is left out.
	long := strings.Repeat("a", 3000)
	sn = snippetOf(t, s, "one.txt", src.add(t, []byte("first\n"+long)))
	if len(sn.Lines) != 1 || sn.Lines[0] != "first" {
		t.Fatalf("cut %+v", sn)
	}
	sn = snippetOf(t, s, "long.txt", src.add(t, []byte(long)))
	if len(sn.Lines) != 1 || len([]rune(sn.Lines[0])) != lineChars {
		t.Fatalf("long line %d", len(sn.Lines[0]))
	}
	// A multi-byte character cut by the read isn't mangled.
	sn = snippetOf(t, s, "utf8.txt", src.add(t, []byte(strings.Repeat("é", 1500))))
	if !strings.HasSuffix(sn.Lines[0], "é") {
		t.Fatalf("utf-8 %q", sn.Lines[0][len(sn.Lines[0])-4:])
	}
	// A binary file named .txt has no snippet.
	v := src.add(t, []byte("MZ\x00\x00\x03binary"))
	if _, err := s.Snippet(context.Background(), "tool.txt", v); !errors.Is(err, ErrNone) || s.Kind("tool.txt", v) != "" {
		t.Fatalf("binary: %v", err)
	}
}

func TestSheetSnippets(t *testing.T) {
	s := NewWithoutTools(t.TempDir(), nil, quiet())
	src := newSource(t)
	s.src = src
	sn := snippetOf(t, s, "a.csv", src.add(t, []byte("Item;Price;Qty;Total;Note\nMilk;1,20;2;2,40;x\n\"Bread; white\";2;1;2;\n")))
	if sn.Kind != "sheet" || len(sn.Rows) != 3 || len(sn.Rows[0]) != 4 || sn.Rows[1][1] != "1,20" || sn.Rows[2][0] != "Bread; white" {
		t.Fatalf("csv %+v", sn)
	}
	var csv strings.Builder
	for i := range 100 {
		csv.WriteString("a,b,c\n")
		_ = i
	}
	if sn = snippetOf(t, s, "b.csv", src.add(t, []byte(csv.String()))); len(sn.Rows) != sheetRows {
		t.Fatalf("csv rows %d", len(sn.Rows))
	}

	x := excelize.NewFile()
	x.SetSheetRow("Sheet1", "A1", &[]any{"Month", "Rent", "Food", "Fun", "Hidden"})
	x.SetSheetRow("Sheet1", "A2", &[]any{"Jan", 950, 412.5, 80})
	x.SetCellFormula("Sheet1", "B3", "SUM(B2:B2)")
	x.SetCellValue("Sheet1", "A3", "Total")
	var buf bytes.Buffer
	if err := x.Write(&buf); err != nil {
		t.Fatal(err)
	}
	sn = snippetOf(t, s, "budget.xlsx", src.add(t, buf.Bytes()))
	if sn.Kind != "sheet" || len(sn.Rows) != 3 || len(sn.Rows[0]) != 4 || sn.Rows[0][0] != "Month" || sn.Rows[1][2] != "412.5" {
		t.Fatalf("xlsx %+v", sn)
	}
	// Not a spreadsheet: no snippet.
	v := src.add(t, []byte("PK\x03\x04 broken"))
	if _, err := s.Snippet(context.Background(), "broken.xlsx", v); !errors.Is(err, ErrNone) {
		t.Fatal(err)
	}
}

func TestZipSnippet(t *testing.T) {
	s := NewWithoutTools(t.TempDir(), nil, quiet())
	src := newSource(t)
	s.src = src
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, name := range []string{"DCIM/", "DCIM/a.jpg", "DCIM/b.jpg", "WhatsApp/x/y.opus", "Contacts.vcf",
		"__MACOSX/._x", "../evil", "a", "b", "c", "d", "e", "f", "g"} {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		w.Write([]byte("x"))
	}
	zw.Close()
	sn := snippetOf(t, s, "phone.zip", src.add(t, buf.Bytes()))
	want := []string{"DCIM/", "WhatsApp/", "Contacts.vcf", "a", "b", "c", "d", "e"}
	if sn.Kind != "archive" || !slices.Equal(sn.Entries, want) || sn.Total != 10 {
		t.Fatalf("zip %+v", sn)
	}
	if size, err := zipDirectorySize(bytes.NewReader(buf.Bytes()), int64(buf.Len())); err != nil || size <= 0 {
		t.Fatalf("directory size %d %v", size, err)
	}
	if _, err := zipDirectorySize(strings.NewReader("not a zip"), 9); err == nil {
		t.Fatal("not a zip")
	}
}

func TestBackground(t *testing.T) {
	s, src, _ := newFaked(t)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { s.Start(ctx); close(done) }()
	defer func() { cancel(); <-done }()
	photo, text := src.add(t, jpegBytes), src.add(t, []byte("hello"))
	s.Saved("a.jpg", photo)
	s.Saved("b.txt", text)
	s.Saved("c.docx", src.add(t, []byte("doc")))
	deadline := time.Now().Add(5 * time.Second)
	for !(s.cache.exists(photo, imageSuffix(List)) && s.cache.exists(text, snippetSuffix)) {
		if time.Now().After(deadline) {
			t.Fatal("not made in the background")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestCleanup(t *testing.T) {
	s, src, _ := newFaked(t)
	ctx := context.Background()
	live, gone := src.add(t, jpegBytes), src.add(t, jpegBytes)
	for _, v := range []string{live, gone} {
		if _, err := s.Image(ctx, "a.jpg", v, Grid); err != nil {
			t.Fatal(err)
		}
	}
	link := src.add(t, nil)
	s.FailLink(link)
	old := time.Now().Add(-2 * linkRetry)
	os.Chtimes(s.cache.path(link, linkFailedSuffix), old, old)
	if s.LinkFailed(link) {
		t.Fatal("link failure older than an hour still counts")
	}
	legacy := s.cache.legacyLink(gone)
	os.MkdirAll(filepath.Dir(legacy), 0o750)
	os.WriteFile(legacy, jpegBytes, 0o640)
	stale, _ := s.cache.workDir()
	os.Chtimes(stale, old, old)
	os.MkdirAll(filepath.Join(s.cache.dir, "ab"), 0o750)
	other := filepath.Join(s.cache.dir, "ab", "README")
	os.WriteFile(other, nil, 0o640)

	src.gone[gone] = true
	n, err := s.Cleanup(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if n != 5 { // gone's two images and legacy image, the link failure, the work folder
		t.Fatalf("removed %d", n)
	}
	if !s.cache.exists(live, imageSuffix(Grid)) || s.cache.exists(gone, imageSuffix(Grid)) ||
		fileExists(legacy) || fileExists(stale) || fileExists(s.cache.path(link, linkFailedSuffix)) || !fileExists(other) {
		t.Fatal("wrong entries removed")
	}
}

func TestLinks(t *testing.T) {
	s, src, rec := newFaked(t)
	ctx := context.Background()
	v := db.NewID()
	if _, err := s.LinkImage(v, Grid); !errors.Is(err, ErrNone) {
		t.Fatal(err)
	}
	if err := s.MakeLink(ctx, v, []byte("<html>")); err == nil {
		t.Fatal("made a thumbnail of a web page")
	}
	if err := s.MakeLink(ctx, v, jpegBytes); err != nil {
		t.Fatal(err)
	}
	if _, err := s.LinkImage(v, List); err != nil {
		t.Fatal(err)
	}
	if _, err := s.LinkImage(v, Preview); !errors.Is(err, ErrNone) {
		t.Fatal("links have no preview")
	}
	if rec.ran(vips)[0][1] != "/dev/fd/3" {
		t.Fatal(rec.ran(vips))
	}
	// Links aren't made in the background: their image needs a lookup.
	s.Saved("Cats.url", src.add(t, nil))
	if len(s.queue) != 0 {
		t.Fatal("link queued")
	}
	// Recent failures are remembered for an hour.
	w := db.NewID()
	s.FailLink(w)
	if !s.LinkFailed(w) || s.Kind("Cats.url", w) != "" {
		t.Fatal("link failure")
	}
}

func TestSniff(t *testing.T) {
	for in, want := range map[string]string{
		string(jpegBytes):                    "jpeg",
		"\x89PNG\r\n\x1a\nxxxx":              "png",
		"GIF89a....":                         "gif",
		"RIFF\x00\x00\x00\x00WEBPVP8 ":       "webp",
		"II*\x00....":                        "tiff",
		"BM......":                           "bmp",
		string(heicBytes):                    "heif",
		"\x00\x00\x00\x1cftypavif\x00\x00":   "heif",
		string(mp4Bytes):                     "",
		"<svg":                               "",
		"%PDF-1.4":                           "",
		"\x00\x00\x00\x18ftypqt  \x00\x00..": "",
	} {
		if got := sniff([]byte(in)); got != want {
			t.Errorf("sniff(%q) = %q, want %q", in, got, want)
		}
	}
	if w, h, ok := headerSize([]byte("/dev/fd/3: 4032x3024 uchar, 3 bands, srgb, heifload\n")); !ok || w != 4032 || h != 3024 {
		t.Fatal(w, h, ok)
	}
	if _, _, ok := headerSize([]byte("vipsheader: unable to load")); ok {
		t.Fatal("no size")
	}
}

func TestSizes(t *testing.T) {
	for in, want := range map[string]bool{"": true, "grid": true, "list": true, "preview": false, "huge": false} {
		if _, ok := ParseSize(in); ok != want {
			t.Errorf("ParseSize(%q) = %v", in, ok)
		}
	}
}
