package registry

import (
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	bolt "go.etcd.io/bbolt"

	"github.com/depado/buoy/internal/types"
)

func newTestRegistry(t *testing.T) *Registry {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "test.db")

	r, err := Open(path, []types.RepoRef{
		{Name: "local", URL: "/backup"},
		{Name: "s3", URL: "s3:https://bucket"},
	}, slog.Default())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { r.Close() })
	return r
}

func testContainer(name, project, service string) *types.Container {
	return &types.Container{
		ID:             "abc123",
		Name:           name,
		ComposeProject: project,
		ComposeService: service,
	}
}

func TestSyncContainer(t *testing.T) {
	r := newTestRegistry(t)
	ctr := testContainer("myapp", "project", "web")

	repos, err := r.SyncContainer(ctr, types.BackupConfig{})
	if err != nil {
		t.Fatalf("SyncContainer: %v", err)
	}
	if len(repos) != 2 {
		t.Fatalf("expected 2 repos, got %d", len(repos))
	}
	if repos[0].Name != "local" {
		t.Errorf("repo 0 name: got %q, want local", repos[0].Name)
	}
	if repos[1].Name != "s3" {
		t.Errorf("repo 1 name: got %q, want s3", repos[1].Name)
	}
}

func TestSyncContainer_ComposeStack(t *testing.T) {
	r := newTestRegistry(t)
	ctr := testContainer("myapp", "myproject", "web")

	repos, err := r.SyncContainer(ctr, types.BackupConfig{})
	if err != nil {
		t.Fatalf("SyncContainer: %v", err)
	}

	for _, ref := range repos {
		if ref.Name == "local" {
			expected := filepath.Clean("/backup/myproject/web")
			if isLocalPath(ref.URL) {
				abs, _ := filepath.Abs(expected)
				if ref.URL != filepath.Clean(abs) {
					t.Errorf("local URL: got %q, want %q", ref.URL, filepath.Clean(abs))
				}
			}
		}
	}
}

func TestSyncContainer_ReposOverride(t *testing.T) {
	r := newTestRegistry(t)
	ctr := testContainer("myapp", "", "")

	repos, err := r.SyncContainer(ctr, types.BackupConfig{
		ReposOverride: []string{"s3"},
	})
	if err != nil {
		t.Fatalf("SyncContainer: %v", err)
	}
	if len(repos) != 1 {
		t.Fatalf("expected 1 repo, got %d", len(repos))
	}
	if repos[0].Name != "s3" {
		t.Errorf("expected s3 repo, got %q", repos[0].Name)
	}
}

func TestSyncContainer_UnknownRepoName(t *testing.T) {
	r := newTestRegistry(t)
	ctr := testContainer("myapp", "", "")

	repos, err := r.SyncContainer(ctr, types.BackupConfig{
		ReposOverride: []string{"unknown", "local"},
	})
	if err != nil {
		t.Fatalf("SyncContainer: %v", err)
	}
	if len(repos) != 1 {
		t.Fatalf("expected 1 repo (unknown skipped), got %d", len(repos))
	}
	if repos[0].Name != "local" {
		t.Errorf("expected local, got %q", repos[0].Name)
	}
}

func TestMarkBackupComplete(t *testing.T) {
	r := newTestRegistry(t)
	ctr := testContainer("myapp", "", "")

	repos, err := r.SyncContainer(ctr, types.BackupConfig{})
	if err != nil {
		t.Fatalf("SyncContainer: %v", err)
	}

	if err := r.MarkBackupComplete(repos[0].URL, true); err != nil {
		t.Fatalf("MarkBackupComplete: %v", err)
	}

	entries, err := r.ListRepos()
	if err != nil {
		t.Fatalf("ListRepos: %v", err)
	}

	for _, e := range entries {
		if e.URL == repos[0].URL {
			if e.LastBackupAt.IsZero() {
				t.Error("LastBackupAt not set")
			}
			if !e.LastBackupOK {
				t.Error("LastBackupOK should be true")
			}
			return
		}
	}
	t.Error("repo entry not found in listing")
}

func TestListRepos(t *testing.T) {
	r := newTestRegistry(t)
	ctr := testContainer("myapp", "", "")

	if _, err := r.SyncContainer(ctr, types.BackupConfig{}); err != nil {
		t.Fatalf("SyncContainer: %v", err)
	}

	entries, err := r.ListRepos()
	if err != nil {
		t.Fatalf("ListRepos: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries))
	}
}

func TestListRepos_OnlyOrphaned(t *testing.T) {
	r := newTestRegistry(t)
	ctr := testContainer("myapp", "", "")

	if _, err := r.SyncContainer(ctr, types.BackupConfig{}); err != nil {
		t.Fatalf("SyncContainer: %v", err)
	}
	if err := r.MarkOrphaned("abc123"); err != nil {
		t.Fatalf("MarkOrphaned: %v", err)
	}

	entries, err := r.ListRepos(OnlyOrphaned())
	if err != nil {
		t.Fatalf("ListRepos: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("expected 2 orphaned entries, got %d", len(entries))
	}
	for _, e := range entries {
		if !e.Orphaned {
			t.Errorf("entry %q should be orphaned", e.URL)
		}
	}
}

func TestListRepos_ExcludeOrphaned(t *testing.T) {
	r := newTestRegistry(t)
	ctr := testContainer("myapp", "", "")

	if _, err := r.SyncContainer(ctr, types.BackupConfig{}); err != nil {
		t.Fatalf("SyncContainer: %v", err)
	}

	entries, err := r.ListRepos(ExcludeOrphaned())
	if err != nil {
		t.Fatalf("ListRepos: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries))
	}
	for _, e := range entries {
		if e.Orphaned {
			t.Errorf("entry %q should not be orphaned", e.URL)
		}
	}
}

func TestListRepos_FilterByRepo(t *testing.T) {
	r := newTestRegistry(t)
	ctr := testContainer("myapp", "project", "web")

	repos, err := r.SyncContainer(ctr, types.BackupConfig{})
	if err != nil {
		t.Fatalf("SyncContainer: %v", err)
	}

	entries, err := r.ListRepos(FilterByRepo(repos[0].URL))
	if err != nil {
		t.Fatalf("ListRepos: %v", err)
	}
	if len(entries) > 1 {
		t.Errorf("expected at most 1 entry, got %d", len(entries))
	}
	if len(entries) > 0 && entries[0].URL != repos[0].URL {
		t.Errorf("expected URL %q, got %q", repos[0].URL, entries[0].URL)
	}
}

func TestGetContainerRepos(t *testing.T) {
	r := newTestRegistry(t)
	ctr := testContainer("myapp", "", "")

	if _, err := r.SyncContainer(ctr, types.BackupConfig{}); err != nil {
		t.Fatalf("SyncContainer: %v", err)
	}

	entries, err := r.GetContainerRepos("abc123")
	if err != nil {
		t.Fatalf("GetContainerRepos: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries))
	}
}

func TestGetContainerRepos_Unknown(t *testing.T) {
	r := newTestRegistry(t)
	entries, err := r.GetContainerRepos("nonexistent")
	if err != nil {
		t.Fatalf("GetContainerRepos: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("expected 0 entries, got %d", len(entries))
	}
}

func TestMarkOrphaned(t *testing.T) {
	r := newTestRegistry(t)
	ctr := testContainer("myapp", "", "")

	if _, err := r.SyncContainer(ctr, types.BackupConfig{}); err != nil {
		t.Fatalf("SyncContainer: %v", err)
	}
	if err := r.MarkOrphaned("abc123"); err != nil {
		t.Fatalf("MarkOrphaned: %v", err)
	}

	entries, err := r.ListRepos()
	if err != nil {
		t.Fatalf("ListRepos: %v", err)
	}
	for _, e := range entries {
		if !e.Orphaned {
			t.Errorf("entry %q should be orphaned", e.URL)
		}
	}
}

func TestMarkOrphaned_UnknownContainer(t *testing.T) {
	r := newTestRegistry(t)
	if err := r.MarkOrphaned("nonexistent"); err != nil {
		t.Fatalf("MarkOrphaned should not error on unknown container: %v", err)
	}
}

func TestListRepos_Empty(t *testing.T) {
	r := newTestRegistry(t)
	entries, err := r.ListRepos()
	if err != nil {
		t.Fatalf("ListRepos: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("expected 0 entries, got %d", len(entries))
	}
}

func TestResolveRepos(t *testing.T) {
	r := newTestRegistry(t)
	ctr := testContainer("standalone", "", "")

	repos, err := r.ResolveRepos(ctr, types.BackupConfig{})
	if err != nil {
		t.Fatalf("ResolveRepos: %v", err)
	}

	if len(repos) != 2 {
		t.Fatalf("expected 2 repos, got %d", len(repos))
	}
	if repos[0].Name != "local" {
		t.Errorf("first repo: got %q, want local", repos[0].Name)
	}
	if repos[1].Name != "s3" {
		t.Errorf("second repo: got %q, want s3", repos[1].Name)
	}
}

func TestOpen_NonExistentDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "subdir", "nested")
	path := filepath.Join(dir, "test.db")

	r, err := Open(path, []types.RepoRef{{Name: "local", URL: "/backup"}}, slog.Default())
	if err != nil {
		t.Fatalf("Open should create parent dirs: %v", err)
	}
	r.Close()

	if _, err := os.Stat(dir); os.IsNotExist(err) {
		t.Error("parent directory was not created")
	}
}

func TestMarkStopped_AndPendingRestarts(t *testing.T) {
	r := newTestRegistry(t)
	ctr := testContainer("myapp", "project", "web")

	if err := r.MarkStopped(ctr); err != nil {
		t.Fatalf("MarkStopped: %v", err)
	}

	pending, err := r.PendingRestarts()
	if err != nil {
		t.Fatalf("PendingRestarts: %v", err)
	}
	if len(pending) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(pending))
	}
	p := pending[0]
	if p.ContainerID != ctr.ID || p.ContainerName != ctr.Name || p.Project != "project" || p.Service != "web" {
		t.Errorf("unexpected entry: %+v", p)
	}
	if p.StoppedAt.IsZero() {
		t.Error("StoppedAt not set")
	}
}

func TestClearStopped(t *testing.T) {
	r := newTestRegistry(t)
	ctr := testContainer("myapp", "", "")

	if err := r.MarkStopped(ctr); err != nil {
		t.Fatalf("MarkStopped: %v", err)
	}
	if err := r.ClearStopped(ctr.ID); err != nil {
		t.Fatalf("ClearStopped: %v", err)
	}

	pending, err := r.PendingRestarts()
	if err != nil {
		t.Fatalf("PendingRestarts: %v", err)
	}
	if len(pending) != 0 {
		t.Fatalf("expected 0 entries, got %d", len(pending))
	}
}

func TestPendingRestarts_SurvivesReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.db")
	r, err := Open(path, nil, slog.Default())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	ctr := testContainer("myapp", "project", "web")
	if err := r.MarkStopped(ctr); err != nil {
		t.Fatalf("MarkStopped: %v", err)
	}
	if err := r.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	r2, err := Open(path, nil, slog.Default())
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer r2.Close()

	pending, err := r2.PendingRestarts()
	if err != nil {
		t.Fatalf("PendingRestarts: %v", err)
	}
	if len(pending) != 1 {
		t.Fatalf("expected 1 entry after reopen, got %d", len(pending))
	}
	if pending[0].ContainerName != "myapp" {
		t.Errorf("entry name: got %q, want myapp", pending[0].ContainerName)
	}
}

func TestPendingRestarts_OldestFirst(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.db")
	r, err := Open(path, nil, slog.Default())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer r.Close()

	// Write directly so StoppedAt values are controlled, not wall-clock order.
	for _, e := range []PendingRestart{
		{ContainerID: "c", ContainerName: "third", StoppedAt: time.Unix(300, 0)},
		{ContainerID: "a", ContainerName: "first", StoppedAt: time.Unix(100, 0)},
		{ContainerID: "b", ContainerName: "second", StoppedAt: time.Unix(200, 0)},
	} {
		data, err := json.Marshal(e)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		if err := r.db.Update(func(tx *bolt.Tx) error {
			return tx.Bucket(pendingRestartsBucket).Put([]byte(e.ContainerID), data)
		}); err != nil {
			t.Fatalf("put: %v", err)
		}
	}

	pending, err := r.PendingRestarts()
	if err != nil {
		t.Fatalf("PendingRestarts: %v", err)
	}
	got := make([]string, len(pending))
	for i, p := range pending {
		got[i] = p.ContainerName
	}
	want := []string{"first", "second", "third"}
	if !slices.Equal(got, want) {
		t.Errorf("order: got %v, want %v", got, want)
	}
}
