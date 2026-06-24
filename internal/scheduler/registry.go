package scheduler

import (
	"log/slog"
	"sync"

	"github.com/robfig/cron/v3"

	"github.com/depado/buoy/internal/docker"
)

type containerRegistry struct {
	mu      sync.Mutex
	groups  map[string][]*docker.Container
	index   map[string]string
	cronIDs map[string]cron.EntryID
	cron    *cron.Cron
	logger  *slog.Logger
}

func newContainerRegistry(c *cron.Cron, logger *slog.Logger) *containerRegistry {
	return &containerRegistry{
		groups:  make(map[string][]*docker.Container),
		index:   make(map[string]string),
		cronIDs: make(map[string]cron.EntryID),
		cron:    c,
		logger:  logger,
	}
}

func (r *containerRegistry) register(ctr *docker.Container, key, schedule string, fn func()) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if existingKey, exists := r.index[ctr.ID]; exists {
		if existingKey == key {
			return nil
		}
		r.unregisterLocked(ctr.ID)
	}

	if _, exists := r.cronIDs[key]; !exists {
		cid, err := r.cron.AddFunc(schedule, fn)
		if err != nil {
			return err
		}
		r.cronIDs[key] = cid
	}

	r.index[ctr.ID] = key
	r.groups[key] = append(r.groups[key], ctr)
	r.logger.Debug("scheduled container", append(ctr.LogAttrs(), "schedule", schedule)...)
	return nil
}

func (r *containerRegistry) unregister(containerID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.unregisterLocked(containerID)
}

func (r *containerRegistry) unregisterLocked(containerID string) {
	groupKey, ok := r.index[containerID]
	if !ok {
		return
	}
	delete(r.index, containerID)

	ctrs := r.groups[groupKey]
	for i, c := range ctrs {
		if c.ID == containerID {
			r.groups[groupKey] = append(ctrs[:i], ctrs[i+1:]...)
			break
		}
	}

	if len(r.groups[groupKey]) == 0 {
		delete(r.groups, groupKey)
		if cid, ok := r.cronIDs[groupKey]; ok {
			r.cron.Remove(cid)
			delete(r.cronIDs, groupKey)
		}
	}
}

func (r *containerRegistry) getGroup(key string) []*docker.Container {
	r.mu.Lock()
	ctrs := make([]*docker.Container, len(r.groups[key]))
	copy(ctrs, r.groups[key])
	r.mu.Unlock()
	return ctrs
}

func (r *containerRegistry) has(containerID string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	_, ok := r.index[containerID]
	return ok
}

func (r *containerRegistry) forEachEntry(fn func(id, key string) bool) {
	r.mu.Lock()
	entries := make(map[string]string, len(r.index))
	for id, key := range r.index {
		entries[id] = key
	}
	r.mu.Unlock()

	for id, key := range entries {
		if !fn(id, key) {
			return
		}
	}
}

type entryInfo struct {
	ctr      *docker.Container
	schedule string
}

func (r *containerRegistry) listAll() []entryInfo {
	r.mu.Lock()
	entries := make([]entryInfo, 0, len(r.index))
	for id, key := range r.index {
		schedule := scheduleFromKey(key)
		for _, ctr := range r.groups[key] {
			if ctr.ID == id {
				entries = append(entries, entryInfo{ctr: ctr, schedule: schedule})
				break
			}
		}
	}
	r.mu.Unlock()
	return entries
}

func scheduleGroupKey(project, schedule string) string {
	return project + "::" + schedule
}

func scheduleFromKey(key string) string {
	if idx := lastIndex(key, "::"); idx >= 0 {
		return key[idx+2:]
	}
	return key
}

func lastIndex(s, sep string) int {
	for i := len(s) - len(sep); i >= 0; i-- {
		if s[i:i+len(sep)] == sep {
			return i
		}
	}
	return -1
}
