package backup

import (
	"context"
	"fmt"
	"log/slog"
	"maps"
	"slices"
	"strings"
	"sync"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"

	"github.com/depado/buoy/internal/types"
)

func (r *Runner) RunStackBatch(ctx context.Context, project string, batch []*types.Container) {
	start := time.Now()
	var backupErrors map[string]error
	var allIssues []string
	ctx, span := r.tracer.Start(ctx, "buoy.stack.backup",
		trace.WithAttributes(
			attribute.String("project", project),
			attribute.Int("services", len(batch)),
		),
	)
	defer func() {
		r.meters.StackDuration.Record(context.WithoutCancel(ctx), time.Since(start).Seconds(),
			metric.WithAttributes(
				attribute.String("project", project),
				attribute.Int("services", len(batch)),
				attribute.Bool("success", len(backupErrors) == 0),
			),
		)
		span.End()
	}()

	l := r.logger.With("project", project)

	all, byID := r.discoverStackContainers(ctx, project, l)

	fresh := make([]*types.Container, 0, len(batch))
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

	deps := serviceDeps(all)
	stopSvc := stopSet(fresh, deps)
	l.Info("stack backup started", "services", len(fresh), "stop_set", slices.Collect(maps.Keys(stopSvc)))
	services := serviceContainers(all)

	l.Debug("stopping stack services", "count", len(stopSvc))
	wasStopped, stopFailed, ignored := r.stopStackServices(ctx, services, stopSvc, deps, l)
	defer r.releaseBatch(ignored)

	l.Debug("backing up stack services", "count", len(fresh))
	backupErrors, allIssues = r.backupStackServices(ctx, fresh, containerCfg, containerRepos, stopFailed, l)

	l.Debug("starting stack services")
	r.startStackServices(ctx, services, deps, all, wasStopped, l)

	l.Debug("running post-backup hooks and retention")
	r.runPostHooksAndRetention(ctx, fresh, containerCfg, containerRepos, backupErrors, &allIssues, l)

	ok := len(fresh) - len(backupErrors)
	if len(backupErrors) > 0 {
		l.Warn("stack backup finished with failures", "total", len(fresh), "ok", ok, "failed", len(backupErrors))
		span.RecordError(fmt.Errorf("stack backup: %d/%d services failed", len(backupErrors), len(fresh)))
		span.SetStatus(codes.Error, fmt.Sprintf("%d/%d services failed", len(backupErrors), len(fresh)))
	} else {
		l.Info("stack backup finished", "total", len(fresh), "ok", ok)
	}
	r.finalizeStackBackup(project, len(fresh), allIssues)
}

func (r *Runner) discoverStackContainers(ctx context.Context, project string, l *slog.Logger) ([]*types.Container, map[string]*types.Container) {
	ctx, span := r.tracer.Start(ctx, "buoy.stack.discover")
	defer span.End()

	summaries, err := r.docker.ListContainersByProject(ctx, project)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		l.Warn("failed to list stack containers", "error", err)
		return nil, nil
	}
	l.Debug("listed stack containers", "count", len(summaries))
	all, byID := r.inspectStackContainers(ctx, summaries, l)
	span.SetAttributes(attribute.Int("containers", len(all)))
	return all, byID
}

func (r *Runner) inspectStackContainers(ctx context.Context, summaries []types.Container, l *slog.Logger) ([]*types.Container, map[string]*types.Container) {
	all := make([]*types.Container, 0, len(summaries))
	byID := make(map[string]*types.Container, len(summaries))
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

func (r *Runner) resolveStackConfigs(fresh []*types.Container, l *slog.Logger) (map[string]types.BackupConfig, map[string][]types.RepoRef) {
	cfgs := make(map[string]types.BackupConfig, len(fresh))
	repos := make(map[string][]types.RepoRef, len(fresh))
	for _, ctr := range fresh {
		cfg := r.parseConfig(ctr.Labels)
		warnUnusedMountFilters(l.With("service", ctr.ComposeService), ctr, cfg)
		resolved, err := r.repoReg.SyncContainer(ctr, cfg)
		if err != nil {
			l.Warn("failed to sync container repos", "service", ctr.ComposeService, "error", err)
		}
		cfgs[ctr.ID] = cfg
		repos[ctr.ID] = resolved
	}
	return cfgs, repos
}

func (r *Runner) runParallelPreHooks(ctx context.Context, fresh []*types.Container, cfgs map[string]types.BackupConfig, l *slog.Logger) {
	var wg sync.WaitGroup
	for _, ctr := range fresh {
		wg.Add(1)
		go func(c *types.Container) {
			defer wg.Done()
			r.runPreHooks(ctx, c, cfgs[c.ID], l.With("service", c.ComposeService))
		}(ctr)
	}
	wg.Wait()
}

func (r *Runner) stopStackServices(
	ctx context.Context,
	services map[string][]*types.Container,
	stopSvc map[string]bool,
	deps map[string][]depInfo,
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
			sl := l.With("service", ctr.ComposeService)
			r.ignore(ctr.ID)
			ignored[ctr.ID] = true
			sl.Debug("stopping container")
			cfg := r.parseConfig(ctr.Labels)

			_, stopErr := r.stopContainer(ctx, ctr, cfg, sl)
			if stopErr != nil {
				sl.Warn("failed to stop container", "error", stopErr)
				stopFailed[ctr.ID] = true
				r.release(ctr.ID)
				delete(ignored, ctr.ID)
				continue
			}
			wasStopped[ctr.ID] = true
		}
	}
	return
}

func (r *Runner) backupStackServices(
	ctx context.Context,
	fresh []*types.Container,
	cfgs map[string]types.BackupConfig,
	repos map[string][]types.RepoRef,
	stopFailed map[string]bool,
	l *slog.Logger,
) (map[string]error, []string) {
	backupErrors := make(map[string]error)
	var allIssues []string
	for _, ctr := range fresh {
		cfg := cfgs[ctr.ID]
		eligibleMounts := countEligibleMounts(ctr, cfg)
		if stopFailed[ctr.ID] {
			l.With("service", ctr.ComposeService).Warn("skipping backup, stop failed")
			backupErrors[ctr.ComposeService] = fmt.Errorf("stop failed")
			allIssues = append(allIssues, fmt.Sprintf("%s: stop failed", ctr.ComposeService))
			r.meters.BackupsTotal.Add(ctx, 1,
				metric.WithAttributes(containerAttrs(ctr,
					attribute.Int("mounts", eligibleMounts),
					attribute.Bool("success", false),
				)...),
			)
			continue
		}
		ok := true
		if err := r.backupMounts(ctx, ctr, cfg, repos[ctr.ID], l.With("service", ctr.ComposeService)); err != nil {
			l.Error("backup failed for service", "service", ctr.ComposeService, "error", err)
			backupErrors[ctr.ComposeService] = err
			allIssues = append(allIssues, fmt.Sprintf("%s: %s", ctr.ComposeService, err.Error()))
			ok = false
		}
		r.meters.BackupsTotal.Add(ctx, 1,
			metric.WithAttributes(containerAttrs(ctr,
				attribute.Int("mounts", eligibleMounts),
				attribute.Bool("success", ok),
			)...),
		)
	}
	return backupErrors, allIssues
}

func (r *Runner) startStackServices(
	ctx context.Context,
	services map[string][]*types.Container,
	deps map[string][]depInfo,
	all []*types.Container,
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
			cl := l.With("service", ctr.ComposeService)
			cfg := r.parseConfig(ctr.Labels)
			r.startContainer(ctx, ctr, cfg, cl)
		}
	}
}

func (r *Runner) runPostHooksAndRetention(
	ctx context.Context,
	fresh []*types.Container,
	cfgs map[string]types.BackupConfig,
	repos map[string][]types.RepoRef,
	backupErrors map[string]error,
	allIssues *[]string,
	l *slog.Logger,
) {
	for _, ctr := range fresh {
		cfg := cfgs[ctr.ID]
		r.runPostHooks(ctx, ctr, cfg, l.With("service", ctr.ComposeService))
		if _, failed := backupErrors[ctr.ComposeService]; !failed {
			for _, ri := range r.applyRetention(ctx, ctr, cfg, repos[ctr.ID], l.With("service", ctr.ComposeService)) {
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

func (r *Runner) finalizeStackBackup(project string, total int, allIssues []string) {
	if len(allIssues) == 0 {
		r.notifier.SendInfo(
			fmt.Sprintf("buoy stack backup complete: %s", project),
			fmt.Sprintf("Backup completed for project %s (%d services)", project, total),
		)
		return
	}
	r.notifier.SendBackupError(project, strings.Join(allIssues, "\n"))
}

func (r *Runner) waitForDeps(ctx context.Context, deps map[string][]depInfo, ctrs []*types.Container, serviceName string, logger *slog.Logger) error {
	svcMap := serviceContainers(ctrs)

	for _, dep := range depConditionsFrom(deps, serviceName) {
		depCtrs := svcMap[dep.Name]
		if len(depCtrs) == 0 {
			continue
		}
		for _, ctr := range depCtrs {
			l := logger.With("service", dep.Name)
			l.Debug("waiting for dependency", "condition", dep.Condition)
			cfg := r.parseConfig(ctr.Labels)
			if err := r.waitForCondition(ctx, ctr, dep.Condition, r.effectiveHealthWaitTimeout(cfg)); err != nil {
				return err
			}
			if dep.Condition == serviceHealthy {
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
	ctr *types.Container,
	eventTypes []string,
	check func(*types.Container) (bool, error),
) (waitErr error) {
	ctx, span := r.tracer.Start(ctx, "buoy.container.wait",
		trace.WithAttributes(containerAttrs(ctr, attribute.StringSlice("event_types", eventTypes))...),
	)
	defer func() {
		if waitErr != nil {
			span.RecordError(waitErr)
			span.SetStatus(codes.Error, waitErr.Error())
		}
		span.End()
	}()

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

func eventsForCondition(c depCondition) []string {
	switch c {
	case serviceStarted, serviceRunningOrHealthy:
		return []string{"start", "die"}
	case serviceHealthy:
		return []string{"health_status", "die"}
	case serviceCompletedSuccessfully:
		return []string{"die"}
	}
	return nil
}

func (r *Runner) waitForCondition(ctx context.Context, ctr *types.Container, condition depCondition, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	eventTypes := eventsForCondition(condition)
	if eventTypes == nil {
		return fmt.Errorf("unknown dependency condition: %s", condition)
	}

	return r.waitForEvent(ctx, ctr, eventTypes, func(c *types.Container) (bool, error) {
		switch condition {
		case serviceHealthy:
			if c.Health == nil {
				return false, fmt.Errorf("%s has no healthcheck configured", ctr.Name)
			}
			if c.Health.Status == "unhealthy" {
				return false, fmt.Errorf("%s is unhealthy", ctr.Name)
			}
			return c.Health.Status == "healthy", nil
		case serviceStarted, serviceRunningOrHealthy:
			return c.State == "running", nil
		case serviceCompletedSuccessfully:
			if c.State == "exited" {
				if c.ExitCode != 0 {
					return false, fmt.Errorf("%s exited with code %d", ctr.Name, c.ExitCode)
				}
				return true, nil
			}
			return false, nil
		}
		return false, fmt.Errorf("unknown dependency condition: %s", condition)
	})
}

func deduplicateByService(ctrs []*types.Container) []*types.Container {
	seen := make(map[string]bool)
	out := make([]*types.Container, 0, len(ctrs))
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
