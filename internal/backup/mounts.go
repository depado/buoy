package backup

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"

	"github.com/depado/buoy/internal/restic"
	"github.com/depado/buoy/internal/types"
)

func (r *Runner) backupMounts(ctx context.Context, ctr *types.Container, cfg types.BackupConfig, repos []types.RepoRef, logger *slog.Logger) error {
	mountCount := 0
	var failures []mountError

	for _, ref := range repos {
		// Give each repo its own budget so one slow repo can't starve the
		// others. The parent deadline still caps the whole cycle.
		repoCtx := ctx
		cancel := func() {}
		if t := r.effectiveRepoTimeout(cfg, ref.Name); t > 0 {
			repoCtx, cancel = context.WithTimeout(ctx, t)
		}
		repoCtx = restic.WithPassword(repoCtx, r.effectivePassword(cfg, ref.Name))

		repoStart := time.Now()
		if !r.ensureRepo(repoCtx, ref.URL, logger, &failures) {
			r.meters.BackupDuration.Record(context.WithoutCancel(repoCtx), time.Since(repoStart).Seconds(),
				metric.WithAttributes(containerAttrs(ctr,
					attribute.String("repo", ref.URL),
					attribute.Bool("success", false),
				)...),
			)
			cancel()
			continue
		}

		l := logger.With("repo", ref.URL)
		repoOK := true
		repoMounts := 0

		for _, m := range ctr.Mounts {
			if m.Type == "tmpfs" {
				continue
			}

			matchedName, ok := types.MountMatches(m, cfg.Include, cfg.Exclude)
			if !ok {
				continue
			}

			mountCount++
			repoMounts++
			if repoErr := r.backupSingleMount(repoCtx, ref.URL, ctr, m, matchedName, cfg, l); repoErr != nil {
				failures = append(failures, *repoErr)
				repoOK = false
			}
		}
		r.meters.BackupDuration.Record(context.WithoutCancel(repoCtx), time.Since(repoStart).Seconds(),
			metric.WithAttributes(containerAttrs(ctr,
				attribute.String("repo", ref.URL),
				attribute.Bool("success", repoOK),
			)...),
		)
		if err := r.repoReg.MarkBackupComplete(ref.URL, repoOK); err != nil {
			logger.Warn("failed to persist backup status", "repo", ref.URL, "error", err)
		}
		cancel()
	}

	if mountCount == 0 {
		logger.Warn("container has no backup-eligible mounts", "mounts", len(ctr.Mounts), "include", len(cfg.Include), "exclude", len(cfg.Exclude))
	}

	return summarizeFailures(failures, mountCount, len(repos))
}

func (r *Runner) ensureRepo(ctx context.Context, repo string, logger *slog.Logger, failures *[]mountError) bool {
	l := logger.With("repo", repo)

	l.Debug("checking repo")
	exists, err := r.restic.RepoExists(ctx, repo)
	if err != nil {
		l.Warn("failed to check repo, skipping repo", "error", err)
		*failures = append(*failures, mountError{repo: repo, err: fmt.Errorf("repo check: %w", err)})
		if err := r.repoReg.MarkBackupComplete(repo, false); err != nil {
			l.Warn("failed to persist backup status", "error", err)
		}
		return false
	}
	if !exists {
		l.Debug("repo not found, initializing")
		if err := r.restic.Init(ctx, repo); err != nil {
			l.Warn("failed to init repo, skipping repo", "error", err)
			*failures = append(*failures, mountError{repo: repo, err: fmt.Errorf("repo init: %w", err)})
			if err := r.repoReg.MarkBackupComplete(repo, false); err != nil {
				l.Warn("failed to persist backup status", "error", err)
			}
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
	ctr *types.Container,
	m types.Mount,
	matchedName string,
	cfg types.BackupConfig,
	l *slog.Logger,
) (mountErr *mountError) {
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
		Hostname: ctr.Name,
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

	start := time.Now()

	ctx, span := r.tracer.Start(ctx, "buoy.restic.backup",
		trace.WithAttributes(
			attribute.String("repo", repo),
			attribute.String("mount.source", source),
			attribute.String("mount.type", m.Type),
		),
	)
	defer span.End()

	result, err := r.restic.Backup(ctx, repo, paths, opts)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
	} else if result != nil {
		span.SetAttributes(attribute.String("snapshot.id", result.SnapshotID))
	}

	r.meters.BackupDuration.Record(ctx, time.Since(start).Seconds(),
		metric.WithAttributes(containerAttrs(ctr,
			attribute.String("repo", repo),
			attribute.String("mount", source),
			attribute.Bool("success", err == nil),
		)...),
	)

	if err != nil {
		if result != nil {
			l.Error("backup completed with errors",
				"mount", source,
				"snapshot", result.SnapshotID, "error", err,
			)
		} else {
			l.Error("backup failed", "mount", source, "error", err)
		}
		return &mountError{mount: source, repo: repo, err: err}
	}
	if result == nil {
		l.Error("backup produced no summary", "mount", source)
		return &mountError{mount: source, repo: repo, err: fmt.Errorf("no summary")}
	}
	l.Info("backup completed",
		"mount", source,
		"snapshot", result.SnapshotID,
		slog.Duration("duration", time.Duration(result.TotalDuration*float64(time.Second))),
	)
	return nil
}
