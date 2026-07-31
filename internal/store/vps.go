package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/enowdev/antares/internal/secret"
)

const vpsCols = `id,label,host,port,username,auth_method,password,private_key,passphrase,host_key,folder_id,sort_order,created_at,updated_at`

// ErrInvalidVPSHierarchy identifies invalid folder parents and cyclic moves.
var ErrInvalidVPSHierarchy = errors.New("invalid VPS hierarchy")

// crypter returns the process secret box, obtained once. VPS credentials are
// unusable without it, so a failure here fails the operation rather than
// silently storing plaintext.
func (s *sqlStore) crypter() (*secret.Box, error) {
	s.boxOnce.Do(func() { s.box, s.boxErr = secret.Default() })
	return s.box, s.boxErr
}

// PutVPSHost inserts or updates a host, encrypting its secret fields at rest.
func (s *sqlStore) PutVPSHost(ctx context.Context, v *VPSHost) error {
	box, err := s.crypter()
	if err != nil {
		return err
	}
	now := time.Now()
	isNew := v.CreatedAt.IsZero()
	if isNew {
		v.CreatedAt = now
	}
	v.UpdatedAt = now
	if v.Port == 0 {
		v.Port = 22
	}

	encPass, err := box.Encrypt(v.Password)
	if err != nil {
		return err
	}
	encKey, err := box.Encrypt(v.PrivateKey)
	if err != nil {
		return err
	}
	encPhrase, err := box.Encrypt(v.Passphrase)
	if err != nil {
		return err
	}

	// host_key is deliberately NOT in the conflict-update set: it is pinned by
	// SetVPSHostKey on first connect and must survive an edit of the other
	// fields. On insert it starts empty.
	query := `INSERT INTO vps_hosts (` + vpsCols + `) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?)` +
		onConflict("id", `label=EXCLUDED.label, host=EXCLUDED.host, port=EXCLUDED.port,
			username=EXCLUDED.username, auth_method=EXCLUDED.auth_method, password=EXCLUDED.password,
			private_key=EXCLUDED.private_key, passphrase=EXCLUDED.passphrase, folder_id=EXCLUDED.folder_id,
			sort_order=EXCLUDED.sort_order, updated_at=EXCLUDED.updated_at`)
	if isNew {
		tx, err := s.beginVPSTreeTx(ctx)
		if err != nil {
			return err
		}
		defer tx.Rollback()
		if err := s.requireVPSFolderTx(ctx, tx, v.FolderID); err != nil {
			return err
		}
		if err := tx.QueryRowContext(ctx, s.rebind(`SELECT COALESCE(MAX(sort_order), -1) + 1 FROM vps_hosts WHERE COALESCE(folder_id, '')=?`), v.FolderID).Scan(&v.SortOrder); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, s.rebind(query), v.ID, v.Label, v.Host, v.Port, v.Username, v.AuthMethod,
			encPass, encKey, encPhrase, v.HostKey, nullableVPSParent(v.FolderID), v.SortOrder,
			ms(v.CreatedAt), ms(v.UpdatedAt)); err != nil {
			return err
		}
		return tx.Commit()
	}
	_, err = s.exec(ctx, query, v.ID, v.Label, v.Host, v.Port, v.Username, v.AuthMethod,
		encPass, encKey, encPhrase, v.HostKey, nullableVPSParent(v.FolderID), v.SortOrder,
		ms(v.CreatedAt), ms(v.UpdatedAt))
	return err
}

// SetVPSHostKey pins (or updates) a host's SSH public key — called on first
// connect for TOFU, and again only when the user has confirmed a key change.
func (s *sqlStore) SetVPSHostKey(ctx context.Context, id, hostKey string) error {
	_, err := s.exec(ctx, `UPDATE vps_hosts SET host_key=? WHERE id=?`, hostKey, id)
	return err
}

// GetVPSHost returns a host with its secret fields decrypted.
func (s *sqlStore) GetVPSHost(ctx context.Context, id string) (*VPSHost, error) {
	box, err := s.crypter()
	if err != nil {
		return nil, err
	}
	v, err := scanVPSHost(s.row(ctx, `SELECT `+vpsCols+` FROM vps_hosts WHERE id=?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return decryptVPS(box, v)
}

// ListVPSHosts returns every host with secrets decrypted in hierarchy order.
func (s *sqlStore) ListVPSHosts(ctx context.Context) ([]VPSHost, error) {
	box, err := s.crypter()
	if err != nil {
		return nil, err
	}
	rows, err := s.query(ctx, `SELECT `+vpsCols+` FROM vps_hosts ORDER BY COALESCE(folder_id, ''), sort_order, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []VPSHost{}
	for rows.Next() {
		v, err := scanVPSHost(rows)
		if err != nil {
			return nil, err
		}
		if _, err := decryptVPS(box, v); err != nil {
			return nil, err
		}
		out = append(out, *v)
	}
	return out, rows.Err()
}

func (s *sqlStore) DeleteVPSHost(ctx context.Context, id string) error {
	_, err := s.exec(ctx, `DELETE FROM vps_hosts WHERE id=?`, id)
	return err
}

func scanVPSHost(sc interface{ Scan(...any) error }) (*VPSHost, error) {
	var v VPSHost
	var folderID sql.NullString
	var created, updated int64
	if err := sc.Scan(&v.ID, &v.Label, &v.Host, &v.Port, &v.Username, &v.AuthMethod,
		&v.Password, &v.PrivateKey, &v.Passphrase, &v.HostKey, &folderID, &v.SortOrder, &created, &updated); err != nil {
		return nil, err
	}
	v.FolderID = folderID.String
	v.CreatedAt = fromMS(created)
	v.UpdatedAt = fromMS(updated)
	return &v, nil
}

// decryptVPS turns the stored ciphertext back into usable secrets in place.
func decryptVPS(box *secret.Box, v *VPSHost) (*VPSHost, error) {
	var err error
	if v.Password, err = box.Decrypt(v.Password); err != nil {
		return nil, err
	}
	if v.PrivateKey, err = box.Decrypt(v.PrivateKey); err != nil {
		return nil, err
	}
	if v.Passphrase, err = box.Decrypt(v.Passphrase); err != nil {
		return nil, err
	}
	return v, nil
}

func nullableVPSParent(id string) any {
	if id == "" {
		return nil
	}
	return id
}

// CreateVPSFolder appends a folder beneath parent. Structural writes update a
// singleton row first so concurrent Postgres instances serialize validation.
func (s *sqlStore) CreateVPSFolder(ctx context.Context, f *VPSFolder) error {
	f.Name = strings.TrimSpace(f.Name)
	if f.ID == "" || f.Name == "" {
		return fmt.Errorf("%w: folder id and name are required", ErrInvalidVPSHierarchy)
	}
	tx, err := s.beginVPSTreeTx(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := s.requireVPSFolderTx(ctx, tx, f.ParentID); err != nil {
		return err
	}
	now := time.Now()
	if f.CreatedAt.IsZero() {
		f.CreatedAt = now
	}
	f.UpdatedAt = now
	if err := tx.QueryRowContext(ctx, s.rebind(`SELECT COALESCE(MAX(sort_order), -1) + 1 FROM vps_folders WHERE COALESCE(parent_id, '')=?`), f.ParentID).Scan(&f.SortOrder); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, s.rebind(`INSERT INTO vps_folders (id,name,parent_id,sort_order,created_at,updated_at) VALUES (?,?,?,?,?,?)`),
		f.ID, f.Name, nullableVPSParent(f.ParentID), f.SortOrder, ms(f.CreatedAt), ms(f.UpdatedAt))
	if err != nil {
		return err
	}
	return tx.Commit()
}

func (s *sqlStore) RenameVPSFolder(ctx context.Context, id, name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("%w: folder name is required", ErrInvalidVPSHierarchy)
	}
	res, err := s.exec(ctx, `UPDATE vps_folders SET name=?, updated_at=? WHERE id=?`, name, ms(time.Now()), id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *sqlStore) ListVPSFolders(ctx context.Context) ([]VPSFolder, error) {
	rows, err := s.query(ctx, `SELECT id,name,parent_id,sort_order,created_at,updated_at FROM vps_folders ORDER BY COALESCE(parent_id, ''), sort_order, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []VPSFolder{}
	for rows.Next() {
		var f VPSFolder
		var parentID sql.NullString
		var created, updated int64
		if err := rows.Scan(&f.ID, &f.Name, &parentID, &f.SortOrder, &created, &updated); err != nil {
			return nil, err
		}
		f.ParentID = parentID.String
		f.CreatedAt = fromMS(created)
		f.UpdatedAt = fromMS(updated)
		out = append(out, f)
	}
	return out, rows.Err()
}

func (s *sqlStore) MoveVPSFolder(ctx context.Context, id, parentID string, index int) error {
	tx, err := s.beginVPSTreeTx(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	parents, err := s.vpsFolderParentsTx(ctx, tx)
	if err != nil {
		return err
	}
	sourceParent, ok := parents[id]
	if !ok {
		return ErrNotFound
	}
	if parentID != "" {
		if _, ok := parents[parentID]; !ok {
			return fmt.Errorf("%w: parent folder not found", ErrInvalidVPSHierarchy)
		}
	}
	seen := map[string]bool{}
	for ancestor := parentID; ancestor != ""; ancestor = parents[ancestor] {
		if ancestor == id {
			return fmt.Errorf("%w: folder cannot be moved into itself or a descendant", ErrInvalidVPSHierarchy)
		}
		if seen[ancestor] {
			return fmt.Errorf("%w: existing folder cycle", ErrInvalidVPSHierarchy)
		}
		seen[ancestor] = true
	}

	source, err := s.vpsFolderIDsTx(ctx, tx, sourceParent)
	if err != nil {
		return err
	}
	source = withoutVPSID(source, id)
	if sourceParent == parentID {
		source = insertVPSID(source, id, index)
		if err := s.writeVPSFolderOrderTx(ctx, tx, sourceParent, source); err != nil {
			return err
		}
	} else {
		target, err := s.vpsFolderIDsTx(ctx, tx, parentID)
		if err != nil {
			return err
		}
		target = insertVPSID(target, id, index)
		if err := s.writeVPSFolderOrderTx(ctx, tx, sourceParent, source); err != nil {
			return err
		}
		if err := s.writeVPSFolderOrderTx(ctx, tx, parentID, target); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *sqlStore) MoveVPSHost(ctx context.Context, id, folderID string, index int) error {
	tx, err := s.beginVPSTreeTx(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := s.requireVPSFolderTx(ctx, tx, folderID); err != nil {
		return err
	}
	var source sql.NullString
	if err := tx.QueryRowContext(ctx, s.rebind(`SELECT folder_id FROM vps_hosts WHERE id=?`), id).Scan(&source); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		return err
	}
	sourceID := source.String
	sourceIDs, err := s.vpsHostIDsTx(ctx, tx, sourceID)
	if err != nil {
		return err
	}
	sourceIDs = withoutVPSID(sourceIDs, id)
	if sourceID == folderID {
		sourceIDs = insertVPSID(sourceIDs, id, index)
		if err := s.writeVPSHostOrderTx(ctx, tx, sourceID, sourceIDs); err != nil {
			return err
		}
	} else {
		targetIDs, err := s.vpsHostIDsTx(ctx, tx, folderID)
		if err != nil {
			return err
		}
		targetIDs = insertVPSID(targetIDs, id, index)
		if err := s.writeVPSHostOrderTx(ctx, tx, sourceID, sourceIDs); err != nil {
			return err
		}
		if err := s.writeVPSHostOrderTx(ctx, tx, folderID, targetIDs); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// DeleteVPSFolder promotes direct child folders and hosts to the deleted
// folder's parent. Descendant subtrees remain attached to their direct parent.
func (s *sqlStore) DeleteVPSFolder(ctx context.Context, id string) error {
	tx, err := s.beginVPSTreeTx(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var parent sql.NullString
	if err := tx.QueryRowContext(ctx, s.rebind(`SELECT parent_id FROM vps_folders WHERE id=?`), id).Scan(&parent); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		return err
	}
	parentID := parent.String

	parentFolders, err := s.vpsFolderIDsTx(ctx, tx, parentID)
	if err != nil {
		return err
	}
	insertAt := indexOfVPSID(parentFolders, id)
	parentFolders = withoutVPSID(parentFolders, id)
	children, err := s.vpsFolderIDsTx(ctx, tx, id)
	if err != nil {
		return err
	}
	if insertAt < 0 {
		insertAt = len(parentFolders)
	}
	parentFolders = insertVPSIDs(parentFolders, children, insertAt)
	if err := s.writeVPSFolderOrderTx(ctx, tx, parentID, parentFolders); err != nil {
		return err
	}

	parentHosts, err := s.vpsHostIDsTx(ctx, tx, parentID)
	if err != nil {
		return err
	}
	childHosts, err := s.vpsHostIDsTx(ctx, tx, id)
	if err != nil {
		return err
	}
	parentHosts = append(parentHosts, childHosts...)
	if err := s.writeVPSHostOrderTx(ctx, tx, parentID, parentHosts); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, s.rebind(`DELETE FROM vps_folders WHERE id=?`), id); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *sqlStore) beginVPSTreeTx(ctx context.Context) (*sql.Tx, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, s.rebind(`UPDATE vps_tree_state SET revision=revision+1 WHERE id=1`)); err != nil {
		tx.Rollback()
		return nil, err
	}
	return tx, nil
}

func (s *sqlStore) requireVPSFolderTx(ctx context.Context, tx *sql.Tx, id string) error {
	if id == "" {
		return nil
	}
	var exists int
	if err := tx.QueryRowContext(ctx, s.rebind(`SELECT 1 FROM vps_folders WHERE id=?`), id).Scan(&exists); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("%w: folder not found", ErrInvalidVPSHierarchy)
		}
		return err
	}
	return nil
}

func (s *sqlStore) vpsFolderParentsTx(ctx context.Context, tx *sql.Tx) (map[string]string, error) {
	rows, err := tx.QueryContext(ctx, `SELECT id,parent_id FROM vps_folders`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var id string
		var parent sql.NullString
		if err := rows.Scan(&id, &parent); err != nil {
			return nil, err
		}
		out[id] = parent.String
	}
	return out, rows.Err()
}

func (s *sqlStore) vpsFolderIDsTx(ctx context.Context, tx *sql.Tx, parentID string) ([]string, error) {
	return s.vpsOrderedIDsTx(ctx, tx, `SELECT id FROM vps_folders WHERE COALESCE(parent_id, '')=? ORDER BY sort_order,id`, parentID)
}

func (s *sqlStore) vpsHostIDsTx(ctx context.Context, tx *sql.Tx, folderID string) ([]string, error) {
	return s.vpsOrderedIDsTx(ctx, tx, `SELECT id FROM vps_hosts WHERE COALESCE(folder_id, '')=? ORDER BY sort_order,id`, folderID)
}

func (s *sqlStore) vpsOrderedIDsTx(ctx context.Context, tx *sql.Tx, query, parentID string) ([]string, error) {
	rows, err := tx.QueryContext(ctx, s.rebind(query), parentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func (s *sqlStore) writeVPSFolderOrderTx(ctx context.Context, tx *sql.Tx, parentID string, ids []string) error {
	for i, id := range ids {
		if _, err := tx.ExecContext(ctx, s.rebind(`UPDATE vps_folders SET parent_id=?, sort_order=?, updated_at=? WHERE id=?`),
			nullableVPSParent(parentID), i, ms(time.Now()), id); err != nil {
			return err
		}
	}
	return nil
}

func (s *sqlStore) writeVPSHostOrderTx(ctx context.Context, tx *sql.Tx, folderID string, ids []string) error {
	for i, id := range ids {
		if _, err := tx.ExecContext(ctx, s.rebind(`UPDATE vps_hosts SET folder_id=?, sort_order=?, updated_at=? WHERE id=?`),
			nullableVPSParent(folderID), i, ms(time.Now()), id); err != nil {
			return err
		}
	}
	return nil
}

func withoutVPSID(ids []string, remove string) []string {
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		if id != remove {
			out = append(out, id)
		}
	}
	return out
}

func insertVPSID(ids []string, id string, index int) []string {
	return insertVPSIDs(ids, []string{id}, index)
}

func insertVPSIDs(ids, added []string, index int) []string {
	if index < 0 {
		index = 0
	}
	if index > len(ids) {
		index = len(ids)
	}
	out := make([]string, 0, len(ids)+len(added))
	out = append(out, ids[:index]...)
	out = append(out, added...)
	out = append(out, ids[index:]...)
	return out
}

func indexOfVPSID(ids []string, target string) int {
	for i, id := range ids {
		if id == target {
			return i
		}
	}
	return -1
}
