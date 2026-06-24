package backup

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/moby/moby/api/types/container"

	"github.com/depado/buoy/internal/docker"
	"github.com/depado/buoy/internal/hook"
	"github.com/depado/buoy/internal/notify"
	"github.com/depado/buoy/internal/registry"
	"github.com/depado/buoy/internal/restic"
)

type Runner struct {
	docker            *docker.Client
	restic            *restic.Client
	hook              *hook.Executor
	repoReg           *registry.Registry
	defaultSchedule   string
	defaultRetention  string
	ignoredIDs        *sync.Map
	notifier          *notify.Notifier
	logger            *slog.Logger
	execTimeout       time.Duration
	healthWaitTimeout time.Duration
	backupTimeout     time.Duration
}

// RunnerConfig holds the dependencies and configuration for a Runner.
type RunnerConfig struct {
	Docker            *docker.Client
	Restic            *restic.Client
	Hook              *hook.Executor
	Registry          *registry.Registry
	DefaultSchedule   string
	DefaultRetention  string
	IgnoredIDs        *sync.Map
	Notifier          *notify.Notifier
	Logger            *slog.Logger
	ExecTimeout       time.Duration
	HealthWaitTimeout time.Duration
	BackupTimeout     time.Duration
}

func New(cfg *RunnerConfig) *Runner {
	return &Runner{
		docker:            cfg.Docker,
		restic:            cfg.Restic,
		hook:              cfg.Hook,
		repoReg:           cfg.Registry,
		defaultSchedule:   cfg.DefaultSchedule,
		defaultRetention:  cfg.DefaultRetention,
		ignoredIDs:        cfg.IgnoredIDs,
		notifier:          cfg.Notifier,
		logger:            cfg.Logger,
		execTimeout:       cfg.ExecTimeout,
		healthWaitTimeout: cfg.HealthWaitTimeout,
		backupTimeout:     cfg.BackupTimeout,
	}
}

func (r *Runner) parseConfig(labels map[string]string) docker.BackupConfig {
	return docker.ParseBackupConfig(labels, r.defaultSchedule, r.defaultRetention)
}

func (r *Runner) Run(ctx context.Context, ctr *docker.Container) error {
	l := r.logger.With(ctr.LogAttrs()...)

	fresh, err := r.docker.InspectContainer(ctx, ctr.ID)
	if err != nil {
		return fmt.Errorf("inspect: %w", err)
	}

	if fresh.State != "running" {
		l.Debug("skipping, not running", "state", fresh.State)
		return nil
	}

	cfg := r.parseConfig(fresh.Labels)

	repos, err := r.repoReg.SyncContainer(fresh, cfg)
	if err != nil {
		l.Warn("failed to sync container repos", "error", err)
	}
	if len(repos) == 0 {
		return fmt.Errorf("no repos resolved for container %s", ctr.Name)
	}

	r.runPreHooks(ctx, fresh, cfg, l)

	l.Info("starting backup", "stop", cfg.StopBefore)
	wasRunning := false
	if cfg.StopBefore {
		r.ignore(fresh.ID)
		defer r.release(fresh.ID)
		var err error
		wasRunning, err = r.stopContainer(ctx, fresh, cfg, l)
		if err != nil {
			return err
		}
	}

	backupErr := r.backupMounts(ctx, fresh, cfg, repos, l)

	if wasRunning {
		r.startContainer(ctx, fresh, l)
		r.waitRunning(ctx, fresh)
	}

	r.runPostHooks(ctx, fresh, cfg, l)

	var issues []string
	if backupErr != nil {
		issues = append(issues, fmt.Sprintf("Backup: %s", backupErr.Error()))
	} else {
		issues = append(issues, r.applyRetention(ctx, fresh, cfg, repos, l)...)
	}

	if len(issues) > 0 {
		l.Error("backup completed with failures")
		msg := strings.Join(issues, "\n")
		r.notifier.SendBackupError(ctr.Name, msg)
		if backupErr != nil {
			return backupErr
		}
		return nil
	}

	l.Info("backup complete")
	r.notifier.SendInfo(
		fmt.Sprintf("buoy backup complete: %s", ctr.Name),
		fmt.Sprintf("Backup completed for container %s", ctr.Name),
	)
	return nil
}

func (r *Runner) stopContainer(ctx context.Context, ctr *docker.Container, cfg docker.BackupConfig, l *slog.Logger) (bool, error) {
	l.Debug("stopping container")
	if err := r.docker.StopContainer(ctx, ctr.ID, cfg.StopTimeout); err != nil {
		return false, fmt.Errorf("stop container: %w", err)
	}
	if err := r.docker.ContainerWait(ctx, ctr.ID, container.WaitConditionNotRunning); err != nil {
		l.Warn("container did not stop in time, aborting backup", "error", err)
		if startErr := r.docker.StartContainer(ctx, ctr.ID); startErr != nil {
			l.Error("failed to restart container after failed stop", "error", startErr)
		}
		return false, fmt.Errorf("wait for stop: %w", err)
	}
	l.Info("container stopped")
	return true, nil
}

func (r *Runner) startContainer(ctx context.Context, ctr *docker.Container, l *slog.Logger) {
	l.Debug("starting container")
	if err := r.docker.StartContainer(ctx, ctr.ID); err != nil {
		l.Error("start container failed", "error", err)
		return
	}
	l.Info("container started")
}

func (r *Runner) RunStackBatch(ctx context.Context, project string, batch []*docker.Container) error {
	l := r.logger.With("project", project)

	summaries, err := r.docker.ListContainersByProject(ctx, project)
	if err != nil {
		return fmt.Errorf("list stack containers: %w", err)
	}
	l.Debug("listed stack containers", "count", len(summaries))

	all, byID := r.inspectStackContainers(ctx, summaries, l)

	fresh := make([]*docker.Container, 0, len(batch))
	for _, ctr := range batch {
		inspected, ok := byID[ctr.ID]
		if !ok {
			l.Warn("batch container not found in stack", "id", ctr.ID)
			continue
		}
		fresh = append(fresh, inspected)
	}
	fresh = deduplicateByService(fresh)

	containerCfg, containerRepos := r.resolveStackConfigs(fresh, l)

	r.runParallelPreHooks(ctx, fresh, containerCfg, l)

	stopSvc := stopSet(fresh, all)
	l.Info("starting stack backup", "services", len(fresh), "stop_set", mapKeys(stopSvc))
	services := serviceContainers(all)
	deps := serviceDeps(all)

	wasStopped, stopFailed, ignored := r.stopStackServices(ctx, services, stopSvc, deps, project, l)
	defer r.releaseBatch(ignored)

	backupErrors, allIssues := r.backupStackServices(ctx, fresh, containerCfg, containerRepos, stopFailed, l)

	r.startStackServices(ctx, services, deps, all, wasStopped, l)

	for _, ctr := range all {
		if wasStopped[ctr.ID] {
			r.waitRunning(ctx, ctr)
		}
	}

	r.runPostHooksAndRetention(ctx, fresh, containerCfg, containerRepos, backupErrors, &allIssues, l)

	l.Info("stack backup complete", "services", len(fresh)-len(backupErrors), "failed", len(backupErrors))
	return r.finalizeStackBackup(project, len(fresh), len(backupErrors), allIssues)
}

func (r *Runner) inspectStackContainers(ctx context.Context, summaries []docker.Container, l *slog.Logger) ([]*docker.Container, map[string]*docker.Container) {
	all := make([]*docker.Container, 0, len(summaries))
	byID := make(map[string]*docker.Container, len(summaries))
	for i := range summaries {
		ctr, err := r.docker.InspectContainer(ctx, summaries[i].ID)
		if err != nil {
			l.Warn("failed to inspect stack container", "id", summaries[i].ID, "error", err)
			continue
		}
		all = append(all, ctr)
		byID[ctr.ID] = ctr
	}
	return all, byID
}

func (r *Runner) resolveStackConfigs(fresh []*docker.Container, l *slog.Logger) (map[string]docker.BackupConfig, map[string][]string) {
	cfgs := make(map[string]docker.BackupConfig, len(fresh))
	repos := make(map[string][]string, len(fresh))
	for _, ctr := range fresh {
		cfg := r.parseConfig(ctr.Labels)
		resolved, err := r.repoReg.SyncContainer(ctr, cfg)
		if err != nil {
			l.Warn("failed to sync container repos", "service", ctr.ComposeService, "error", err)
		}
		cfgs[ctr.ID] = cfg
		repos[ctr.ID] = resolved
	}
	return cfgs, repos
}

func (r *Runner) runParallelPreHooks(ctx context.Context, fresh []*docker.Container, cfgs map[string]docker.BackupConfig, l *slog.Logger) {
	var wg sync.WaitGroup
	for _, ctr := range fresh {
		wg.Add(1)
		go func(c *docker.Container) {
			defer wg.Done()
			r.runPreHooks(ctx, c, cfgs[c.ID], l.With(c.LogAttrs()...))
		}(ctr)
	}
	wg.Wait()
}

func (r *Runner) stopStackServices(
	ctx context.Context,
	services map[string][]*docker.Container,
	stopSvc map[string]bool,
	deps map[string][]depInfo,
	project string,
	l *slog.Logger,
) (wasStopped, stopFailed, ignored map[string]bool) {
	wasStopped = make(map[string]bool)
	stopFailed = make(map[string]bool)
	ignored = make(map[string]bool)

	stopOrder := orderForStopFromDeps(deps, func(svc string) {
		l.Warn("dependency cycle detected", "service", svc, "project", project)
	})
	for _, svc := range stopOrder {
		for _, ctr := range services[svc] {
			if !stopSvc[svc] {
				continue
			}
			sl := l.With(ctr.LogAttrs()...)
			r.ignore(ctr.ID)
			ignored[ctr.ID] = true
			sl.Debug("stopping container")
			cfg := r.parseConfig(ctr.Labels)
			if err := r.docker.StopContainer(ctx, ctr.ID, cfg.StopTimeout); err != nil {
				sl.Warn("failed to stop container", "error", err)
				stopFailed[ctr.ID] = true
				r.release(ctr.ID)
				delete(ignored, ctr.ID)
				continue
			}
			if err := r.docker.ContainerWait(ctx, ctr.ID, container.WaitConditionNotRunning); err != nil {
				sl.Warn("failed to wait for container stop", "error", err)
			}
			sl.Info("container stopped")
			wasStopped[ctr.ID] = true
		}
	}
	return
}

func (r *Runner) backupStackServices(
	ctx context.Context,
	fresh []*docker.Container,
	cfgs map[string]docker.BackupConfig,
	repos map[string][]string,
	stopFailed map[string]bool,
	l *slog.Logger,
) (map[string]error, []string) {
	backupErrors := make(map[string]error)
	var allIssues []string
	for _, ctr := range fresh {
		if stopFailed[ctr.ID] {
			l.With("service", ctr.ComposeService).Warn("skipping backup, stop failed")
			backupErrors[ctr.ComposeService] = fmt.Errorf("stop failed")
			allIssues = append(allIssues, fmt.Sprintf("%s: stop failed", ctr.ComposeService))
			continue
		}
		if err := r.backupMounts(ctx, ctr, cfgs[ctr.ID], repos[ctr.ID], l.With("service", ctr.ComposeService)); err != nil {
			l.Error("backup failed for service", "service", ctr.ComposeService, "error", err)
			backupErrors[ctr.ComposeService] = err
			allIssues = append(allIssues, fmt.Sprintf("%s: %s", ctr.ComposeService, err.Error()))
		}
	}
	return backupErrors, allIssues
}

func (r *Runner) startStackServices(
	ctx context.Context,
	services map[string][]*docker.Container,
	deps map[string][]depInfo,
	all []*docker.Container,
	wasStopped map[string]bool,
	l *slog.Logger,
) {
	startOrder := orderForStartFromDeps(deps, func(svc string) {
		l.Warn("dependency cycle detected", "service", svc)
	})
	for _, svc := range startOrder {
		if err := r.waitForDeps(ctx, deps, all, svc, l); err != nil {
			l.Warn("failed waiting for dependencies", "service", svc, "error", err)
		}
		for _, ctr := range services[svc] {
			if !wasStopped[ctr.ID] {
				continue
			}
			cl := l.With(ctr.LogAttrs()...)
			cl.Debug("starting container")
			if err := r.docker.StartContainer(ctx, ctr.ID); err != nil {
				cl.Warn("failed to start container", "error", err)
			} else {
				cl.Info("container started")
			}
		}
	}
}

func (r *Runner) runPostHooksAndRetention(
	ctx context.Context,
	fresh []*docker.Container,
	cfgs map[string]docker.BackupConfig,
	repos map[string][]string,
	backupErrors map[string]error,
	allIssues *[]string,
	l *slog.Logger,
) {
	for _, ctr := range fresh {
		cfg := cfgs[ctr.ID]
		r.runPostHooks(ctx, ctr, cfg, l)
		if _, failed := backupErrors[ctr.ComposeService]; !failed {
			for _, ri := range r.applyRetention(ctx, ctr, cfg, repos[ctr.ID], l) {
				*allIssues = append(*allIssues, fmt.Sprintf("%s: %s", ctr.ComposeService, ri))
			}
		}
	}
}

func (r *Runner) releaseBatch(ignored map[string]bool) {
	for id := range ignored {
		r.release(id)
	}
}

func (r *Runner) finalizeStackBackup(project string, total, failed int, allIssues []string) error {
	if len(allIssues) == 0 {
		r.notifier.SendInfo(
			fmt.Sprintf("buoy stack backup complete: %s", project),
			fmt.Sprintf("Backup completed for project %s (%d services)", project, total),
		)
		return nil
	}
	r.notifier.SendBackupError(project, strings.Join(allIssues, "\n"))
	if failed > 0 {
		return fmt.Errorf("stack backup: %d/%d services failed", failed, total)
	}
	return nil
}

func (r *Runner) runPreHooks(ctx context.Context, ctr *docker.Container, cfg docker.BackupConfig, l *slog.Logger) {
	if cfg.HookPreCmd != "" {
		if err := r.hook.ExecOnHost(ctx, cfg.HookPreCmd); err != nil {
			l.Warn("pre-backup host command failed", "error", err)
		}
	}
	if cfg.HookPreExec != "" {
		execCtx, cancel := context.WithTimeout(ctx, r.execTimeout)
		defer cancel()
		if err := r.hook.ExecInContainer(execCtx, ctr.ID, cfg.HookPreExec); err != nil {
			l.Warn("pre-backup exec failed", "error", err)
		}
	}
}

func (r *Runner) runPostHooks(ctx context.Context, ctr *docker.Container, cfg docker.BackupConfig, l *slog.Logger) {
	if cfg.HookPostExec != "" {
		execCtx, cancel := context.WithTimeout(ctx, r.execTimeout)
		defer cancel()
		if err := r.hook.ExecInContainer(execCtx, ctr.ID, cfg.HookPostExec); err != nil {
			l.Warn("post-backup exec failed", "error", err)
		}
	}
	if cfg.HookPostCmd != "" {
		if err := r.hook.ExecOnHost(ctx, cfg.HookPostCmd); err != nil {
			l.Warn("post-backup host command failed", "error", err)
		}
	}
}

type mountError struct {
	mount string
	repo  string
	err   error
}

func (r *Runner) backupMounts(ctx context.Context, ctr *docker.Container, cfg docker.BackupConfig, repos []string, logger *slog.Logger) error {
	mountCount := 0
	var failures []mountError

	for _, repo := range repos {
		if !r.ensureRepo(ctx, repo, logger, &failures) {
			continue
		}

		l := logger.With("repo", repo)
		repoOK := true

		for _, m := range ctr.Mounts {
			if m.Type == "tmpfs" {
				continue
			}

			matchedName, ok := docker.MountMatches(m, cfg.Include, cfg.Exclude)
			if !ok {
				continue
			}

			mountCount++
			if repoErr := r.backupSingleMount(ctx, repo, ctr.Name, m, matchedName, cfg, l); repoErr != nil {
				failures = append(failures, *repoErr)
				repoOK = false
			}
		}
		_ = r.repoReg.MarkBackupComplete(repo, repoOK)
	}

	if mountCount == 0 {
		logger.Warn("container has no backup-eligible mounts")
	}

	return summarizeFailures(failures, mountCount, len(repos))
}

func (r *Runner) ensureRepo(ctx context.Context, repo string, logger *slog.Logger, failures *[]mountError) bool {
	l := logger.With("repo", repo)

	exists, err := r.restic.RepoExists(ctx, repo)
	if err != nil {
		l.Warn("failed to check repo, skipping repo", "error", err)
		*failures = append(*failures, mountError{repo: repo, err: fmt.Errorf("repo check: %w", err)})
		r.repoReg.MarkBackupComplete(repo, false) //nolint:errcheck
		return false
	}
	if !exists {
		l.Debug("repo not found, initializing")
		if err := r.restic.Init(ctx, repo); err != nil {
			l.Warn("failed to init repo, skipping repo", "error", err)
			*failures = append(*failures, mountError{repo: repo, err: fmt.Errorf("repo init: %w", err)})
			r.repoReg.MarkBackupComplete(repo, false) //nolint:errcheck
			return false
		}
		l.Info("initialized repo")
	}

	if err := r.restic.Unlock(ctx, repo); err != nil {
		l.Warn("failed to unlock repo", "error", err)
	}
	return true
}

func (r *Runner) backupSingleMount(
	ctx context.Context,
	repo string,
	hostname string,
	m docker.Mount,
	matchedName string,
	cfg docker.BackupConfig,
	l *slog.Logger,
) *mountError {
	source := m.Source
	if _, err := os.Stat(source); os.IsNotExist(err) {
		l.Warn("mount source does not exist, skipping", "source", source, "type", m.Type)
		return &mountError{mount: source, repo: repo, err: fmt.Errorf("mount source not found")}
	}

	mountTag := m.Name
	if mountTag == "" {
		mountTag = m.Destination
	}

	files, excludes, tags := cfg.ResolveMountBackup(matchedName)

	opts := restic.BackupOptions{
		Tags:     append(tags, "mount:"+mountTag),
		Excludes: excludes,
		Files:    files,
		Hostname: hostname,
		WorkDir:  source,
	}

	paths := []string{"."}
	if len(files) == 0 {
		entries, err := os.ReadDir(source)
		if err != nil {
			l.Warn("failed to read source directory, skipping", "source", source, "error", err)
			return &mountError{mount: source, repo: repo, err: fmt.Errorf("read dir: %w", err)}
		}
		if len(entries) == 0 {
			l.Debug("source directory is empty, skipping", "source", source)
			return nil
		}
		paths = make([]string, 0, len(entries))
		for _, e := range entries {
			paths = append(paths, e.Name())
		}
	}

	result, err := r.restic.Backup(ctx, repo, paths, opts)
	if err != nil {
		if result != nil {
			l.Error("backup completed with errors",
				"repo", repo, "mount", source,
				"snapshot", result.SnapshotID, "error", err,
			)
		} else {
			l.Error("backup failed", "repo", repo, "mount", source, "error", err)
		}
		return &mountError{mount: source, repo: repo, err: err}
	}
	if result == nil {
		l.Error("backup produced no summary", "repo", repo, "mount", source)
		return &mountError{mount: source, repo: repo, err: fmt.Errorf("no summary")}
	}
	l.Info("backup complete",
		"repo", repo, "mount", source,
		"snapshot", result.SnapshotID,
		slog.Duration("duration", time.Duration(result.TotalDuration*float64(time.Second))),
	)
	return nil
}

func summarizeFailures(failures []mountError, mountCount, repoCount int) error {
	if len(failures) == 0 {
		return nil
	}
	parts := make([]string, 0, len(failures))
	for _, f := range failures {
		msg := strings.TrimPrefix(f.err.Error(), "restic backup: ")
		if f.mount != "" {
			parts = append(parts, fmt.Sprintf("%s: %s", f.mount, msg))
		} else {
			parts = append(parts, fmt.Sprintf("%s: %s", f.repo, msg))
		}
	}
	joined := strings.Join(parts, "; ")
	if mountCount > 0 && len(failures) == mountCount*repoCount {
		return fmt.Errorf("all %d mounts failed: %s", mountCount, joined)
	}
	return fmt.Errorf("%d failure(s): %s", len(failures), joined)
}

func (r *Runner) applyRetention(ctx context.Context, ctr *docker.Container, cfg docker.BackupConfig, repos []string, logger *slog.Logger) []string {
	logger.Debug("applying retention", "policy", cfg.Retention, "repos", len(repos))
	policy := cfg.Retention

	var issues []string
	for _, repo := range repos {
		l := logger
		if ctr.ComposeService != "" {
			l = logger.With("service", ctr.ComposeService)
		}
		l = l.With("repo", repo)

		start := time.Now()
		if err := r.restic.Forget(ctx, repo, policy, ctr.Name); err != nil {
			l.Warn("forget failed", "error", err)
			issues = append(issues, fmt.Sprintf("forget on %s: %s", repo, err.Error()))
		}
		if err := r.restic.Prune(ctx, repo); err != nil {
			l.Warn("prune failed", "error", err)
			issues = append(issues, fmt.Sprintf("prune on %s: %s", repo, err.Error()))
		}
		l.Info("retention complete", slog.Duration("duration", time.Since(start)))
	}
	return issues
}

func (r *Runner) waitForDeps(ctx context.Context, deps map[string][]depInfo, ctrs []*docker.Container, serviceName string, logger *slog.Logger) error {
	svcMap := serviceContainers(ctrs)

	for _, dep := range depConditionsFrom(deps, serviceName) {
		depCtrs := svcMap[dep.Name]
		if len(depCtrs) == 0 {
			continue
		}
		for _, ctr := range depCtrs {
			l := logger.With("dependency", dep.Name, "condition", dep.Condition)
			l.Debug("waiting for dependency")
			if err := r.waitForCondition(ctx, ctr, dep.Condition); err != nil {
				return err
			}
			if dep.Condition == ServiceHealthy {
				l.Info("container healthy")
			} else {
				l.Debug("dependency satisfied")
			}
		}
	}
	return nil
}

func (r *Runner) waitForEvent(
	ctx context.Context,
	ctr *docker.Container,
	eventTypes []string,
	check func(*docker.Container) (bool, error),
) error {
	fresh, err := r.docker.InspectContainer(ctx, ctr.ID)
	if err != nil {
		return err
	}
	if done, err := check(fresh); done || err != nil {
		return err
	}

	eventCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	msgs, errs := r.docker.WatchContainer(eventCtx, ctr.ID, eventTypes...)

	for {
		select {
		case _, ok := <-msgs:
			if !ok {
				return fmt.Errorf("event stream closed for %s", ctr.Name)
			}
		case err, ok := <-errs:
			if !ok {
				return fmt.Errorf("event error stream closed for %s", ctr.Name)
			}
			if err != nil {
				return fmt.Errorf("event stream error for %s: %w", ctr.Name, err)
			}
		case <-ctx.Done():
			return ctx.Err()
		}

		fresh, err := r.docker.InspectContainer(ctx, ctr.ID)
		if err != nil {
			return err
		}
		if done, err := check(fresh); done || err != nil {
			return err
		}
	}
}

func eventsForCondition(c DepCondition) []string {
	switch c {
	case ServiceStarted, ServiceRunningOrHealthy:
		return []string{"start", "die"}
	case ServiceHealthy:
		return []string{"health_status", "die"}
	case ServiceCompletedSuccessfully:
		return []string{"die"}
	default:
		return nil
	}
}

func (r *Runner) waitForCondition(ctx context.Context, ctr *docker.Container, condition DepCondition) error {
	ctx, cancel := context.WithTimeout(ctx, r.healthWaitTimeout)
	defer cancel()

	eventTypes := eventsForCondition(condition)
	if eventTypes == nil {
		return fmt.Errorf("unknown dependency condition: %s", condition)
	}

	return r.waitForEvent(ctx, ctr, eventTypes, func(c *docker.Container) (bool, error) {
		switch condition {
		case ServiceHealthy:
			if c.Health == nil {
				return false, fmt.Errorf("%s has no healthcheck configured", ctr.Name)
			}
			if c.Health.Status == "unhealthy" {
				return false, fmt.Errorf("%s is unhealthy", ctr.Name)
			}
			return c.Health.Status == "healthy", nil
		case ServiceStarted, ServiceRunningOrHealthy:
			return c.State == "running", nil
		case ServiceCompletedSuccessfully:
			if c.State == "exited" {
				if c.ExitCode != 0 {
					return false, fmt.Errorf("%s exited with code %d", ctr.Name, c.ExitCode)
				}
				return true, nil
			}
			return false, nil
		default:
			return false, fmt.Errorf("unknown dependency condition: %s", condition)
		}
	})
}

func (r *Runner) waitRunning(ctx context.Context, ctr *docker.Container) {
	ctx, cancel := context.WithTimeout(ctx, r.healthWaitTimeout)
	defer cancel()

	_ = r.waitForEvent(ctx, ctr,
		[]string{"start", "die"},
		func(c *docker.Container) (bool, error) {
			return c.State == "running" || c.State == "exited", nil
		})
}

func (r *Runner) ignore(id string)  { r.ignoredIDs.Store(id, true) }
func (r *Runner) release(id string) { r.ignoredIDs.Delete(id) }

func deduplicateByService(ctrs []*docker.Container) []*docker.Container {
	seen := make(map[string]bool)
	out := make([]*docker.Container, 0, len(ctrs))
	for _, c := range ctrs {
		svc := c.ComposeService
		if svc == "" {
			out = append(out, c)
			continue
		}
		if seen[svc] {
			continue
		}
		seen[svc] = true
		out = append(out, c)
	}
	return out
}

func mapKeys(m map[string]bool) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

func (r *Runner) CheckKnownRepos(ctx context.Context) {
	l := r.logger.With("component", "check")

	entries, err := r.repoReg.ListRepos(registry.ExcludeOrphaned())
	if err != nil {
		l.Error("check: failed to list repos from registry", "error", err)
		return
	}
	var failures []string
	if len(entries) > 0 {
		failures = r.checkRepoEntries(ctx, entries, l)
	} else {
		containers, err := r.docker.ListBackupContainers(ctx)
		if err != nil {
			l.Error("check: failed to list containers", "error", err)
			return
		}

		seen := make(map[string]bool)
		for _, ctr := range containers {
			cfg := r.parseConfig(ctr.Labels)
			repos, err := r.repoReg.SyncContainer(&ctr, cfg)
			if err != nil {
				l.Warn("check: failed to resolve repos", "container", ctr.Name, "error", err)
				continue
			}
			for _, repo := range repos {
				if seen[repo] {
					continue
				}
				seen[repo] = true
				if err := r.restic.Check(ctx, repo); err != nil {
					l.Error("check: repository check failed", "repo", repo, "error", err)
					_ = r.repoReg.MarkCheckComplete(repo, false)
					failures = append(failures, fmt.Sprintf("%s: %s", repo, err.Error()))
				} else {
					l.Info("check: repository ok", "repo", repo)
					_ = r.repoReg.MarkCheckComplete(repo, true)
				}
			}
		}
	}

	if len(failures) > 0 {
		msg := strings.Join(failures, "\n")
		r.notifier.SendBackupError("repository-check", msg)
	}
}

func (r *Runner) checkRepoEntries(ctx context.Context, entries []registry.RepoEntry, logger *slog.Logger) []string {
	var failed []string
	seen := make(map[string]bool)
	for _, entry := range entries {
		if seen[entry.URL] {
			continue
		}
		seen[entry.URL] = true
		if err := r.restic.Check(ctx, entry.URL); err != nil {
			logger.Error("check: repository check failed", "repo", entry.URL, "error", err)
			_ = r.repoReg.MarkCheckComplete(entry.URL, false)
			failed = append(failed, fmt.Sprintf("%s: %s", entry.URL, err.Error()))
		} else {
			logger.Info("check: repository ok", "repo", entry.URL)
			_ = r.repoReg.MarkCheckComplete(entry.URL, true)
		}
	}
	return failed
}
