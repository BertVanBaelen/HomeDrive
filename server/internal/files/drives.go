package files

import (
	"context"
	"database/sql"
	"errors"
	"unicode/utf8"

	"github.com/BertVanBaelen/HomeDrive/server/internal/db"
)

// Shared drives have members with roles (docs/architecture.md §4, "Shared
// drives and roles"). Managers rename the drive and manage members; a
// drive always keeps at least one manager. Admins manage the members of
// every shared drive without seeing its files.

var (
	ErrPersonalDrive = errors.New("a personal drive has no members and can't be renamed or deleted")
	ErrLastManager   = errors.New("a shared drive needs a manager")
	ErrDriveNotEmpty = errors.New("only an empty drive can be deleted")
	ErrInvalidRole   = errors.New("role must be viewer, editor or manager")
)

// maxDriveName is shorter than a file name: drive names fit the sidebar.
const maxDriveName = 60

func validateDriveName(name string) (string, error) {
	name, err := ValidateName(name)
	if err != nil || utf8.RuneCountInString(name) > maxDriveName {
		return "", ErrInvalidName
	}
	return name, nil
}

func validRole(role string) bool { return role == "viewer" || role == "editor" || role == "manager" }

// Member is a person in a shared drive.
type Member struct {
	UserID   string `json:"userId"`
	Name     string `json:"name"`
	Email    string `json:"email"`
	Role     string `json:"role"`
	Disabled bool   `json:"disabled,omitempty"`
}

// CreateDrive makes a shared drive with the user as its manager.
func (s *Service) CreateDrive(ctx context.Context, userID, name string) (Drive, error) {
	name, err := validateDriveName(name)
	if err != nil {
		return Drive{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Drive{}, err
	}
	defer tx.Rollback()
	id, err := createDrive(ctx, tx, "shared", name, "", s.now())
	if err != nil {
		return Drive{}, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO drive_members (drive_id, user_id, role) VALUES (?, ?, 'manager')`, id, userID); err != nil {
		return Drive{}, err
	}
	d, err := s.drive(ctx, tx, userID, id)
	if err != nil {
		return Drive{}, err
	}
	return d, tx.Commit()
}

// sharedDrive returns a shared drive with the user's role in it. Personal
// drives give ErrPersonalDrive to their owner and ErrNotFound to others.
func (s *Service) sharedDrive(ctx context.Context, q querier, userID, driveID string) (Drive, error) {
	if !db.ValidID(driveID) {
		return Drive{}, ErrNotFound
	}
	d, err := s.drive(ctx, q, userID, driveID)
	if err != nil {
		return Drive{}, err
	}
	if d.Kind != "shared" {
		return Drive{}, ErrPersonalDrive
	}
	return d, nil
}

// managedDrive is sharedDrive for managers only.
func (s *Service) managedDrive(ctx context.Context, q querier, userID, driveID string) (Drive, error) {
	d, err := s.sharedDrive(ctx, q, userID, driveID)
	if err == nil && d.Role != "manager" {
		err = ErrReadOnly
	}
	return d, err
}

// RenameDrive changes a shared drive's name. Managers only.
func (s *Service) RenameDrive(ctx context.Context, userID, driveID, name string) (Drive, error) {
	name, err := validateDriveName(name)
	if err != nil {
		return Drive{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Drive{}, err
	}
	defer tx.Rollback()
	d, err := s.managedDrive(ctx, tx, userID, driveID)
	if err != nil {
		return Drive{}, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE drives SET name = ? WHERE id = ?`, name, d.ID); err != nil {
		return Drive{}, err
	}
	if d, err = s.drive(ctx, tx, userID, d.ID); err != nil {
		return Drive{}, err
	}
	return d, tx.Commit()
}

// DeleteDrive removes a shared drive that has nothing left in it, not even
// in its trash. Managers only.
func (s *Service) DeleteDrive(ctx context.Context, userID, driveID string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	d, err := s.managedDrive(ctx, tx, userID, driveID)
	if err != nil {
		return err
	}
	var used bool
	if err := tx.QueryRowContext(ctx,
		`SELECT EXISTS (SELECT 1 FROM nodes WHERE drive_id = ? AND parent_id IS NOT NULL)`, d.ID).Scan(&used); err != nil {
		return err
	}
	if used {
		return ErrDriveNotEmpty
	}
	// Unfinished uploads into the drive go too; cleanup removes their
	// bytes once nothing refers to them.
	for _, q := range []struct{ sql, arg string }{
		{`DELETE FROM uploads WHERE parent_id = ?`, d.RootID},
		{`DELETE FROM drives WHERE id = ?`, d.ID},
		{`DELETE FROM nodes WHERE id = ?`, d.RootID},
	} {
		if _, err := tx.ExecContext(ctx, q.sql, q.arg); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Service) members(ctx context.Context, q querier, driveID string) ([]Member, error) {
	rows, err := q.QueryContext(ctx, `
		SELECT u.id, u.name, u.email, m.role, u.disabled_at IS NOT NULL
		FROM drive_members m JOIN users u ON u.id = m.user_id
		WHERE m.drive_id = ?
		ORDER BY CASE m.role WHEN 'manager' THEN 0 WHEN 'editor' THEN 1 ELSE 2 END, lower(u.name), u.email`, driveID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Member{}
	for rows.Next() {
		var m Member
		if err := rows.Scan(&m.UserID, &m.Name, &m.Email, &m.Role, &m.Disabled); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// Members lists a shared drive's members for anyone in it.
func (s *Service) Members(ctx context.Context, userID, driveID string) ([]Member, error) {
	d, err := s.sharedDrive(ctx, s.db, userID, driveID)
	if err != nil {
		return nil, err
	}
	return s.members(ctx, s.db, d.ID)
}

// MemberChange says what SetMember or RemoveMember did, for the audit log.
type MemberChange string

const (
	MemberAdded   MemberChange = "add"
	MemberRole    MemberChange = "role"
	MemberRemoved MemberChange = "remove"
	MemberLeft    MemberChange = "leave"
	MemberSame    MemberChange = "" // nothing changed
)

// SetMember adds someone to a shared drive or changes their role. Managers
// may do it; so may admins (asAdmin), on any shared drive. New members must
// have an account that isn't disabled.
func (s *Service) SetMember(ctx context.Context, actorID, driveID, memberID, role string, asAdmin bool) (MemberChange, error) {
	if !validRole(role) {
		return MemberSame, ErrInvalidRole
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return MemberSame, err
	}
	defer tx.Rollback()
	id, err := s.memberDrive(ctx, tx, actorID, driveID, asAdmin)
	if err != nil {
		return MemberSame, err
	}
	var current sql.NullString
	var disabled bool
	err = tx.QueryRowContext(ctx, `SELECT m.role, u.disabled_at IS NOT NULL FROM users u
		LEFT JOIN drive_members m ON m.user_id = u.id AND m.drive_id = ?
		WHERE u.id = ?`, id, memberID).Scan(&current, &disabled)
	if errors.Is(err, sql.ErrNoRows) || err == nil && !current.Valid && disabled {
		return MemberSame, ErrNotFound
	}
	if err != nil {
		return MemberSame, err
	}
	change := MemberAdded
	switch {
	case current.String == role:
		return MemberSame, nil
	case current.Valid:
		change = MemberRole
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO drive_members (drive_id, user_id, role) VALUES (?, ?, ?)
		ON CONFLICT (drive_id, user_id) DO UPDATE SET role = excluded.role`, id, memberID, role); err != nil {
		return MemberSame, err
	}
	if err := keepsManager(ctx, tx, id); err != nil {
		return MemberSame, err
	}
	return change, tx.Commit()
}

// RemoveMember takes someone out of a shared drive. Managers may remove
// anyone, every member may leave, and admins (asAdmin) may remove anyone
// from any shared drive. The last manager stays.
func (s *Service) RemoveMember(ctx context.Context, actorID, driveID, memberID string, asAdmin bool) (MemberChange, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return MemberSame, err
	}
	defer tx.Rollback()
	var id string
	change := MemberRemoved
	if !asAdmin && actorID == memberID {
		d, err := s.sharedDrive(ctx, tx, actorID, driveID)
		if err != nil {
			return MemberSame, err
		}
		id, change = d.ID, MemberLeft
	} else if id, err = s.memberDrive(ctx, tx, actorID, driveID, asAdmin); err != nil {
		return MemberSame, err
	}
	res, err := tx.ExecContext(ctx, `DELETE FROM drive_members WHERE drive_id = ? AND user_id = ?`, id, memberID)
	if err != nil {
		return MemberSame, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return MemberSame, ErrNotFound
	}
	if err := keepsManager(ctx, tx, id); err != nil {
		return MemberSame, err
	}
	return change, tx.Commit()
}

// memberDrive checks that the actor may manage the members of a shared
// drive and returns its ID.
func (s *Service) memberDrive(ctx context.Context, q querier, actorID, driveID string, asAdmin bool) (string, error) {
	if !asAdmin {
		d, err := s.managedDrive(ctx, q, actorID, driveID)
		return d.ID, err
	}
	if !db.ValidID(driveID) {
		return "", ErrNotFound
	}
	var kind string
	err := q.QueryRowContext(ctx, `SELECT kind FROM drives WHERE id = ?`, driveID).Scan(&kind)
	if errors.Is(err, sql.ErrNoRows) || err == nil && kind != "shared" {
		return "", ErrNotFound
	}
	return driveID, err
}

// keepsManager fails when a change left a shared drive without a manager.
// A disabled manager still counts: enabling them again restores the drive.
func keepsManager(ctx context.Context, q querier, driveID string) error {
	var ok bool
	if err := q.QueryRowContext(ctx,
		`SELECT EXISTS (SELECT 1 FROM drive_members WHERE drive_id = ? AND role = 'manager')`, driveID).Scan(&ok); err != nil {
		return err
	}
	if !ok {
		return ErrLastManager
	}
	return nil
}

// AdminDrive is a shared drive as the admin's list shows it. It holds no
// file names: admins only see the contents of drives they are in.
type AdminDrive struct {
	ID            string   `json:"id"`
	Name          string   `json:"name"`
	Size          int64    `json:"size"`
	JoinByDefault bool     `json:"joinByDefault"`
	Members       []Member `json:"members"`
}

// AdminDrives lists every shared drive for admins.
func (s *Service) AdminDrives(ctx context.Context) ([]AdminDrive, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT d.id, d.name, d.join_by_default,
			(SELECT coalesce(sum(n.size), 0) FROM nodes n WHERE n.drive_id = d.id AND n.kind = 'file')
		FROM drives d WHERE d.kind = 'shared' ORDER BY lower(d.name), d.created_at`)
	if err != nil {
		return nil, err
	}
	out := []AdminDrive{}
	for rows.Next() {
		var d AdminDrive
		if err := rows.Scan(&d.ID, &d.Name, &d.JoinByDefault, &d.Size); err != nil {
			rows.Close()
			return nil, err
		}
		out = append(out, d)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range out {
		if out[i].Members, err = s.members(ctx, s.db, out[i].ID); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// SetJoinByDefault sets whether the invite form ticks a shared drive.
func (s *Service) SetJoinByDefault(ctx context.Context, driveID string, on bool) error {
	if !db.ValidID(driveID) {
		return ErrNotFound
	}
	res, err := s.db.ExecContext(ctx, `UPDATE drives SET join_by_default = ? WHERE id = ? AND kind = 'shared'`, on, driveID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}
