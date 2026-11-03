package files

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/url"

	"github.com/BertVanBaelen/HomeDrive/server/internal/savedlink"
)

// LinkInfo is where a saved link goes, for its card. Site is the provider
// or the host; Author is known only from a provider.
type LinkInfo struct {
	URL    string `json:"url"`
	Site   string `json:"site"`
	Author string `json:"author,omitempty"`
}

// linkTarget reads where a saved link goes from its contents, or nil when
// the file isn't a .url file or holds no usable address.
func (s *Service) linkTarget(name, objectID string, size int64) *url.URL {
	if !savedlink.IsLink(name) || size > savedlink.MaxFile {
		return nil
	}
	f, err := s.store.OpenObject(objectID)
	if err != nil {
		return nil
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, savedlink.MaxFile+1))
	if err != nil {
		return nil
	}
	u, err := savedlink.Read(b)
	if err != nil {
		return nil
	}
	return u
}

// indexLink records where a saved link goes when its version is saved, or
// forgets it when the contents hold no address.
func indexLink(ctx context.Context, q querier, nodeID string, u *url.URL) error {
	if u == nil {
		_, err := q.ExecContext(ctx, `DELETE FROM link_previews WHERE node_id = ?`, nodeID)
		return err
	}
	_, err := q.ExecContext(ctx, `INSERT INTO link_previews (node_id, url, site) VALUES (?, ?, ?)
		ON CONFLICT (node_id) DO UPDATE SET url = excluded.url, site = excluded.site, author = '', fetched_at = NULL`,
		nodeID, u.String(), savedlink.Site(u))
	return err
}

// addLinks sets Link on the saved links among nodes, in one query.
func (s *Service) addLinks(ctx context.Context, nodes []Node) error {
	var ids []string
	for _, n := range nodes {
		if n.Type == "link" {
			ids = append(ids, n.ID)
		}
	}
	if len(ids) == 0 {
		return nil
	}
	list, err := json.Marshal(ids)
	if err != nil {
		return err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT node_id, url, site, author FROM link_previews
		WHERE node_id IN (SELECT value FROM json_each(?))`, string(list))
	if err != nil {
		return err
	}
	defer rows.Close()
	links := map[string]*LinkInfo{}
	for rows.Next() {
		var id string
		var l LinkInfo
		if err := rows.Scan(&id, &l.URL, &l.Site, &l.Author); err != nil {
			return err
		}
		links[id] = &l
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for i := range nodes {
		nodes[i].Link = links[nodes[i].ID]
	}
	return nil
}

// SetLinkAuthor stores the author a provider gave for a saved link. The
// caller has checked access to the link already.
func (s *Service) SetLinkAuthor(ctx context.Context, nodeID, author string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE link_previews SET author = ?, fetched_at = ? WHERE node_id = ?`,
		author, s.stamp(), nodeID)
	return err
}

// SaveLinkToInbox stores a link shared from a device as a .url file in the
// user's Inbox, named after its title, with the shared text as its
// description.
func (s *Service) SaveLinkToInbox(ctx context.Context, userID string, u *url.URL, title, description string) (Node, error) {
	return s.UploadToInbox(ctx, userID, savedlink.FileName(title), description, bytes.NewReader(savedlink.Shortcut(u)))
}
