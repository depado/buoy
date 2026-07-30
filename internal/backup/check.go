package backup

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"go.opentelemetry.io/otel/codes"

	"github.com/depado/buoy/internal/registry"
	"github.com/depado/buoy/internal/restic"
	"github.com/depado/buoy/internal/types"
)

func (r *Runner) CheckKnownRepos(ctx context.Context) {
	start := time.Now()
	ctx, span := r.tracers.Tracer.Start(ctx, "buoy.check")
	var checkErr error
	defer span.End()
	defer func() {
		if checkErr != nil {
			span.RecordError(checkErr)
			span.SetStatus(codes.Error, checkErr.Error())
		}
		r.meters.CheckDuration.Record(ctx, time.Since(start).Seconds())
	}()

	l := r.logger.With("component", "check")

	entries, err := r.repoReg.ListRepos(registry.ExcludeOrphaned())
	if err != nil {
		l.Error("check: failed to list repos from registry", "error", err)
		checkErr = err
		return
	}
	var failures []string
	if len(entries) > 0 {
		failures = r.checkRepoEntries(ctx, entries, l)
	} else {
		containers, err := r.docker.ListBackupContainers(ctx)
		if err != nil {
			l.Error("check: failed to list containers", "error", err)
			checkErr = err
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
			for _, ref := range repos {
				if seen[ref.URL] {
					continue
				}
				seen[ref.URL] = true
				ctx := restic.WithPassword(ctx, r.effectivePassword(cfg, ref.Name))
				if err := r.restic.Check(ctx, ref.URL); err != nil {
					l.Error("check: repository check failed", "repo", ref.URL, "error", err)
					if err := r.repoReg.MarkCheckComplete(ref.URL, false); err != nil {
						l.Warn("failed to persist check status", "repo", ref.URL, "error", err)
					}
					failures = append(failures, fmt.Sprintf("%s: %s", ref.URL, err.Error()))
				} else {
					l.Info("check: repository ok", "repo", ref.URL)
					if err := r.repoReg.MarkCheckComplete(ref.URL, true); err != nil {
						l.Warn("failed to persist check status", "repo", ref.URL, "error", err)
					}
				}
			}
		}
	}

	if len(failures) > 0 {
		msg := strings.Join(failures, "\n")
		r.notifier.SendBackupError("repository-check", msg)
	}
}

func (r *Runner) checkRepoEntries(ctx context.Context, entries []types.RepoEntry, logger *slog.Logger) []string {
	var failed []string
	seen := make(map[string]bool)
	for _, entry := range entries {
		if seen[entry.URL] {
			continue
		}
		seen[entry.URL] = true
		ctx := restic.WithPassword(ctx, r.resticConf.PasswordForEntry(entry.RepoName, entry.URL))
		if err := r.restic.Check(ctx, entry.URL); err != nil {
			logger.Error("check: repository check failed", "repo", entry.URL, "error", err)
			if err := r.repoReg.MarkCheckComplete(entry.URL, false); err != nil {
				logger.Warn("failed to persist check status", "repo", entry.URL, "error", err)
			}
			failed = append(failed, fmt.Sprintf("%s: %s", entry.URL, err.Error()))
		} else {
			logger.Info("check: repository ok", "repo", entry.URL)
			if err := r.repoReg.MarkCheckComplete(entry.URL, true); err != nil {
				logger.Warn("failed to persist check status", "repo", entry.URL, "error", err)
			}
		}
	}
	return failed
}
