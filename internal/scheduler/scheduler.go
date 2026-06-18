package scheduler

import (
	"context"
	"log/slog"
	"sync"

	"github.com/robfig/cron/v3"

	"github.com/depado/buoy/internal/backup"
	"github.com/depado/buoy/internal/docker"
)

type Scheduler struct {
	cron    *cron.Cron
	docker  *docker.Client
	backup  *backup.Runner
	sem     chan struct{}
	mu      sync.Mutex
	running sync.Map
	entries sync.Map
	groups  map[string][]*docker.Container
	cronIDs map[string]cron.EntryID
	stacks  map[string]*stackQueue
	stackMu           sync.Mutex
	logger            *slog.Logger
	defaultSchedule   string
	defaultRetention  string
}

func New(d *docker.Client, r *backup.Runner, concurrency int, defaultSchedule, defaultRetention string, logger *slog.Logger) *Scheduler {
	if concurrency < 1 {
		concurrency = 1
	}

	return &Scheduler{
		cron: cron.New(
			cron.WithChain(
				cron.SkipIfStillRunning(cronLogger{logger}),
			),
		),
		docker:           d,
		backup:           r,
		sem:              make(chan struct{}, concurrency),
		groups:           make(map[string][]*docker.Container),
		cronIDs:          make(map[string]cron.EntryID),
		stacks:           make(map[string]*stackQueue),
		logger:           logger,
		defaultSchedule:  defaultSchedule,
		defaultRetention: defaultRetention,
	}
}

func (s *Scheduler) Start() {
	s.cron.Start()
}

func (s *Scheduler) Stop() context.Context {
	return s.cron.Stop()
}

func (s *Scheduler) AddContainer(ctr *docker.Container) error {
	cfg := docker.ParseBackupConfig(ctr.Labels, s.defaultSchedule, s.defaultRetention)
	if cfg.Schedule == "" {
		s.logger.Warn("no schedule, skipping", ctr.LogAttrs()...)
		return nil
	}

	key := scheduleGroupKey(ctr.ComposeProject, cfg.Schedule)

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, loaded := s.entries.Load(ctr.ID); loaded {
		return nil
	}

	if _, exists := s.cronIDs[key]; !exists {
		cid, err := s.cron.AddFunc(cfg.Schedule, func() {
			s.runScheduleGroup(key, ctr.ComposeProject)
		})
		if err != nil {
			return err
		}
		s.cronIDs[key] = cid
	}

	s.entries.Store(ctr.ID, key)
	s.groups[key] = append(s.groups[key], ctr)
	s.logger.Debug("scheduled container", append(ctr.LogAttrs(), "schedule", cfg.Schedule)...)
	return nil
}

func (s *Scheduler) RemoveContainer(containerID string) {
	v, ok := s.entries.LoadAndDelete(containerID)
	if !ok {
		return
	}
	groupKey := v.(string)

	s.mu.Lock()
	defer s.mu.Unlock()

	ctrs := s.groups[groupKey]
	for i, c := range ctrs {
		if c.ID == containerID {
			s.groups[groupKey] = append(ctrs[:i], ctrs[i+1:]...)
			break
		}
	}

	if len(s.groups[groupKey]) == 0 {
		delete(s.groups, groupKey)
		if cid, ok := s.cronIDs[groupKey]; ok {
			s.cron.Remove(cid)
			delete(s.cronIDs, groupKey)
		}
	}
}

func (s *Scheduler) Resync(ctx context.Context) {
	containers, err := s.docker.ListBackupContainers(ctx)
	if err != nil {
		s.logger.Error("resync: failed to list containers", "error", err)
		return
	}
	s.logger.Debug("resync scan", "found", len(containers))

	active := make(map[string]bool)
	for _, ctr := range containers {
		active[ctr.ID] = true
		if _, ok := s.entries.Load(ctr.ID); !ok {
			ctr := ctr
			if err := s.AddContainer(&ctr); err != nil {
				s.logger.Warn("resync: failed to add container", append(ctr.LogAttrs(), "error", err)...)
			}
		}
	}

	s.entries.Range(func(key, _ any) bool {
		id := key.(string)
		if !active[id] {
			s.RemoveContainer(id)
		}
		return true
	})
}

func (s *Scheduler) runScheduleGroup(key, project string) {
	s.mu.Lock()
	ctrs := make([]*docker.Container, len(s.groups[key]))
	copy(ctrs, s.groups[key])
	s.mu.Unlock()

	if len(ctrs) == 0 {
		return
	}

	if project == "" {
		for _, ctr := range ctrs {
			if _, loaded := s.running.LoadOrStore(ctr.ID, true); loaded {
				s.logger.Warn("backup already running, skipping", ctr.LogAttrs()...)
				continue
			}
			s.sem <- struct{}{}
			go func(c *docker.Container) {
				defer func() { <-s.sem }()
				defer s.running.Delete(c.ID)
				if err := s.backup.Run(context.Background(), c); err != nil {
					s.logger.Error("backup failed", append(c.LogAttrs(), "error", err)...)
				}
			}(ctr)
		}
		return
	}

	s.enqueueBatch(project, ctrs)
}

func (s *Scheduler) enqueueBatch(project string, batch []*docker.Container) {
	q := s.getStackQueue(project)

	q.mu.Lock()
	q.pending = append(q.pending, batch...)
	if q.active {
		s.logger.Debug("stack queue busy, batch queued", "project", project, "count", len(batch))
		q.mu.Unlock()
		return
	}
	q.active = true
	q.mu.Unlock()

	s.sem <- struct{}{}
	defer func() { <-s.sem }()

	for {
		q.mu.Lock()
		batch := q.pending
		q.pending = nil
		q.mu.Unlock()

		if len(batch) == 0 {
			q.mu.Lock()
			if len(q.pending) == 0 {
				q.active = false
				q.mu.Unlock()
				return
			}
			q.mu.Unlock()
			continue
		}

		names := make([]string, len(batch))
		for i, c := range batch {
			if c.ComposeService != "" {
				names[i] = c.ComposeService
			} else {
				names[i] = c.Name
			}
		}
		s.logger.Debug("processing stack batch", "project", project, "containers", names)

		if err := s.backup.RunStackBatch(context.Background(), project, batch); err != nil {
			s.logger.Error("stack batch backup failed", "project", project, "error", err)
		}
	}
}

func (s *Scheduler) getStackQueue(project string) *stackQueue {
	s.stackMu.Lock()
	defer s.stackMu.Unlock()
	q, ok := s.stacks[project]
	if !ok {
		q = &stackQueue{}
		s.stacks[project] = q
	}
	return q
}

func scheduleGroupKey(project, schedule string) string {
	return project + "::" + schedule
}

type stackQueue struct {
	mu      sync.Mutex
	pending []*docker.Container
	active  bool
}

type cronLogger struct {
	*slog.Logger
}

func (l cronLogger) Info(msg string, keysAndValues ...interface{}) {
	l.Logger.Info(msg, keysAndValues...)
}

func (l cronLogger) Error(err error, msg string, keysAndValues ...interface{}) {
	args := append([]any{"error", err}, keysAndValues...)
	l.Logger.Error(msg, args...)
}
