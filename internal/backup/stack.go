package backup

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"

	"github.com/moby/moby/api/types/container"

	"github.com/depado/buoy/internal/docker"
	"github.com/depado/buoy/internal/registry"
)

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

	l.Debug("running pre-backup hooks", "services", len(fresh))
	r.runParallelPreHooks(ctx, fresh, containerCfg, l)

	stopSvc := stopSet(fresh, all)
	l.Info("stack backup started", "services", len(fresh), "stop_set", mapKeys(stopSvc))
	services := serviceContainers(all)
	deps := serviceDeps(all)

	l.Debug("stopping stack services", "count", len(stopSvc))
	wasStopped, stopFailed, ignored := r.stopStackServices(ctx, services, stopSvc, deps, project, l)
	defer r.releaseBatch(ignored)

	l.Debug("backing up stack services", "count", len(fresh))
	backupErrors, allIssues := r.backupStackServices(ctx, fresh, containerCfg, containerRepos, stopFailed, l)

	l.Debug("starting stack services")
	r.startStackServices(ctx, services, deps, all, wasStopped, l)

	for _, ctr := range all {
		if wasStopped[ctr.ID] {
			r.waitRunning(ctx, ctr, l.With(ctr.LogAttrs()...))
		}
	}

	l.Debug("running post-backup hooks and retention")
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

func (r *Runner) resolveStackConfigs(fresh []*docker.Container, l *slog.Logger) (map[string]docker.BackupConfig, map[string][]registry.RepoRef) {
	cfgs := make(map[string]docker.BackupConfig, len(fresh))
	repos := make(map[string][]registry.RepoRef, len(fresh))
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
		l.Warn("dependency cycle detected", "service", svc)
	})

	ordered := make(map[string]bool, len(stopOrder))
	for _, svc := range stopOrder {
		ordered[svc] = true
	}

	svcs := make([]string, 0, len(stopSvc))
	svcs = append(svcs, stopOrder...)
	for svc := range stopSvc {
		if !ordered[svc] {
			svcs = append(svcs, svc)
		}
	}

	for _, svc := range svcs {
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
	repos map[string][]registry.RepoRef,
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

	ordered := make(map[string]bool, len(startOrder))
	for _, svc := range startOrder {
		ordered[svc] = true
	}

	svcs := make([]string, 0, len(services))
	svcs = append(svcs, startOrder...)
	for svc := range services {
		if !ordered[svc] {
			svcs = append(svcs, svc)
		}
	}

	for _, svc := range svcs {
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
	repos map[string][]registry.RepoRef,
	backupErrors map[string]error,
	allIssues *[]string,
	l *slog.Logger,
) {
	for _, ctr := range fresh {
		cfg := cfgs[ctr.ID]
		r.runPostHooks(ctx, ctr, cfg, l.With(ctr.LogAttrs()...))
		if _, failed := backupErrors[ctr.ComposeService]; !failed {
			for _, ri := range r.applyRetention(ctx, ctr, cfg, repos[ctr.ID], l.With(ctr.LogAttrs()...)) {
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
