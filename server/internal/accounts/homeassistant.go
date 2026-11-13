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
//
// An admin can also add someone with only their Home Assistant username
// (AddHAUser): such an account has no Homedrive password (User.HAOnly), is
// a member, and confirms sensitive actions with its Home Assistant
// password. It becomes a normal account once its owner chooses a password
// through an emailed link.

var (
	ErrInvalidHAUsername = errors.New("invalid Home Assistant username")
	ErrHAUsernameTaken   = errors.New("Home Assistant username linked to another account")
	// ErrHAOnlyAdmin: an account without a Homedrive password can't be an
	// admin, whose actions need a password to confirm.
	ErrHAOnlyAdmin = errors.New("an account without a password can't be an admin")
	// ErrHAOnlyUnlink: unlinking an account without a Homedrive password
	// would leave it no way to log in.
	ErrHAOnlyUnlink = errors.New("an account without a password must stay linked to Home Assistant")
	// ErrNoPassword: there is no current password to change.
	ErrNoPassword = errors.New("the account has no password")
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
// give ErrInvalidCredentials, as a wrong password does: an invitation
// stays the way to choose a password. Accounts added as Home Assistant
// users have no password and do log in.
func (s *Service) HAUser(ctx context.Context, username string) (User, error) {
	u, err := scanUser(s.db.QueryRowContext(ctx,
		`SELECT `+userColumns+` FROM users WHERE ha_username = ? AND disabled_at IS NULL AND (password_hash != '' OR ha_only = 1)`, username))
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, ErrInvalidCredentials
	}
	return u, err
}

// AddHAUser adds an account that logs in with Home Assistant only: linked
// to the Home Assistant username at once, without a Homedrive password and
// without sending an email (its login codes still go to email). It is
// always a member. The new user joins the shared drives in driveIDs as
// InviteUser's do.
func (s *Service) AddHAUser(ctx context.Context, email, name, haUsername, locale string, driveIDs []string) (UserInfo, error) {
	email, err := NormalizeEmail(email)
	if err != nil {
		return UserInfo{}, err
	}
	if name, err = NormalizeName(name); err != nil {
		return UserInfo{}, err
	}
	username, err := NormalizeHAUsername(haUsername)
	if err != nil {
		return UserInfo{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return UserInfo{}, err
	}
	defer tx.Rollback()
	u, err := s.createUser(ctx, tx, email, name, "", "member", locale, driveIDs)
	if err != nil {
		return UserInfo{}, err
	}
	_, err = tx.ExecContext(ctx, `UPDATE users SET ha_username = ?, ha_only = 1 WHERE id = ?`, username, u.ID)
	if db.IsUniqueViolation(err) {
		return UserInfo{}, ErrHAUsernameTaken
	}
	if err != nil {
		return UserInfo{}, err
	}
	if err := tx.Commit(); err != nil {
		return UserInfo{}, err
	}
	return s.userInfo(ctx, s.db, u.ID)
}

// ReauthHomeAssistant is Reauth for an account without a Homedrive
// password, once the Supervisor has confirmed the Home Assistant password
// of username. It only counts while the account is still linked to that
// username and has no password.
func (s *Service) ReauthHomeAssistant(ctx context.Context, sess Session, username string) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE sessions SET recent_auth_at = ? WHERE id_hash = ? AND user_id IN
		 (SELECT id FROM users WHERE ha_username = ? AND ha_only = 1 AND disabled_at IS NULL)`,
		db.FormatTime(s.now()), sess.hash, username)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrWrongPassword
	}
	return nil
}

// HAUsername is the Home Assistant username linked to a user, or "".
func (s *Service) HAUsername(ctx context.Context, userID string) (string, error) {
	var name sql.NullString
	err := s.db.QueryRowContext(ctx, `SELECT ha_username FROM users WHERE id = ?`, userID).Scan(&name)
	return name.String, err
}

// SetHAUsername links a user to a Home Assistant username, or unlinks them
// when username is empty. An account without a Homedrive password can't be
// unlinked (ErrHAOnlyUnlink).
func (s *Service) SetHAUsername(ctx context.Context, userID, username string) error {
	return setHAUsername(ctx, s.db, userID, username)
}

func setHAUsername(ctx context.Context, q interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, userID, username string) error {
	var value any
	if strings.TrimSpace(username) != "" {
		name, err := NormalizeHAUsername(username)
		if err != nil {
			return err
		}
		value = name
	} else {
		var haOnly bool
		err := q.QueryRowContext(ctx, `SELECT ha_only FROM users WHERE id = ?`, userID).Scan(&haOnly)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if haOnly {
			return ErrHAOnlyUnlink
		}
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
