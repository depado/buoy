package scheduler

import (
	"context"
	"log/slog"
	"sync"

	"github.com/robfig/cron/v3"

	"github.com/depado/buoy/internal/backup"
	"github.com/depado/buoy/internal/docker"
)

// Scheduler manages cron-triggered backup jobs for Docker containers.
// It provides per-container concurrency control (skip if already running)
// and a global concurrency limit via a semaphore.
type Scheduler struct {
	cron    *cron.Cron
	docker  *docker.Client
	backup  *backup.Runner
	sem     chan struct{}
	mu      sync.Mutex
	running sync.Map
	entries sync.Map
	logger  *slog.Logger
}

// New creates a new Scheduler. concurrency controls the maximum number of
// simultaneous backup jobs (minimum 1).
func New(d *docker.Client, r *backup.Runner, concurrency int, logger *slog.Logger) *Scheduler {
	if concurrency < 1 {
		concurrency = 1
	}

	return &Scheduler{
		cron: cron.New(
			cron.WithChain(
				cron.SkipIfStillRunning(cronLogger{logger}),
			),
		),
		docker: d,
		backup: r,
		sem:    make(chan struct{}, concurrency),
		logger: logger,
	}
}

// Start begins executing scheduled jobs.
func (s *Scheduler) Start() {
	s.cron.Start()
}

// Stop halts the scheduler and returns a context that completes when all
// running jobs have finished.
func (s *Scheduler) Stop() context.Context {
	return s.cron.Stop()
}

// AddContainer registers a container for scheduled backups.
// If the container has no schedule, it is skipped.
func (s *Scheduler) AddContainer(ctr *docker.Container) error {
	cfg := docker.ParseBackupConfig(ctr.Labels)
	if cfg.Schedule == "" {
		s.logger.Warn("no schedule, skipping", "container", ctr.Name)
		return nil
	}

	id := ctr.ID

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, loaded := s.entries.Load(id); loaded {
		s.logger.Warn("container already scheduled", "container", ctr.Name, "id", id)
		return nil
	}

	cid, err := s.cron.AddFunc(cfg.Schedule, func() {
		s.runJob(id, ctr)
	})
	if err != nil {
		return err
	}

	s.entries.Store(id, cid)
	s.logger.Info("scheduled container", "container", ctr.Name, "schedule", cfg.Schedule)
	return nil
}

// RemoveContainer removes a container from the schedule.
func (s *Scheduler) RemoveContainer(containerID string) {
	v, ok := s.entries.LoadAndDelete(containerID)
	if !ok {
		return
	}
	cid := v.(cron.EntryID)
	s.cron.Remove(cid)
}

// Resync re-scans all containers and updates the schedule.
// New containers are added, removed containers are dropped.
func (s *Scheduler) Resync(ctx context.Context) {
	containers, err := s.docker.ListBackupContainers(ctx)
	if err != nil {
		s.logger.Error("resync: failed to list containers", "error", err)
		return
	}

	active := make(map[string]bool)
	for _, ctr := range containers {
		active[ctr.ID] = true
		if _, ok := s.entries.Load(ctr.ID); !ok {
			ctr := ctr
			if err := s.AddContainer(&ctr); err != nil {
				s.logger.Warn("resync: failed to add container", "container", ctr.Name, "error", err)
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

func (s *Scheduler) runJob(containerID string, ctr *docker.Container) {
	if _, loaded := s.running.LoadOrStore(containerID, true); loaded {
		s.logger.Warn("backup already running, skipping", "container", ctr.Name)
		return
	}
	defer s.running.Delete(containerID)

	s.sem <- struct{}{}
	defer func() { <-s.sem }()

	ctx := context.Background()

	if err := s.backup.Run(ctx, ctr); err != nil {
		s.logger.Error("backup failed", "container", ctr.Name, "error", err)
	}
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
