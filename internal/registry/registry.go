package registry

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	bolt "go.etcd.io/bbolt"

	"github.com/depado/buoy/internal/config"
	"github.com/depado/buoy/internal/docker"
)

var (
	reposBucket          = []byte("repos")
	containerReposBucket = []byte("container_repos")
)

type RepoEntry struct {
	URL            string    `json:"url"`
	RepoName       string    `json:"repo_name,omitempty"`
	ContainerID    string    `json:"container_id"`
	ContainerName  string    `json:"container_name"`
	ComposeProject string    `json:"compose_project,omitempty"`
	ComposeService string    `json:"compose_service,omitempty"`
	CreatedAt      time.Time `json:"created_at"`
	LastBackupAt   time.Time `json:"last_backup_at"`
	LastBackupOK   bool      `json:"last_backup_ok"`
	LastCheckAt    time.Time `json:"last_check_at"`
	LastCheckOK    bool      `json:"last_check_ok"`
	Orphaned       bool      `json:"orphaned"`
}

type RepoRef struct {
	Name string
	URL  string
}

type Registry struct {
	db    *bolt.DB
	repos []config.NamedRepo
}

func Open(path string, baseRepos []config.NamedRepo) (*Registry, error) {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, fmt.Errorf("create registry directory %s: %w", dir, err)
	}
	db, err := bolt.Open(path, 0600, &bolt.Options{Timeout: 2 * time.Second})
	if err != nil {
		return nil, fmt.Errorf("open registry: %w", err)
	}
	if err := db.Update(func(tx *bolt.Tx) error {
		for _, bucket := range [][]byte{reposBucket, containerReposBucket} {
			if _, err := tx.CreateBucketIfNotExists(bucket); err != nil {
				return fmt.Errorf("create bucket %s: %w", bucket, err)
			}
		}
		return nil
	}); err != nil {
		db.Close()
		return nil, err
	}
	return &Registry{db: db, repos: baseRepos}, nil
}

func (r *Registry) Close() error {
	return r.db.Close()
}

func (r *Registry) SyncContainer(ctr *docker.Container, cfg docker.BackupConfig) ([]RepoRef, error) {
	repos, err := r.resolveRepos(ctr, cfg)
	if err != nil {
		return nil, err
	}
	now := time.Now()

	writeErr := r.db.Update(func(tx *bolt.Tx) error {
		rb := tx.Bucket(reposBucket)
		cb := tx.Bucket(containerReposBucket)

		urls := make([]string, len(repos))
		for i, ref := range repos {
			urls[i] = ref.URL
			if err := r.upsertRepoEntry(rb, ref, ctr, now); err != nil {
				return err
			}
		}

		idsJSON, err := json.Marshal(urls)
		if err != nil {
			return fmt.Errorf("marshal container repos: %w", err)
		}
		return cb.Put([]byte(ctr.ID), idsJSON)
	})

	return repos, writeErr
}

func (r *Registry) upsertRepoEntry(b *bolt.Bucket, ref RepoRef, ctr *docker.Container, now time.Time) error {
	key := []byte(ref.URL)
	existing := b.Get(key)

	var entry RepoEntry
	if existing != nil {
		if err := json.Unmarshal(existing, &entry); err != nil {
			return fmt.Errorf("unmarshal repo entry %s: %w", ref.URL, err)
		}
		if entry.ContainerID == ctr.ID &&
			entry.ContainerName == ctr.Name &&
			entry.ComposeProject == ctr.ComposeProject &&
			entry.ComposeService == ctr.ComposeService &&
			!entry.Orphaned {
			return nil
		}
		entry.ContainerID = ctr.ID
		entry.ContainerName = ctr.Name
		entry.ComposeProject = ctr.ComposeProject
		entry.ComposeService = ctr.ComposeService
		entry.RepoName = ref.Name
		entry.Orphaned = false
	} else {
		entry = RepoEntry{
			URL:            ref.URL,
			RepoName:       ref.Name,
			ContainerID:    ctr.ID,
			ContainerName:  ctr.Name,
			ComposeProject: ctr.ComposeProject,
			ComposeService: ctr.ComposeService,
			CreatedAt:      now,
		}
	}

	data, err := json.Marshal(entry)
	if err != nil {
		return fmt.Errorf("marshal repo entry %s: %w", ref.URL, err)
	}
	return b.Put(key, data)
}

func (r *Registry) MarkBackupComplete(repo string, ok bool) error {
	return r.updateRepoMeta(repo, func(entry *RepoEntry) {
		entry.LastBackupAt = time.Now()
		entry.LastBackupOK = ok
	})
}

func (r *Registry) MarkCheckComplete(repo string, ok bool) error {
	return r.updateRepoMeta(repo, func(entry *RepoEntry) {
		entry.LastCheckAt = time.Now()
		entry.LastCheckOK = ok
	})
}

func (r *Registry) updateRepoMeta(repo string, fn func(*RepoEntry)) error {
	return r.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket(reposBucket)
		data := b.Get([]byte(repo))
		if data == nil {
			return nil
		}
		var entry RepoEntry
		if err := json.Unmarshal(data, &entry); err != nil {
			return fmt.Errorf("unmarshal repo entry %s: %w", repo, err)
		}
		fn(&entry)
		data, err := json.Marshal(entry)
		if err != nil {
			return fmt.Errorf("marshal repo entry %s: %w", repo, err)
		}
		return b.Put([]byte(repo), data)
	})
}

func (r *Registry) MarkOrphaned(containerID string) error {
	return r.db.Update(func(tx *bolt.Tx) error {
		rb := tx.Bucket(reposBucket)
		cb := tx.Bucket(containerReposBucket)

		data := cb.Get([]byte(containerID))
		if data == nil {
			return nil
		}

		var repos []string
		if err := json.Unmarshal(data, &repos); err != nil {
			return fmt.Errorf("unmarshal container repos: %w", err)
		}

		for _, repo := range repos {
			rd := rb.Get([]byte(repo))
			if rd == nil {
				continue
			}
			var entry RepoEntry
			if err := json.Unmarshal(rd, &entry); err != nil {
				return fmt.Errorf("unmarshal repo entry %s: %w", repo, err)
			}
			entry.Orphaned = true
			nd, err := json.Marshal(entry)
			if err != nil {
				return fmt.Errorf("marshal repo entry %s: %w", repo, err)
			}
			if err := rb.Put([]byte(repo), nd); err != nil {
				return fmt.Errorf("put repo entry %s: %w", repo, err)
			}
		}

		return cb.Delete([]byte(containerID))
	})
}

type ListOption func(*listConfig)

type listConfig struct {
	orphanedOnly    bool
	nonOrphanedOnly bool
	repoURL         string
}

func OnlyOrphaned() ListOption {
	return func(c *listConfig) { c.orphanedOnly = true }
}

func ExcludeOrphaned() ListOption {
	return func(c *listConfig) { c.nonOrphanedOnly = true }
}

func FilterByRepo(url string) ListOption {
	return func(c *listConfig) { c.repoURL = url }
}

func (r *Registry) ListRepos(opts ...ListOption) ([]RepoEntry, error) {
	var cfg listConfig
	for _, o := range opts {
		o(&cfg)
	}

	var entries []RepoEntry
	err := r.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket(reposBucket)
		c := b.Cursor()
		for k, v := c.First(); k != nil; k, v = c.Next() {
			var entry RepoEntry
			if err := json.Unmarshal(v, &entry); err != nil {
				return fmt.Errorf("unmarshal repo entry %s: %w", string(k), err)
			}
			if cfg.orphanedOnly && !entry.Orphaned {
				continue
			}
			if cfg.nonOrphanedOnly && entry.Orphaned {
				continue
			}
			if cfg.repoURL != "" && entry.URL != cfg.repoURL {
				continue
			}
			entries = append(entries, entry)
		}
		return nil
	})
	return entries, err
}

func (r *Registry) resolveRepos(ctr *docker.Container, cfg docker.BackupConfig) ([]RepoRef, error) {
	var refs []RepoRef

	if len(cfg.ReposOverride) > 0 {
		repoMap := make(map[string]config.NamedRepo, len(r.repos))
		for _, nr := range r.repos {
			repoMap[nr.Name] = nr
		}
		for _, name := range cfg.ReposOverride {
			nr, ok := repoMap[name]
			if !ok {
				slog.Warn("unknown repo name in buoy.repos label, skipping", "repo_name", name, "container", ctr.Name)
				continue
			}
			refs = append(refs, RepoRef{Name: nr.Name, URL: nr.URL})
		}
	} else {
		for _, nr := range r.repos {
			refs = append(refs, RepoRef{Name: nr.Name, URL: nr.URL})
		}
	}

	repos := make([]RepoRef, 0, len(refs))
	for _, ref := range refs {
		base := strings.TrimRight(ref.URL, "/")
		path := ctr.RepoPath(base)
		if isLocalPath(path) {
			abs, err := filepath.Abs(path)
			if err != nil {
				return nil, fmt.Errorf("repo path for base %q: %w", base, err)
			}
			path = filepath.Clean(abs)
		}
		repos = append(repos, RepoRef{Name: ref.Name, URL: path})
	}
	return repos, nil
}

func isLocalPath(p string) bool {
	if filepath.IsAbs(p) {
		return true
	}
	if strings.HasPrefix(p, "./") || strings.HasPrefix(p, "../") {
		return true
	}
	idx := strings.Index(p, ":")
	if idx == -1 {
		return true
	}
	if idx == 1 && len(p) > 2 && p[2] == '\\' {
		return true
	}
	return false
}

func (r *Registry) ResolveRepos(ctr *docker.Container, cfg docker.BackupConfig) ([]RepoRef, error) {
	return r.resolveRepos(ctr, cfg)
}

func (r *Registry) GetContainerRepos(containerID string) ([]RepoEntry, error) {
	var entries []RepoEntry
	err := r.db.View(func(tx *bolt.Tx) error {
		cb := tx.Bucket(containerReposBucket)
		rb := tx.Bucket(reposBucket)

		data := cb.Get([]byte(containerID))
		if data == nil {
			return nil
		}

		var repos []string
		if err := json.Unmarshal(data, &repos); err != nil {
			return fmt.Errorf("unmarshal container repos for %s: %w", containerID, err)
		}

		for _, repo := range repos {
			rd := rb.Get([]byte(repo))
			if rd == nil {
				entries = append(entries, RepoEntry{URL: repo})
				continue
			}
			var entry RepoEntry
			if err := json.Unmarshal(rd, &entry); err != nil {
				return fmt.Errorf("unmarshal repo entry %s: %w", repo, err)
			}
			entries = append(entries, entry)
		}
		return nil
	})
	return entries, err
}
