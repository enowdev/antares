package store

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

func TestVPSFolderHierarchy(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	ctx := context.Background()
	s := newTestStore(t)

	createFolder := func(id, name, parent string) {
		t.Helper()
		if err := s.CreateVPSFolder(ctx, &VPSFolder{ID: id, Name: name, ParentID: parent}); err != nil {
			t.Fatalf("create folder %s: %v", id, err)
		}
	}
	createHost := func(id, folder string) {
		t.Helper()
		if err := s.PutVPSHost(ctx, &VPSHost{ID: id, Label: id, Host: id + ".example", FolderID: folder}); err != nil {
			t.Fatalf("create host %s: %v", id, err)
		}
	}

	createFolder("a", "A", "")
	createFolder("z", "Z", "")
	createFolder("c", "C", "a")
	createFolder("d", "D", "c")
	if err := s.CreateVPSFolder(ctx, &VPSFolder{ID: "bad", Name: "Bad", ParentID: "missing"}); !errors.Is(err, ErrInvalidVPSHierarchy) {
		t.Fatalf("create with missing parent: want hierarchy error, got %v", err)
	}

	if err := s.MoveVPSFolder(ctx, "z", "a", 0); err != nil {
		t.Fatalf("move folder: %v", err)
	}
	if err := s.MoveVPSFolder(ctx, "a", "d", 0); !errors.Is(err, ErrInvalidVPSHierarchy) {
		t.Fatalf("cyclic move: want hierarchy error, got %v", err)
	}
	if err := s.RenameVPSFolder(ctx, "c", "Core"); err != nil {
		t.Fatalf("rename: %v", err)
	}

	createHost("root", "")
	createHost("a1", "a")
	createHost("a2", "a")
	createHost("c1", "c")
	if err := s.MoveVPSHost(ctx, "root", "a", 1); err != nil {
		t.Fatalf("move host: %v", err)
	}
	if err := s.MoveVPSHost(ctx, "a1", "missing", 0); !errors.Is(err, ErrInvalidVPSHierarchy) {
		t.Fatalf("move host to missing folder: want hierarchy error, got %v", err)
	}

	if err := s.DeleteVPSFolder(ctx, "a"); err != nil {
		t.Fatalf("delete folder: %v", err)
	}

	folders, err := s.ListVPSFolders(ctx)
	if err != nil {
		t.Fatalf("list folders: %v", err)
	}
	if got := folderState(folders); got != "z:@0,c:@1,d:c@0" {
		t.Fatalf("folder promotion/order mismatch: %s", got)
	}
	if folders[1].Name != "Core" {
		t.Fatalf("rename not persisted: %+v", folders[1])
	}

	hosts, err := s.ListVPSHosts(ctx)
	if err != nil {
		t.Fatalf("list hosts: %v", err)
	}
	if got := hostState(hosts); got != "a1:@0,root:@1,a2:@2,c1:c@0" {
		t.Fatalf("host promotion/order mismatch: %s", got)
	}
}

func TestVPSFolderMigrationFromFlatSchema(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	ctx := context.Background()
	dsn := filepath.Join(t.TempDir(), "legacy.db")
	db, err := sql.Open("sqlite", "file:"+dsn)
	if err != nil {
		t.Fatalf("open legacy sqlite: %v", err)
	}
	_, err = db.Exec(`CREATE TABLE vps_hosts (
		id TEXT PRIMARY KEY, label TEXT NOT NULL DEFAULT '', host TEXT NOT NULL,
		port INTEGER NOT NULL DEFAULT 22, username TEXT NOT NULL DEFAULT 'root',
		auth_method TEXT NOT NULL DEFAULT 'password', password TEXT NOT NULL DEFAULT '',
		private_key TEXT NOT NULL DEFAULT '', passphrase TEXT NOT NULL DEFAULT '',
		host_key TEXT NOT NULL DEFAULT '', created_at BIGINT NOT NULL, updated_at BIGINT NOT NULL
	)`)
	if err != nil {
		t.Fatalf("create legacy schema: %v", err)
	}
	now := time.Now().UnixMilli()
	if _, err := db.Exec(`INSERT INTO vps_hosts (id,label,host,created_at,updated_at) VALUES ('old','Old','old.example',?,?)`, now, now); err != nil {
		t.Fatalf("insert legacy host: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close legacy sqlite: %v", err)
	}

	s, err := Open(ctx, "sqlite", dsn, 1, 5000, false)
	if err != nil {
		t.Fatalf("migrate legacy store: %v", err)
	}
	hosts, err := s.ListVPSHosts(ctx)
	if err != nil || len(hosts) != 1 {
		t.Fatalf("list migrated hosts: err=%v hosts=%+v", err, hosts)
	}
	if hosts[0].FolderID != "" || hosts[0].SortOrder != 0 {
		t.Fatalf("legacy host should be ungrouped: %+v", hosts[0])
	}
	if err := s.Close(); err != nil {
		t.Fatalf("close migrated store: %v", err)
	}

	s, err = Open(ctx, "sqlite", dsn, 1, 5000, false)
	if err != nil {
		t.Fatalf("repeat migrations: %v", err)
	}
	defer s.Close()
	if folders, err := s.ListVPSFolders(ctx); err != nil || len(folders) != 0 {
		t.Fatalf("list folders after repeat migration: err=%v folders=%+v", err, folders)
	}
}

func folderState(folders []VPSFolder) string {
	var out string
	for i, f := range folders {
		if i > 0 {
			out += ","
		}
		out += f.ID + ":" + f.ParentID + "@" + strconv.FormatInt(f.SortOrder, 10)
	}
	return out
}

func hostState(hosts []VPSHost) string {
	var out string
	for i, h := range hosts {
		if i > 0 {
			out += ","
		}
		out += h.ID + ":" + h.FolderID + "@" + strconv.FormatInt(h.SortOrder, 10)
	}
	return out
}
