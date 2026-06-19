package scheduler

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/robfig/cron/v3"

	"github.com/depado/buoy/internal/backup"
	"github.com/depado/buoy/internal/docker"
)

type Scheduler struct {
	cron             *cron.Cron
	docker           *docker.Client
	backup           *backup.Runner
	sem              chan struct{}
	wg               sync.WaitGroup
	registry         *containerRegistry
	stacks           map[string]*stackQueue
	stackMu          sync.Mutex
	active           atomic.Int64
	logger           *slog.Logger
	defaultSchedule  string
	defaultRetention string
	backupTimeout    time.Duration
}

func New(d *docker.Client, r *backup.Runner, concurrency int, defaultSchedule, defaultRetention string, backupTimeout time.Duration, logger *slog.Logger) *Scheduler {
	if concurrency < 1 {
		concurrency = 1
	}

	c := cron.New(
		cron.WithChain(
			cron.Recover(cronLogger{logger}),
			cron.SkipIfStillRunning(cronLogger{logger}),
		),
	)

	return &Scheduler{
		cron:             c,
		docker:           d,
		backup:           r,
		sem:              make(chan struct{}, concurrency),
		registry:         newContainerRegistry(c, logger),
		stacks:           make(map[string]*stackQueue),
		logger:           logger,
		defaultSchedule:  defaultSchedule,
		defaultRetention: defaultRetention,
		backupTimeout:    backupTimeout,
	}
}

func (s *Scheduler) Start() {
	s.cron.Start()
}

func (s *Scheduler) Stop() context.Context {
	cronDone := s.cron.Stop()
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		<-cronDone.Done()
		s.wg.Wait()
		cancel()
	}()
	return ctx
}

func (s *Scheduler) AddContainer(ctr *docker.Container) error {
	cfg := docker.ParseBackupConfig(ctr.Labels, s.defaultSchedule, s.defaultRetention, s.logger)
	if cfg.Schedule == "" {
		s.logger.Warn("no schedule, skipping", ctr.LogAttrs()...)
		return nil
	}

	key := scheduleGroupKey(ctr.ComposeProject, cfg.Schedule)
	project := ctr.ComposeProject

	return s.registry.register(ctr, key, cfg.Schedule, func() {
		s.runScheduleGroup(key, project)
	})
}

func (s *Scheduler) RemoveContainer(containerID string) {
	s.registry.unregister(containerID)
}

func (s *Scheduler) Running() bool {
	return s.active.Load() > 0
}

func (s *Scheduler) ScheduleCheck(schedule string) error {
	if schedule == "" {
		return nil
	}
	_, err := s.cron.AddFunc(schedule, func() {
		s.active.Add(1)
		s.wg.Add(1)
		for i := 0; i < cap(s.sem); i++ {
			s.sem <- struct{}{}
		}
		defer func() {
			for i := 0; i < cap(s.sem); i++ {
				<-s.sem
			}
		}()
		defer s.active.Add(-1)
		defer s.wg.Done()

		s.logger.Info("running periodic restic check")
		ctx, cancel := context.WithTimeout(context.Background(), s.backupTimeout)
		defer cancel()
		s.backup.CheckKnownRepos(ctx)
	})
	return err
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
		if !s.registry.has(ctr.ID) {
			ctr := ctr
			if err := s.AddContainer(&ctr); err != nil {
				s.logger.Warn("resync: failed to add container", append(ctr.LogAttrs(), "error", err)...)
			}
		}
	}

	s.registry.forEachEntry(func(id, _ string) bool {
		if !active[id] {
			s.RemoveContainer(id)
		}
		return true
	})
}

func (s *Scheduler) runScheduleGroup(key, project string) {
	ctrs := s.registry.getGroup(key)
	if len(ctrs) == 0 {
		return
	}

	if project == "" {
		for _, ctr := range ctrs {
			s.active.Add(1)
			s.wg.Add(1)
			s.sem <- struct{}{}
			go func(c *docker.Container) {
				defer s.wg.Done()
				defer func() { <-s.sem }()
				defer s.active.Add(-1)
				ctx, cancel := context.WithTimeout(context.Background(), s.backupTimeout)
				defer cancel()
				if err := s.backup.Run(ctx, c); err != nil {
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

	s.active.Add(1)
	s.wg.Add(1)
	s.sem <- struct{}{}

	defer func() {
		q.mu.Lock()
		q.active = false
		q.mu.Unlock()
	}()

	defer func() { <-s.sem }()
	defer s.active.Add(-1)
	defer s.wg.Done()

	for {
		q.mu.Lock()
		batch := q.pending
		q.pending = nil
		q.mu.Unlock()

		if len(batch) == 0 {
			return
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

		ctx, cancel := context.WithTimeout(context.Background(), s.backupTimeout)
		if err := s.backup.RunStackBatch(ctx, project, batch); err != nil {
			s.logger.Error("stack batch backup failed", "project", project, "error", err)
		}
		cancel()
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

type stackQueue struct {
	mu      sync.Mutex
	pending []*docker.Container
	active  bool
}

type cronLogger struct {
	*slog.Logger
}

func (l cronLogger) Info(msg string, keysAndValues ...interface{}) {
	l.Debug(msg, keysAndValues...)
}

func (l cronLogger) Error(err error, msg string, keysAndValues ...interface{}) {
	args := append([]any{"error", err}, keysAndValues...)
	l.Logger.Error(msg, args...)
}
