package scheduler

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/robfig/cron/v3"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"

	"github.com/depado/buoy/client"
	"github.com/depado/buoy/internal/backup"
	"github.com/depado/buoy/internal/docker"
	"github.com/depado/buoy/internal/registry"
)

type Scheduler struct {
	cron             *cron.Cron
	docker           *docker.Client
	backup           *backup.Runner
	sem              chan struct{}
	wg               sync.WaitGroup
	containerReg     *containerRegistry
	repoReg          *registry.Registry
	stacks           map[string]*stackQueue
	stackMu          sync.Mutex
	active           atomic.Int64
	logger           *slog.Logger
	defaultSchedule  string
	defaultRetention string
	backupTimeout    time.Duration
	tracer           trace.Tracer
}

type Config struct {
	Docker           *docker.Client
	Runner           *backup.Runner
	Registry         *registry.Registry
	Concurrency      int
	DefaultSchedule  string
	DefaultRetention string
	BackupTimeout    time.Duration
	Logger           *slog.Logger
	Tracer           trace.Tracer
}

func New(cfg *Config) *Scheduler {
	if cfg.Concurrency < 1 {
		cfg.Concurrency = 1
	}

	c := cron.New(
		cron.WithChain(
			cron.Recover(cronLogger{cfg.Logger}),
			cron.SkipIfStillRunning(cronLogger{cfg.Logger}),
		),
	)

	tracer := cfg.Tracer
	if tracer == nil {
		tracer = noop.NewTracerProvider().Tracer("buoy")
	}
	return &Scheduler{
		cron:             c,
		docker:           cfg.Docker,
		backup:           cfg.Runner,
		sem:              make(chan struct{}, cfg.Concurrency),
		containerReg:     newContainerRegistry(c, cfg.Logger),
		repoReg:          cfg.Registry,
		stacks:           make(map[string]*stackQueue),
		logger:           cfg.Logger,
		defaultSchedule:  cfg.DefaultSchedule,
		defaultRetention: cfg.DefaultRetention,
		backupTimeout:    cfg.BackupTimeout,
		tracer:           tracer,
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
	cfg := docker.ParseBackupConfig(ctr.Labels, s.defaultSchedule, s.defaultRetention)
	if !cfg.Enabled {
		s.logger.Debug("container not enabled, skipping", ctr.LogAttrs()...)
		return nil
	}
	if cfg.Schedule == "" {
		s.logger.Warn("no schedule, skipping", ctr.LogAttrs()...)
		return nil
	}

	if _, err := s.repoReg.SyncContainer(ctr, cfg); err != nil {
		s.logger.Warn("failed to persist container repos", append(ctr.LogAttrs(), "error", err)...)
	}

	key := scheduleGroupKey(ctr.ComposeProject, cfg.Schedule)
	project := ctr.ComposeProject

	return s.containerReg.register(ctr, key, cfg.Schedule, func() {
		s.runScheduleGroup(key, project)
	})
}

func (s *Scheduler) RemoveContainer(containerID string) {
	if !s.containerReg.unregister(containerID) {
		return
	}
	if err := s.repoReg.MarkOrphaned(containerID); err != nil {
		s.logger.Warn("failed to mark container repos as orphaned", "container_id", containerID, "error", err)
	}
	s.logger.Debug("removed container from schedule", "id", containerID)
}

func (s *Scheduler) Running() bool {
	return s.active.Load() > 0
}

func (s *Scheduler) ListScheduled() []client.ScheduledEntry {
	infos := s.containerReg.listAll()
	entries := make([]client.ScheduledEntry, 0, len(infos))
	for _, info := range infos {
		ctr := info.ctr
		cfg := docker.ParseBackupConfig(ctr.Labels, s.defaultSchedule, s.defaultRetention)

		repoEntries, _ := s.repoReg.GetContainerRepos(ctr.ID)

		var repoList []client.ScheduledRepo
		if len(repoEntries) > 0 {
			repoList = make([]client.ScheduledRepo, 0, len(repoEntries))
			for _, re := range repoEntries {
				repoList = append(repoList, client.ScheduledRepo{
					URL:          re.URL,
					RepoName:     re.RepoName,
					Created:      !re.CreatedAt.IsZero(),
					LastBackupAt: re.LastBackupAt,
					LastBackupOK: re.LastBackupOK,
				})
			}
		} else {
			repoURLs, err := s.repoReg.ResolveRepos(ctr, cfg)
			if err != nil {
				s.logger.Warn("failed to resolve repos for scheduled container", "container", ctr.Name, "error", err)
				repoURLs = nil
			}
			repoList = make([]client.ScheduledRepo, 0, len(repoURLs))
			for _, ref := range repoURLs {
				repoList = append(repoList, client.ScheduledRepo{URL: ref.URL, RepoName: ref.Name})
			}
		}

		entries = append(entries, client.ScheduledEntry{
			ContainerID:    ctr.ID,
			ContainerName:  ctr.Name,
			ComposeProject: ctr.ComposeProject,
			ComposeService: ctr.ComposeService,
			Schedule:       info.schedule,
			Repos:          repoList,
			StopBefore:     cfg.StopBefore,
		})
	}
	return entries
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
		start := time.Now()
		s.backup.CheckKnownRepos(ctx)
		s.logger.Info("periodic restic check complete", slog.Duration("duration", time.Since(start)))
	})
	return err
}

func (s *Scheduler) Resync(ctx context.Context) {
	ctx, span := s.tracer.Start(ctx, "buoy.resync")
	defer span.End()

	containers, err := s.docker.ListBackupContainers(ctx)
	if err != nil {
		s.logger.Error("resync: failed to list containers", "error", err)
		span.SetStatus(codes.Error, err.Error())
		return
	}
	span.SetAttributes(attribute.Int("containers", len(containers)))
	s.logger.Debug("resync scan", "found", len(containers))

	active := make(map[string]bool)
	for _, ctr := range containers {
		active[ctr.ID] = true
		if !s.containerReg.has(ctr.ID) {
			ctr := ctr
			if err := s.AddContainer(&ctr); err != nil {
				s.logger.Warn("resync: failed to add container", append(ctr.LogAttrs(), "error", err)...)
			}
		}
	}

	s.containerReg.forEachEntry(func(id, _ string) bool {
		if !active[id] {
			s.RemoveContainer(id)
		}
		return true
	})
}

func (s *Scheduler) runScheduleGroup(key, project string) {
	ctrs := s.containerReg.getGroup(key)
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
				ctx, span := s.tracer.Start(context.Background(), "buoy.schedule.run")
				defer span.End()
				ctx, cancel := context.WithTimeout(ctx, s.backupTimeout)
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

		ctx, span := s.tracer.Start(context.Background(), "buoy.schedule.run",
			trace.WithAttributes(
				attribute.String("project", project),
			),
		)
		defer span.End()
		ctx, cancel := context.WithTimeout(ctx, s.backupTimeout)
		defer cancel()
		if err := s.backup.RunStackBatch(ctx, project, batch); err != nil {
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

func (s *Scheduler) TriggerBackup(ctx context.Context, identifier string) error {
	ctr := s.containerReg.find(identifier)
	if ctr == nil {
		return fmt.Errorf("container %q not found in scheduled backups", identifier)
	}

	if ctr.ComposeProject != "" {
		s.enqueueBatch(ctr.ComposeProject, []*docker.Container{ctr})
		return nil
	}

	s.active.Add(1)
	s.wg.Add(1)
	s.sem <- struct{}{}
	defer func() { <-s.sem }()
	defer s.active.Add(-1)
	defer s.wg.Done()

	ctx, span := s.tracer.Start(ctx, "buoy.schedule.run")
	defer span.End()
	if err := s.backup.Run(ctx, ctr); err != nil {
		s.logger.Error("triggered backup failed", append(ctr.LogAttrs(), "error", err)...)
		return err
	}
	return nil
}

func (s *Scheduler) TriggerProjectBackup(ctx context.Context, project string, services []string) error {
	batch := s.containerReg.findByProject(project)
	if len(batch) == 0 {
		return fmt.Errorf("project %q not found in scheduled backups", project)
	}

	if len(services) > 0 {
		include := make(map[string]bool, len(services))
		for _, svc := range services {
			include[svc] = true
		}
		filtered := batch[:0]
		for _, c := range batch {
			if include[c.ComposeService] {
				filtered = append(filtered, c)
			}
		}
		if len(filtered) == 0 {
			return fmt.Errorf("no matching services found in project %q", project)
		}
		batch = filtered
	}

	s.enqueueBatch(project, batch)
	return nil
}

type stackQueue struct {
	mu      sync.Mutex
	pending []*docker.Container
	active  bool
}

type cronLogger struct {
	*slog.Logger
}

func (l cronLogger) Info(msg string, keysAndValues ...any) {
	l.Debug(msg, keysAndValues...)
}

func (l cronLogger) Error(err error, msg string, keysAndValues ...any) {
	args := append([]any{"error", err}, keysAndValues...)
	l.Logger.Error(msg, args...)
}
