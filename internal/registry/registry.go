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

	"github.com/depado/buoy/internal/docker"
	"github.com/depado/buoy/internal/types"
)

var (
	reposBucket          = []byte("repos")
	containerReposBucket = []byte("container_repos")
)

type Registry struct {
	db     *bolt.DB
	repos  []types.RepoRef
	logger *slog.Logger
}

func Open(path string, baseRepos []types.RepoRef, logger *slog.Logger) (*Registry, error) {
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
	logger.Debug("opened registry", "path", path)
	return &Registry{db: db, repos: baseRepos, logger: logger}, nil
}

func (r *Registry) Close() error {
	return r.db.Close()
}

func (r *Registry) SyncContainer(ctr *docker.Container, cfg docker.BackupConfig) ([]types.RepoRef, error) {
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

func (r *Registry) upsertRepoEntry(b *bolt.Bucket, ref types.RepoRef, ctr *docker.Container, now time.Time) error {
	key := []byte(ref.URL)
	existing := b.Get(key)

	var entry types.RepoEntry
	if existing != nil {
		if err := json.Unmarshal(existing, &entry); err != nil {
			return fmt.Errorf("unmarshal repo entry %s: %w", ref.URL, err)
		}
		if entry.ContainerID == ctr.ID &&
			entry.ContainerName == ctr.Name &&
			entry.ComposeProject == ctr.ComposeProject &&
			entry.ComposeService == ctr.ComposeService &&
			!entry.Orphaned {
			r.logger.Debug("repo entry unchanged", "repo", ref.URL, "container", ctr.Name)
			return nil
		}
		r.logger.Debug("updating repo entry", "repo", ref.URL, "container", ctr.Name)
		entry.ContainerID = ctr.ID
		entry.ContainerName = ctr.Name
		entry.ComposeProject = ctr.ComposeProject
		entry.ComposeService = ctr.ComposeService
		entry.RepoName = ref.Name
		entry.Orphaned = false
	} else {
		r.logger.Debug("creating repo entry", "repo", ref.URL, "container", ctr.Name)
		entry = types.RepoEntry{
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
	r.logger.Debug("marking backup complete", "repo", repo, "ok", ok)
	return r.updateRepoMeta(repo, func(entry *types.RepoEntry) {
		entry.LastBackupAt = time.Now()
		entry.LastBackupOK = ok
	})
}

func (r *Registry) MarkCheckComplete(repo string, ok bool) error {
	return r.updateRepoMeta(repo, func(entry *types.RepoEntry) {
		entry.LastCheckAt = time.Now()
		entry.LastCheckOK = ok
	})
}

func (r *Registry) updateRepoMeta(repo string, fn func(*types.RepoEntry)) error {
	return r.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket(reposBucket)
		data := b.Get([]byte(repo))
		if data == nil {
			return nil
		}
		var entry types.RepoEntry
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
	var count int
	err := r.db.Update(func(tx *bolt.Tx) error {
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
			var entry types.RepoEntry
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
			count++
		}

		return cb.Delete([]byte(containerID))
	})
	if err == nil && count > 0 {
		r.logger.Debug("marked repos orphaned", "container_id", containerID, "count", count)
	}
	return err
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

func (r *Registry) ListRepos(opts ...ListOption) ([]types.RepoEntry, error) {
	var cfg listConfig
	for _, o := range opts {
		o(&cfg)
	}

	var entries []types.RepoEntry
	err := r.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket(reposBucket)
		c := b.Cursor()
		for k, v := c.First(); k != nil; k, v = c.Next() {
			var entry types.RepoEntry
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

func (r *Registry) resolveRepos(ctr *docker.Container, cfg docker.BackupConfig) ([]types.RepoRef, error) {
	var refs []types.RepoRef

	if len(cfg.ReposOverride) > 0 {
		repoMap := make(map[string]types.RepoRef, len(r.repos))
		for _, nr := range r.repos {
			repoMap[nr.Name] = nr
		}
		for _, name := range cfg.ReposOverride {
			nr, ok := repoMap[name]
			if !ok {
				r.logger.Warn("unknown repo name in buoy.repos label, skipping", "repo_name", name, "container", ctr.Name)
				continue
			}
			refs = append(refs, types.RepoRef{Name: nr.Name, URL: nr.URL})
		}
	} else {
		for _, nr := range r.repos {
			refs = append(refs, types.RepoRef{Name: nr.Name, URL: nr.URL})
		}
	}

	repos := make([]types.RepoRef, 0, len(refs))
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
		repos = append(repos, types.RepoRef{Name: ref.Name, URL: path})
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

func (r *Registry) ResolveRepos(ctr *docker.Container, cfg docker.BackupConfig) ([]types.RepoRef, error) {
	return r.resolveRepos(ctr, cfg)
}

func (r *Registry) GetContainerRepos(containerID string) ([]types.RepoEntry, error) {
	var entries []types.RepoEntry
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
				entries = append(entries, types.RepoEntry{URL: repo})
				continue
			}
			var entry types.RepoEntry
			if err := json.Unmarshal(rd, &entry); err != nil {
				return fmt.Errorf("unmarshal repo entry %s: %w", repo, err)
			}
			entries = append(entries, entry)
		}
		return nil
	})
	return entries, err
}

type LastSuccessEntry struct {
	ContainerName  string
	ComposeProject string
	ComposeService string
	Timestamp      int64
}

func (r *Registry) LastSuccessTimestamps() ([]LastSuccessEntry, error) {
	entries, err := r.ListRepos(ExcludeOrphaned())
	if err != nil {
		return nil, err
	}
	best := make(map[string]LastSuccessEntry)
	for _, e := range entries {
		if !e.LastBackupOK {
			continue
		}
		ts := e.LastBackupAt.Unix()
		if existing, ok := best[e.ContainerName]; !ok || ts > existing.Timestamp {
			best[e.ContainerName] = LastSuccessEntry{
				ContainerName:  e.ContainerName,
				ComposeProject: e.ComposeProject,
				ComposeService: e.ComposeService,
				Timestamp:      ts,
			}
		}
	}
	result := make([]LastSuccessEntry, 0, len(best))
	for _, v := range best {
		result = append(result, v)
	}
	return result, nil
}
