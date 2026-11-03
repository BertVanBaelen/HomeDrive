package files

import (
	"context"
	"errors"
	"strings"
	"unicode"
	"unicode/utf8"
)

// MaxDescription is the longest description, in characters.
const MaxDescription = 5000

var ErrInvalidDescription = errors.New("invalid description")

// normalizeDescription trims text and keeps it plain: line breaks and tabs
// stay, other control characters go, Windows line endings become \n.
func normalizeDescription(s string) string {
	s = strings.ToValidUTF8(s, "")
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.Map(func(r rune) rune {
		switch {
		case r == '\n' || r == '\t':
			return r
		case r == '\r':
			return '\n'
		case unicode.IsControl(r):
			return -1
		}
		return r
	}, s)
	return strings.TrimSpace(s)
}

// ValidateDescription cleans a description someone typed and checks its
// length.
func ValidateDescription(s string) (string, error) {
	s = normalizeDescription(s)
	if utf8.RuneCountInString(s) > MaxDescription {
		return "", ErrInvalidDescription
	}
	return s, nil
}

// ClipDescription cleans text shared from another app into a description,
// cutting it at the maximum length instead of refusing it.
func ClipDescription(s string) string {
	s = normalizeDescription(s)
	if utf8.RuneCountInString(s) > MaxDescription {
		s = strings.TrimSpace(string([]rune(s)[:MaxDescription]))
	}
	return s
}

func descriptionKey(s string) string { return strings.ToLower(s) }

// SetDescription changes an item's description. Anyone who may edit the
// item may describe it. It isn't a change of the contents, so the item's
// modification time stays.
func (s *Service) SetDescription(ctx context.Context, userID, nodeID, text string) (Node, error) {
	text, err := ValidateDescription(text)
	if err != nil {
		return Node{}, err
	}
	it, err := s.lookup(ctx, s.db, userID, nodeID, false)
	if err != nil {
		return Node{}, err
	}
	if it.ParentID == "" {
		return Node{}, ErrRoot
	}
	if !it.canEdit() {
		return Node{}, ErrReadOnly
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE nodes SET description = ?, description_key = ? WHERE id = ?`,
		text, descriptionKey(text), it.ID); err != nil {
		return Node{}, err
	}
	return s.node(ctx, userID, it.ID)
}
