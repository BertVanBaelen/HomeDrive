package thumbs

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
)

// maxPixels is the largest image, by its header, that gets a thumbnail.
const maxPixels = 100_000_000

// errNoCover is an audio file without cover art: it shows the waveform
// card, which isn't a failure worth logging.
var errNoCover = errors.New("no cover art")

// demuxers are the formats ffmpeg may read: no playlists (HLS, concat)
// that could point at other files or the network.
const demuxers = "mov,matroska,avi,mpeg,mp3,aac,flac,ogg,wav"

// buildImage makes the images of a photo, video, audio file or PDF: the
// grid and list sizes, plus the viewer's preview for HEIC and TIFF.
func (s *Service) buildImage(ctx context.Context, t fileType, f *os.File, versionID string) error {
	dir, err := s.cache.workDir()
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)

	// src is what the grid image is made from: the file itself (fd 3)
	// or something the first step wrote.
	src, input := "/dev/fd/3", f
	switch t.image {
	case "photo":
		if src, input, err = s.photo(ctx, t, f, dir, versionID); err != nil {
			return err
		}
	case "video", "audio":
		if src, err = s.frame(ctx, t, f, dir, versionID); err != nil {
			return err
		}
		input = nil
	case "pdf":
		if _, err := s.run(ctx, f, dir, pdftoppm, "-f", "1", "-l", "1", "-singlefile", "-png",
			"-scale-to", "1200", "/dev/fd/3", "page"); err != nil {
			return err
		}
		src, input = filepath.Join(dir, "page.png"), nil
	}
	return s.sizes(ctx, input, src, dir, versionID)
}

// sizes makes the grid image from src and the list image from the grid
// one, and keeps both.
func (s *Service) sizes(ctx context.Context, input *os.File, src, dir, versionID string) error {
	grid := filepath.Join(dir, "grid.webp")
	if err := s.thumbnail(ctx, input, dir, src, grid, sizePixels[Grid]); err != nil {
		return err
	}
	list := filepath.Join(dir, "list.webp")
	if err := s.thumbnail(ctx, nil, dir, grid, list, sizePixels[List]); err != nil {
		return err
	}
	if err := s.cache.keep(grid, versionID, imageSuffix(Grid)); err != nil {
		return err
	}
	return s.cache.keep(list, versionID, imageSuffix(List))
}

// thumbnail runs `vips thumbnail`: at most px on the longest side, never
// larger than the source, upright from EXIF, without metadata.
func (s *Service) thumbnail(ctx context.Context, input *os.File, dir, src, dst string, px int) error {
	size := strconv.Itoa(px)
	_, err := s.run(ctx, input, dir, vips, "thumbnail", src, dst+"[Q=80,keep=none]", size,
		"--height", size, "--size", "down")
	return err
}

// photo checks a photo before libvips decodes it: its first bytes must be
// a format on the list, and its header at most maxPixels. HEIC and TIFF
// get their preview first, which the grid image is then made from; BMP,
// which libvips reads only through ImageMagick, goes through ffmpeg.
func (s *Service) photo(ctx context.Context, t fileType, f *os.File, dir, versionID string) (string, *os.File, error) {
	head := make([]byte, 64)
	n, err := io.ReadFull(f, head)
	if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) {
		return "", nil, err
	}
	format := sniff(head[:n])
	if format == "" {
		return "", nil, errors.New("not an image format on the list")
	}
	if format == "bmp" {
		out := filepath.Join(dir, "frame.png")
		if _, err := s.run(ctx, f, dir, ffmpeg, "-nostdin", "-v", "error", "-threads", "1",
			"-protocol_whitelist", "fd", "-format_whitelist", "bmp_pipe", "-f", "bmp_pipe",
			"-max_pixels", strconv.Itoa(maxPixels), "-fd", "3", "-i", "fd:",
			"-frames:v", "1", "-f", "image2", "-c:v", "png", out); err != nil {
			return "", nil, err
		}
		return out, nil, nil
	}
	out, err := s.run(ctx, f, dir, vipsheader, "/dev/fd/3")
	if err != nil {
		return "", nil, err
	}
	w, h, ok := headerSize(out)
	if !ok {
		return "", nil, errors.New("unreadable image header")
	}
	if w*h > maxPixels {
		return "", nil, fmt.Errorf("image of %d×%d pixels is too large", w, h)
	}
	if !t.preview {
		return "/dev/fd/3", f, nil
	}
	preview := filepath.Join(dir, "preview.webp")
	if err := s.thumbnail(ctx, f, dir, "/dev/fd/3", preview, sizePixels[Preview]); err != nil {
		return "", nil, err
	}
	if err := s.cache.keep(preview, versionID, imageSuffix(Preview)); err != nil {
		return "", nil, err
	}
	return s.cache.path(versionID, imageSuffix(Preview)), nil, nil
}

// sniff names an image format from its first bytes, or "" when it isn't
// one libvips may read here.
func sniff(b []byte) string {
	switch {
	case bytes.HasPrefix(b, []byte{0xff, 0xd8, 0xff}):
		return "jpeg"
	case bytes.HasPrefix(b, []byte("\x89PNG\r\n\x1a\n")):
		return "png"
	case bytes.HasPrefix(b, []byte("GIF87a")), bytes.HasPrefix(b, []byte("GIF89a")):
		return "gif"
	case len(b) >= 12 && string(b[:4]) == "RIFF" && string(b[8:12]) == "WEBP":
		return "webp"
	case bytes.HasPrefix(b, []byte("II*\x00")), bytes.HasPrefix(b, []byte("MM\x00*")):
		return "tiff"
	case bytes.HasPrefix(b, []byte("BM")):
		return "bmp"
	case len(b) >= 12 && string(b[4:8]) == "ftyp":
		switch string(b[8:12]) {
		case "heic", "heix", "heim", "heis", "hevc", "hevx", "mif1", "msf1", "avif", "avis":
			return "heif"
		}
	}
	return ""
}

// headerSize reads "…: 4032x3024 uchar, 3 bands, srgb, jpegload" from
// vipsheader.
var headerRE = regexp.MustCompile(`(\d+)x(\d+)`)

func headerSize(out []byte) (w, h int, ok bool) {
	m := headerRE.FindSubmatch(out)
	if m == nil {
		return 0, 0, false
	}
	w, err1 := strconv.Atoi(string(m[1]))
	h, err2 := strconv.Atoi(string(m[2]))
	return w, h, err1 == nil && err2 == nil && w > 0 && h > 0
}

// probe is what ffprobe tells about a video or audio file.
type probe struct {
	Format struct {
		Duration string `json:"duration"`
	} `json:"format"`
	Streams []struct {
		CodecType   string `json:"codec_type"`
		Disposition struct {
			AttachedPic int `json:"attached_pic"`
		} `json:"disposition"`
	} `json:"streams"`
}

// ffInput are the options before ffmpeg's and ffprobe's input: only the
// listed formats, only the descriptor, no huge frames.
func ffInput() []string {
	return []string{"-protocol_whitelist", "fd", "-format_whitelist", demuxers,
		"-max_pixels", strconv.Itoa(maxPixels), "-fd", "3"}
}

// frame stores a video's or audio file's length and writes a still: a
// video frame at 10 % of the length (at most 30 s in), or the cover art.
func (s *Service) frame(ctx context.Context, t fileType, f *os.File, dir, versionID string) (string, error) {
	args := append([]string{"-v", "error"}, ffInput()...)
	args = append(args, "-show_entries", "format=duration:stream=codec_type:stream_disposition=attached_pic",
		"-of", "json", "fd:")
	out, err := s.run(ctx, f, dir, ffprobe, args...)
	if err != nil {
		return "", err
	}
	var p probe
	if err := json.Unmarshal(out, &p); err != nil {
		return "", fmt.Errorf("reading ffprobe: %w", err)
	}
	seconds, err := strconv.ParseFloat(p.Format.Duration, 64)
	if err == nil && seconds > 0 && !math.IsInf(seconds, 0) {
		if err := s.src.SetDuration(ctx, versionID, int64(math.Round(seconds*1000))); err != nil {
			return "", err
		}
	} else {
		seconds = 0
	}

	hasVideo, hasCover := false, false
	for _, st := range p.Streams {
		if st.CodecType == "video" {
			if st.Disposition.AttachedPic == 1 {
				hasCover = true
			} else {
				hasVideo = true
			}
		}
	}
	args = []string{"-nostdin", "-v", "error", "-threads", "1"}
	switch {
	case t.image == "video" && hasVideo:
		at := min(seconds*0.1, 30)
		args = append(args, "-ss", strconv.FormatFloat(at, 'f', 3, 64))
	case hasCover:
	default:
		return "", errNoCover
	}
	still := filepath.Join(dir, "frame.png")
	args = append(args, ffInput()...)
	args = append(args, "-i", "fd:", "-map", "0:v:0", "-an", "-sn", "-dn", "-frames:v", "1",
		"-vf", "scale=w='min(1200,iw)':h=-1", "-f", "image2", "-c:v", "png", still)
	if _, err := s.run(ctx, f, dir, ffmpeg, args...); err != nil {
		return "", err
	}
	if !fileExists(still) {
		// A seek past the last frame writes nothing.
		return "", errors.New("no frame")
	}
	return still, nil
}

// LinkImage is the image of a saved link at a size, from the cache.
// Visitors and requests that may not ask the provider use only this.
func (s *Service) LinkImage(versionID string, size Size) (string, error) {
	if size == Preview || !s.cache.exists(versionID, imageSuffix(size)) {
		return "", ErrNone
	}
	return s.cache.path(versionID, imageSuffix(size)), nil
}

// LinkFailed reports whether a saved link's image couldn't be fetched in
// the last hour, so it isn't asked for again yet.
func (s *Service) LinkFailed(versionID string) bool { return s.cache.linkFailed(versionID) }

// FailLink remembers that a saved link's image couldn't be fetched.
func (s *Service) FailLink(versionID string) {
	if err := s.cache.mark(versionID, linkFailedSuffix); err != nil {
		s.log.Warn("thumbnail: storing a link failure", "error", err)
	}
}

// LegacyLink returns a saved link's image as v2 cached it before this
// pipeline, so it can be made into thumbnails without asking the provider
// again, or nil.
func (s *Service) LegacyLink(versionID string) []byte {
	b, err := os.ReadFile(s.cache.legacyLink(versionID))
	if err != nil {
		return nil
	}
	return b
}

// MakeLink makes the grid and list images of a saved link from its
// provider's image (JPEG, PNG, GIF or WebP, at most a few MB).
func (s *Service) MakeLink(ctx context.Context, versionID string, image []byte) error {
	if !s.tools.has(link.needs) || !validVersion(versionID) {
		return ErrNone
	}
	switch sniff(image) {
	case "jpeg", "png", "gif", "webp":
	default:
		return errors.New("not an image")
	}
	ctx, cancel := context.WithTimeout(ctx, jobTimeout)
	defer cancel()
	dir, err := s.cache.workDir()
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	src := filepath.Join(dir, "provider")
	if err := os.WriteFile(src, image, 0o640); err != nil {
		return err
	}
	f, err := os.Open(src)
	if err != nil {
		return err
	}
	defer f.Close()
	if err := s.sizes(ctx, f, "/dev/fd/3", dir, versionID); err != nil {
		return err
	}
	os.Remove(s.cache.legacyLink(versionID))
	return nil
}
