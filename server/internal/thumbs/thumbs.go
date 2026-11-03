// Package thumbs makes the previews that cards and the viewer show instead
// of icons (docs/architecture.md §4, "Thumbnails and previews"): WebP
// images of photos, video posters, audio covers and PDF first pages, made
// by libvips, ffmpeg and poppler in sandboxed subprocesses, and snippets of
// text, spreadsheets and zip archives, read in Go. Everything is cached by
// file version under <state dir>/cache/thumbs; the cache can be deleted at
// any time.
package thumbs

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path"
	"strings"
	"sync"
	"time"
)

// Kinds of thumbnail a file can have, as listings tell the web app.
const (
	KindImage   = "image"
	KindSnippet = "snippet"
)

// Size is one of the image sizes: the longest side in pixels.
type Size string

const (
	Grid    Size = "grid"    // cards and the info panel
	List    Size = "list"    // small places: folder tiles, dialogs
	Preview Size = "preview" // the viewer, for photos browsers can't show
)

var sizePixels = map[Size]int{Grid: 400, List: 80, Preview: 2048}

// ParseSize reads the size parameter of a request; empty means grid.
func ParseSize(s string) (Size, bool) {
	if s == "" {
		return Grid, true
	}
	_, ok := sizePixels[Size(s)]
	return Size(s), ok && Size(s) != Preview
}

// ErrNone means a version has no thumbnail of the kind asked for: its type
// has none, the tools are missing, making it failed, or it took too long.
var ErrNone = errors.New("no thumbnail")

const (
	// waitFor is how long a request waits for a thumbnail being made.
	waitFor = 15 * time.Second
	// jobTimeout bounds all the tool runs for one version.
	jobTimeout = 2 * time.Minute
	// slots is how many tools run at the same time.
	slots = 2
	// queueSize is how many new versions may wait for the background
	// worker; more are made when they are first asked for.
	queueSize = 256
)

// Source gives the pipeline the file versions it works on. The files
// service implements it; the caller has checked access already.
type Source interface {
	// OpenVersion opens the contents of a version.
	OpenVersion(ctx context.Context, versionID string) (*os.File, error)
	// SetDuration stores the length of a video or audio version.
	SetDuration(ctx context.Context, versionID string, ms int64) error
	// LiveVersions reports which of ids are still versions of a file.
	LiveVersions(ctx context.Context, ids []string) (map[string]bool, error)
}

// Service makes, caches and finds thumbnails.
type Service struct {
	src   Source
	log   *slog.Logger
	cache cache
	tools tools
	// fake replaces the tools in tests.
	fake FakeTool

	slots chan struct{}
	queue chan job

	mu       sync.Mutex
	inflight map[string]chan struct{}
}

type job struct{ name, version string }

// New sets up the pipeline on the state dir and logs which tools are
// missing, if any. Start runs the background worker.
func New(stateDir string, src Source, log *slog.Logger) *Service {
	s := newService(stateDir, src, log, findTools())
	if missing := s.tools.missing(); len(missing) > 0 {
		log.Warn("thumbnail tools missing: these files show placeholders instead of previews",
			"missing", strings.Join(missing, ", "), "affects", s.tools.affected())
	}
	return s
}

// NewWithoutTools is a pipeline that makes snippets only, as on a machine
// without the tools, without logging about them.
func NewWithoutTools(stateDir string, src Source, log *slog.Logger) *Service {
	return newService(stateDir, src, log, tools{paths: map[string]string{}})
}

func newService(stateDir string, src Source, log *slog.Logger, t tools) *Service {
	return &Service{
		src:      src,
		log:      log,
		cache:    newCache(stateDir),
		tools:    t,
		slots:    make(chan struct{}, slots),
		queue:    make(chan job, queueSize),
		inflight: map[string]chan struct{}{},
	}
}

// Start makes the thumbnails of new versions in the background until ctx
// ends, one version at a time, so a request always finds a free tool.
func (s *Service) Start(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case j := <-s.queue:
			if s.has(j.name, j.version) {
				continue
			}
			select {
			case <-s.make(j.name, j.version):
			case <-ctx.Done():
				return
			}
		}
	}
}

// Saved is told about every new file version. Its thumbnail is made in the
// background; when the queue is full it waits until someone asks for it.
func (s *Service) Saved(name, versionID string) {
	if s.Kind(name, versionID) == "" || typeOf(name).link {
		return
	}
	select {
	case s.queue <- job{name, versionID}:
	default:
	}
}

// Kind is the thumbnail a file version has: KindImage, KindSnippet or "".
// It is decided by the file's type and the tools at hand, before anything
// is made; a version that failed has none.
func (s *Service) Kind(name, versionID string) string {
	if !validVersion(versionID) {
		return ""
	}
	t := typeOf(name)
	switch {
	case t.snippet != "":
		if s.cache.failed(versionID) {
			return ""
		}
		return KindSnippet
	case t.link:
		if !s.tools.has(t.needs) || s.cache.linkFailed(versionID) {
			return ""
		}
		return KindImage
	case t.image != "":
		if !s.tools.has(t.needs) || s.cache.failed(versionID) {
			return ""
		}
		return KindImage
	}
	return ""
}

// HasPreview reports whether the viewer shows a version through its
// preview image instead of the file itself (HEIC and TIFF photos).
func (s *Service) HasPreview(name, versionID string) bool {
	return typeOf(name).preview && s.Kind(name, versionID) == KindImage
}

// has reports whether everything for a version is made (or failed).
func (s *Service) has(name, versionID string) bool {
	if s.cache.failed(versionID) {
		return true
	}
	if typeOf(name).snippet != "" {
		return s.cache.exists(versionID, snippetSuffix)
	}
	return s.cache.exists(versionID, imageSuffix(List))
}

// Image returns the path of a version's image at a size, making it if
// needed. A request waits up to 15 s for it.
func (s *Service) Image(ctx context.Context, name, versionID string, size Size) (string, error) {
	t := typeOf(name)
	if s.Kind(name, versionID) != KindImage || t.link || size == Preview && !t.preview {
		return "", ErrNone
	}
	return s.get(ctx, name, versionID, imageSuffix(size))
}

// Snippet returns a version's snippet as JSON, making it if needed.
func (s *Service) Snippet(ctx context.Context, name, versionID string) ([]byte, error) {
	if s.Kind(name, versionID) != KindSnippet {
		return nil, ErrNone
	}
	p, err := s.get(ctx, name, versionID, snippetSuffix)
	if err != nil {
		return nil, err
	}
	return os.ReadFile(p)
}

func (s *Service) get(ctx context.Context, name, versionID, suffix string) (string, error) {
	p := s.cache.path(versionID, suffix)
	if fileExists(p) {
		return p, nil
	}
	wait, cancel := context.WithTimeout(ctx, waitFor)
	defer cancel()
	select {
	case <-s.make(name, versionID):
	case <-wait.Done():
		return "", ErrNone
	}
	if fileExists(p) {
		return p, nil
	}
	return "", ErrNone
}

// make starts making a version's thumbnails, unless that is already
// happening, and returns a channel that closes when it is done. The work
// isn't tied to the request that asked, so a closed tab doesn't waste it.
func (s *Service) make(name, versionID string) <-chan struct{} {
	s.mu.Lock()
	defer s.mu.Unlock()
	if done, ok := s.inflight[versionID]; ok {
		return done
	}
	done := make(chan struct{})
	s.inflight[versionID] = done
	go func() {
		defer func() {
			s.mu.Lock()
			delete(s.inflight, versionID)
			s.mu.Unlock()
			close(done)
		}()
		s.build(name, versionID)
	}()
	return done
}

// build makes everything a version has. A version that can't be made gets
// a failure marker and isn't tried again.
func (s *Service) build(name, versionID string) {
	if s.has(name, versionID) {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), jobTimeout)
	defer cancel()
	f, err := s.src.OpenVersion(ctx, versionID)
	if err != nil {
		// Gone, or the data disk is away: nothing to remember.
		s.log.Debug("thumbnail: opening version", "version", versionID, "error", err)
		return
	}
	defer f.Close()
	t := typeOf(name)
	if t.snippet != "" {
		err = s.buildSnippet(t.snippet, f, versionID)
	} else {
		err = s.buildImage(ctx, t, f, versionID)
	}
	if err == nil {
		return
	}
	if !errors.Is(err, errNoCover) {
		s.log.Info("no thumbnail for a file", "type", path.Ext(strings.ToLower(name)), "version", versionID, "error", err)
	}
	if err := s.cache.fail(versionID); err != nil {
		s.log.Warn("thumbnail: storing a failure", "error", err)
	}
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}
