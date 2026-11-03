package accounts

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/BertVanBaelen/HomeDrive/server/internal/db"
)

// Home Assistant login (add-on only): a user can be linked to one Home
// Assistant username, which they may then log in with. The password is
// checked by the Supervisor; Homedrive only keeps the username.

var (
	ErrInvalidHAUsername = errors.New("invalid Home Assistant username")
	ErrHAUsernameTaken   = errors.New("Home Assistant username linked to another account")
)

// NormalizeHAUsername trims and lower-cases a Home Assistant username, the
// way Home Assistant compares them.
func NormalizeHAUsername(s string) (string, error) {
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "" || utf8.RuneCountInString(s) > 100 || strings.ContainsFunc(s, unicode.IsControl) {
		return "", ErrInvalidHAUsername
	}
	return s, nil
}

// HAUser is the account linked to a normalized Home Assistant username.
// Unlinked usernames, disabled accounts and invitations not accepted yet
// give ErrInvalidCredentials, as a wrong password does. (Without a
// Homedrive password, sensitive actions couldn't be confirmed.)
func (s *Service) HAUser(ctx context.Context, username string) (User, error) {
	u, err := scanUser(s.db.QueryRowContext(ctx,
		`SELECT `+userColumns+` FROM users WHERE ha_username = ? AND disabled_at IS NULL AND password_hash != ''`, username))
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, ErrInvalidCredentials
	}
	return u, err
}

// HAUsername is the Home Assistant username linked to a user, or "".
func (s *Service) HAUsername(ctx context.Context, userID string) (string, error) {
	var name sql.NullString
	err := s.db.QueryRowContext(ctx, `SELECT ha_username FROM users WHERE id = ?`, userID).Scan(&name)
	return name.String, err
}

// SetHAUsername links a user to a Home Assistant username, or unlinks them
// when username is empty.
func (s *Service) SetHAUsername(ctx context.Context, userID, username string) error {
	return setHAUsername(ctx, s.db, userID, username)
}

func setHAUsername(ctx context.Context, q interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}, userID, username string) error {
	var value any
	if strings.TrimSpace(username) != "" {
		name, err := NormalizeHAUsername(username)
		if err != nil {
			return err
		}
		value = name
	}
	res, err := q.ExecContext(ctx, `UPDATE users SET ha_username = ? WHERE id = ?`, value, userID)
	if db.IsUniqueViolation(err) {
		return ErrHAUsernameTaken
	}
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}
