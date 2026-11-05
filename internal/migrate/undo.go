package migrate

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/enowdev/antares/internal/config"
)

// BackupInfo describes one past migration, for the Settings list.
type BackupInfo struct {
	Name      string    `json:"name"` // dir name under backups/, the undo handle
	Source    string    `json:"source"`
	Profile   string    `json:"profile,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	Applied   int       `json:"applied"`
	Undone    bool      `json:"undone"`
}

// ListBackups returns the migration backups, newest first.
func ListBackups() []BackupInfo {
	entries, err := os.ReadDir(BackupsDir())
	if err != nil {
		return []BackupInfo{}
	}
	out := []BackupInfo{}
	for _, e := range entries {
		if !e.IsDir() || !strings.HasPrefix(e.Name(), "migrate-") {
			continue
		}
		m, err := readManifest(filepath.Join(BackupsDir(), e.Name()))
		if err != nil {
			continue
		}
		out = append(out, BackupInfo{Name: e.Name(), Source: m.Source, Profile: m.Profile, CreatedAt: m.CreatedAt,
			Applied: len(m.Applied), Undone: m.Undone})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out
}

// ResolveBackup turns a backup directory name ("migrate-hermes-20261003T101500Z")
// into its absolute path under the backups dir. Only a bare name is
// accepted: anything with a path separator or "..", or not starting with
// "migrate-", is refused.
func ResolveBackup(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", fmt.Errorf("no backup named")
	}
	if strings.ContainsAny(name, `/\`) || strings.Contains(name, "..") || !strings.HasPrefix(name, "migrate-") {
		return "", fmt.Errorf("not a migration backup name: %q", name)
	}
	dir := filepath.Join(BackupsDir(), name)
	if !IsFile(filepath.Join(dir, "manifest.json")) {
		return "", fmt.Errorf("backup %q not found", name)
	}
	return dir, nil
}

func readManifest(dir string) (*Manifest, error) {
	b, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if err != nil {
		return nil, err
	}
	var m Manifest
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, fmt.Errorf("manifest: %w", err)
	}
	return &m, nil
}

// Undo reverses one Apply: files it changed are restored from the backup,
// files/folders it created are removed, and the memories, cron jobs and RAG
// documents it added are deleted. The backup dir is kept, marked undone; a
// second Undo is an error. The caller reloads the server afterwards.
func Undo(ctx context.Context, backup string, deps Deps) error {
	dir, err := ResolveBackup(backup)
	if err != nil {
		return err
	}
	m, err := readManifest(dir)
	if err != nil {
		return err
	}
	if m.Undone {
		return fmt.Errorf("this migration was already undone")
	}
	home := filepath.Clean(config.Home()) + string(filepath.Separator)
	inHome := func(p string) bool { return strings.HasPrefix(filepath.Clean(p), home) }
	var errs []error

	// Created paths first: a replaced skill was backed up and removed, then
	// re-created, so it appears in both lists; removing then restoring
	// leaves the original.
	for i := len(m.CreatedPaths) - 1; i >= 0; i-- {
		p := m.CreatedPaths[i]
		if !inHome(p) {
			errs = append(errs, fmt.Errorf("refusing to remove %s outside the Antares home", p))
			continue
		}
		if err := os.RemoveAll(p); err != nil {
			errs = append(errs, err)
		}
	}
	for _, r := range m.Restore {
		if !inHome(r.Path) {
			errs = append(errs, fmt.Errorf("refusing to restore %s outside the Antares home", r.Path))
			continue
		}
		src := filepath.Join(dir, r.Backup)
		st, err := os.Stat(src)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if st.IsDir() {
			_ = os.RemoveAll(r.Path)
			err = copyTree(src, r.Path)
		} else {
			err = copyFile(src, r.Path, st.Mode().Perm())
		}
		if err != nil {
			errs = append(errs, err)
		}
	}
	if deps.Store != nil {
		for _, id := range m.Memories {
			if err := deps.Store.DeleteMemory(ctx, id); err != nil {
				errs = append(errs, err)
			}
		}
		for _, id := range m.CronJobs {
			if err := deps.Store.DeleteCronJob(ctx, id); err != nil {
				errs = append(errs, err)
			}
		}
		byColl := map[string][]string{}
		for _, d := range m.RAGDocs {
			byColl[d.Collection] = append(byColl[d.Collection], d.DocID)
		}
		for coll, ids := range byColl {
			if _, err := deps.Store.DeleteDocuments(ctx, coll, ids); err != nil {
				errs = append(errs, err)
			}
		}
	} else if len(m.Memories)+len(m.CronJobs)+len(m.RAGDocs) > 0 {
		errs = append(errs, fmt.Errorf("no database available to remove imported rows"))
	}
	if _, err := config.Reload(); err != nil {
		errs = append(errs, err)
	}
	if len(errs) > 0 {
		return errors.Join(errs...)
	}
	m.Undone = true
	return writeManifest(dir, m)
}
