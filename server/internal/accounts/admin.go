package accounts

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/BertVanBaelen/HomeDrive/server/internal/auth"
	"github.com/BertVanBaelen/HomeDrive/server/internal/db"
)

// There is no self-registration: an admin adds people, who get an email
// with a link to choose their password. A password reset works the same
// way, because email is already the second factor; a reset link sent on
// request would turn sign-in into one factor.

var (
	ErrNotFound     = errors.New("user not found")
	ErrInvalidLink  = errors.New("link invalid or expired")
	ErrOwnAccount   = errors.New("can't do this to your own account")
	ErrLastAdmin    = errors.New("at least one active admin is needed")
	ErrUserDisabled = errors.New("account is disabled")
	ErrMailFailed   = errors.New("email could not be sent")
	ErrInvalidRole  = errors.New("role must be admin or member")
)

const (
	inviteValidity = 7 * 24 * time.Hour
	resetValidity  = 24 * time.Hour
)

// UserInfo is a user as the admin page shows them.
type UserInfo struct {
	User
	Status     string     `json:"status"` // active | invited | disabled
	LastSeenAt *time.Time `json:"lastSeenAt,omitempty"`
}

const userInfoQuery = `SELECT u.id, u.email, u.name, u.role, u.locale, u.created_at,
	u.password_hash = '', u.disabled_at IS NOT NULL,
	(SELECT max(last_seen_at) FROM sessions s WHERE s.user_id = u.id)
	FROM users u`

func scanUserInfo(row interface{ Scan(...any) error }) (UserInfo, error) {
	var (
		u                 UserInfo
		created           string
		invited, disabled bool
		seen              sql.NullString
	)
	if err := row.Scan(&u.ID, &u.Email, &u.Name, &u.Role, &u.Locale, &created, &invited, &disabled, &seen); err != nil {
		return UserInfo{}, err
	}
	u.CreatedAt, _ = db.ParseTime(created)
	switch {
	case disabled:
		u.Status = "disabled"
	case invited:
		u.Status = "invited"
	default:
		u.Status = "active"
	}
	if seen.Valid {
		t, _ := db.ParseTime(seen.String)
		u.LastSeenAt = &t
	}
	return u, nil
}

func (s *Service) ListUsers(ctx context.Context) ([]UserInfo, error) {
	rows, err := s.db.QueryContext(ctx, userInfoQuery+` ORDER BY lower(u.name), u.email`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []UserInfo{}
	for rows.Next() {
		u, err := scanUserInfo(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// Person is someone drives and items can be shared with.
type Person struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Email string `json:"email"`
}

// People lists every account that isn't disabled, including people who
// haven't accepted their invitation yet.
func (s *Service) People(ctx context.Context) ([]Person, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, name, email FROM users WHERE disabled_at IS NULL ORDER BY lower(name), email`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Person{}
	for rows.Next() {
		var p Person
		if err := rows.Scan(&p.ID, &p.Name, &p.Email); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// rowQuerier is satisfied by *sql.DB and *sql.Tx.
type rowQuerier interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

func (s *Service) userInfo(ctx context.Context, q rowQuerier, id string) (UserInfo, error) {
	if !db.ValidID(id) {
		return UserInfo{}, ErrNotFound
	}
	u, err := scanUserInfo(q.QueryRowContext(ctx, userInfoQuery+` WHERE u.id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return UserInfo{}, ErrNotFound
	}
	return u, err
}

// InviteUser adds an account and emails its owner a link to choose a
// password. The new user joins the shared drives in driveIDs as an editor;
// nil means the drives marked join by default. If the email fails the
// account still exists; the admin can send the link again.
func (s *Service) InviteUser(ctx context.Context, admin User, email, name, role, locale string, driveIDs []string) (UserInfo, error) {
	email, err := NormalizeEmail(email)
	if err != nil {
		return UserInfo{}, err
	}
	if name, err = NormalizeName(name); err != nil {
		return UserInfo{}, err
	}
	if role != "admin" && role != "member" {
		return UserInfo{}, ErrInvalidRole
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return UserInfo{}, err
	}
	defer tx.Rollback()
	u, err := s.createUser(ctx, tx, email, name, "", role, locale, driveIDs)
	if err != nil {
		return UserInfo{}, err
	}
	token, err := s.newLink(ctx, tx, u.ID, "invite", admin.ID)
	if err != nil {
		return UserInfo{}, err
	}
	if err := tx.Commit(); err != nil {
		return UserInfo{}, err
	}
	info, err := s.userInfo(ctx, s.db, u.ID)
	if err != nil {
		return UserInfo{}, err
	}
	if err := s.mail.Send(ctx, linkMessage(u, admin.Name, s.linkURL(token), "invite")); err != nil {
		s.log.Error("sending invitation", "to", u.Email, "error", err)
		return info, ErrMailFailed
	}
	return info, nil
}

func (s *Service) linkURL(token string) string { return s.origin + "/welcome#" + token }

// newLink creates a password link for a user inside tx. Earlier links for
// the same user stop working.
func (s *Service) newLink(ctx context.Context, tx *sql.Tx, userID, purpose, createdBy string) (string, error) {
	now := s.now()
	if _, err := tx.ExecContext(ctx, `UPDATE invitations SET used_at = ? WHERE user_id = ? AND used_at IS NULL`,
		db.FormatTime(now), userID); err != nil {
		return "", err
	}
	validity := resetValidity
	if purpose == "invite" {
		validity = inviteValidity
	}
	token, hash := auth.NewToken()
	_, err := tx.ExecContext(ctx,
		`INSERT INTO invitations (token_hash, user_id, purpose, created_by, created_at, expires_at) VALUES (?, ?, ?, ?, ?, ?)`,
		hash, userID, purpose, createdBy, db.FormatTime(now), db.FormatTime(now.Add(validity)))
	return token, err
}

// SendPasswordLink emails a user a new link: the invitation again if they
// never accepted it, otherwise a password reset.
func (s *Service) SendPasswordLink(ctx context.Context, admin User, userID string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	info, err := s.userInfo(ctx, tx, userID)
	if err != nil {
		return err
	}
	if info.Status == "disabled" {
		return ErrUserDisabled
	}
	purpose := "reset"
	if info.Status == "invited" {
		purpose = "invite"
	}
	token, err := s.newLink(ctx, tx, info.ID, purpose, admin.ID)
	if err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	if err := s.mail.Send(ctx, linkMessage(info.User, admin.Name, s.linkURL(token), purpose)); err != nil {
		s.log.Error("sending password link", "to", info.Email, "error", err)
		return ErrMailFailed
	}
	return nil
}

type UserPatch struct {
	Name     *string `json:"name"`
	Role     *string `json:"role"`
	Disabled *bool   `json:"disabled"`
}

// UpdateUser changes a user's name, role or whether they can sign in.
// Admins can't lock themselves out, and one active admin always remains.
func (s *Service) UpdateUser(ctx context.Context, admin User, userID string, p UserPatch) (UserInfo, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return UserInfo{}, err
	}
	defer tx.Rollback()
	info, err := s.userInfo(ctx, tx, userID)
	if err != nil {
		return UserInfo{}, err
	}
	if info.ID == admin.ID && (p.Role != nil && *p.Role != "admin" || p.Disabled != nil && *p.Disabled) {
		return UserInfo{}, ErrOwnAccount
	}
	now := db.FormatTime(s.now())
	if p.Name != nil {
		name, err := NormalizeName(*p.Name)
		if err != nil {
			return UserInfo{}, err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE users SET name = ? WHERE id = ?`, name, info.ID); err != nil {
			return UserInfo{}, err
		}
	}
	if p.Role != nil {
		if *p.Role != "admin" && *p.Role != "member" {
			return UserInfo{}, ErrInvalidRole
		}
		if _, err := tx.ExecContext(ctx, `UPDATE users SET role = ? WHERE id = ?`, *p.Role, info.ID); err != nil {
			return UserInfo{}, err
		}
	}
	if p.Disabled != nil {
		var disabledAt any
		if *p.Disabled {
			disabledAt = now
		}
		if _, err := tx.ExecContext(ctx, `UPDATE users SET disabled_at = ? WHERE id = ?`, disabledAt, info.ID); err != nil {
			return UserInfo{}, err
		}
		if *p.Disabled {
			// Sign them out everywhere and void open links.
			if _, err := tx.ExecContext(ctx, `DELETE FROM sessions WHERE user_id = ?`, info.ID); err != nil {
				return UserInfo{}, err
			}
			if _, err := tx.ExecContext(ctx, `UPDATE invitations SET used_at = ? WHERE user_id = ? AND used_at IS NULL`, now, info.ID); err != nil {
				return UserInfo{}, err
			}
		}
	}
	var admins int
	if err := tx.QueryRowContext(ctx,
		`SELECT count(*) FROM users WHERE role = 'admin' AND disabled_at IS NULL AND password_hash != ''`).Scan(&admins); err != nil {
		return UserInfo{}, err
	}
	if admins == 0 {
		return UserInfo{}, ErrLastAdmin
	}
	if err := tx.Commit(); err != nil {
		return UserInfo{}, err
	}
	return s.userInfo(ctx, s.db, info.ID)
}

// Link is what a password link page shows before the password is chosen.
type Link struct {
	Email   string `json:"email"`
	Name    string `json:"name"`
	Purpose string `json:"purpose"` // invite | reset
}

func (s *Service) openLink(ctx context.Context, q rowQuerier, token string) (User, string, error) {
	if token == "" || len(token) > 100 {
		return User{}, "", ErrInvalidLink
	}
	var purpose string
	u, err := scanUserWith(q.QueryRowContext(ctx,
		`SELECT u.id, u.email, u.name, u.role, u.locale, u.created_at, i.purpose
		 FROM invitations i JOIN users u ON u.id = i.user_id
		 WHERE i.token_hash = ? AND i.used_at IS NULL AND i.expires_at > ? AND u.disabled_at IS NULL`,
		auth.HashToken(token), db.FormatTime(s.now())), &purpose)
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, "", ErrInvalidLink
	}
	return u, purpose, err
}

// CheckLink tells the welcome page whose link it is.
func (s *Service) CheckLink(ctx context.Context, token string) (Link, error) {
	u, purpose, err := s.openLink(ctx, s.db, token)
	if err != nil {
		return Link{}, err
	}
	return Link{Email: u.Email, Name: u.Name, Purpose: purpose}, nil
}

// AcceptLink sets the password chosen on a welcome page. Opening the
// emailed link proves the address, so the caller may sign the user in
// directly. Every other session of the user ends.
func (s *Service) AcceptLink(ctx context.Context, token, password string) (User, error) {
	u, _, err := s.openLink(ctx, s.db, token)
	if err != nil {
		return User{}, err
	}
	if err := auth.CheckPassword(password, u.Email, u.Name); err != nil {
		return User{}, err
	}
	// Hash before the transaction: it takes a moment and would hold the
	// database's write lock.
	hash, err := s.hasher.Hash(ctx, password)
	if err != nil {
		return User{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return User{}, err
	}
	defer tx.Rollback()
	if u, _, err = s.openLink(ctx, tx, token); err != nil {
		return User{}, err
	}
	now := db.FormatTime(s.now())
	for _, q := range []struct {
		sql  string
		args []any
	}{
		{`UPDATE users SET password_hash = ? WHERE id = ?`, []any{hash, u.ID}},
		{`UPDATE invitations SET used_at = ? WHERE user_id = ? AND used_at IS NULL`, []any{now, u.ID}},
		{`DELETE FROM sessions WHERE user_id = ?`, []any{u.ID}},
	} {
		if _, err := tx.ExecContext(ctx, q.sql, q.args...); err != nil {
			return User{}, err
		}
	}
	return u, tx.Commit()
}
