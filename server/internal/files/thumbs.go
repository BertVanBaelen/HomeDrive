package files

import (
	"context"
	"encoding/json"
	"net/url"
	"os"

	"github.com/BertVanBaelen/HomeDrive/server/internal/savedlink"
)

// Thumbnails is the thumbnail pipeline as the files service sees it
// (package thumbs): it says which thumbnail a version has and hears about
// new versions.
type Thumbnails interface {
	// Kind is "image", "snippet" or "" for a version of a file.
	Kind(name, versionID string) string
	// Saved is told about every new version, after it is stored.
	Saved(name, versionID string)
}

// SetThumbnails connects the thumbnail pipeline. Without it, nothing has
// a thumbnail.
func (s *Service) SetThumbnails(t Thumbnails) { s.thumbs = t }

// saved tells the pipeline about a new version.
func (s *Service) saved(name, versionID string) {
	if s.thumbs != nil {
		s.thumbs.Saved(name, versionID)
	}
}

// thumbKind is the thumbnail a file has. Saved links have one only when
// their site has a provider that gives images.
func (s *Service) thumbKind(kind, name, versionID string, link *LinkInfo) string {
	if s.thumbs == nil || kind != "file" || versionID == "" {
		return ""
	}
	if TypeOf(Node{Kind: kind, Name: name}) == "link" {
		u, err := url.Parse(linkURL(link))
		if link == nil || err != nil || !savedlink.HasPreview(u) {
			return ""
		}
	}
	return s.thumbs.Kind(name, versionID)
}

func linkURL(l *LinkInfo) string {
	if l == nil {
		return ""
	}
	return l.URL
}

// addThumbs sets which thumbnail each node and folder tile has, and the
// length of videos and audio. Call it after addLinks and addPreviews.
func (s *Service) addThumbs(ctx context.Context, nodes []Node) error {
	var versions []string
	for i := range nodes {
		n := &nodes[i]
		n.Thumb = s.thumbKind(n.Kind, n.Name, n.versionID, n.Link)
		if n.versionID != "" && (n.Type == "video" || n.Type == "audio") {
			versions = append(versions, n.versionID)
		}
		for j := range n.Preview {
			m := &n.Preview[j]
			if m.Type != "link" {
				m.Thumb = s.thumbKind(m.Kind, m.Name, m.Version, nil)
			}
		}
	}
	if len(versions) == 0 {
		return nil
	}
	list, err := json.Marshal(versions)
	if err != nil {
		return err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT version_id, duration_ms FROM media
		WHERE version_id IN (SELECT value FROM json_each(?))`, string(list))
	if err != nil {
		return err
	}
	defer rows.Close()
	lengths := map[string]int64{}
	for rows.Next() {
		var id string
		var ms int64
		if err := rows.Scan(&id, &ms); err != nil {
			return err
		}
		lengths[id] = ms
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for i := range nodes {
		nodes[i].DurationMs = lengths[nodes[i].versionID]
	}
	return nil
}

// OpenVersion opens a version's contents for the thumbnail pipeline. It
// checks no access: the pipeline only gets versions from listings and
// requests that were checked.
func (s *Service) OpenVersion(ctx context.Context, versionID string) (*os.File, error) {
	var objectID string
	if err := s.db.QueryRowContext(ctx, `SELECT object_id FROM versions WHERE id = ?`, versionID).Scan(&objectID); err != nil {
		return nil, err
	}
	return s.store.OpenObject(objectID)
}

// SetDuration stores the length of a video or audio version.
func (s *Service) SetDuration(ctx context.Context, versionID string, ms int64) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO media (version_id, duration_ms) VALUES (?, ?)
		ON CONFLICT (version_id) DO UPDATE SET duration_ms = excluded.duration_ms`, versionID, ms)
	return err
}

// LiveVersions reports which of ids are versions that still exist, for
// the thumbnail cache's cleanup.
func (s *Service) LiveVersions(ctx context.Context, ids []string) (map[string]bool, error) {
	list, err := json.Marshal(ids)
	if err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id FROM versions WHERE id IN (SELECT value FROM json_each(?))`, string(list))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	live := map[string]bool{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		live[id] = true
	}
	return live, rows.Err()
}
