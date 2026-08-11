package backup

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric"

	"github.com/depado/buoy/internal/registry"
	"github.com/depado/buoy/internal/restic"
	"github.com/depado/buoy/internal/types"
)

func (r *Runner) CheckKnownRepos(ctx context.Context) {
	ctx, span := r.tracer.Start(ctx, "buoy.check")
	var checkErr error
	defer span.End()
	defer func() {
		if checkErr != nil {
			span.RecordError(checkErr)
			span.SetStatus(codes.Error, checkErr.Error())
		}
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
				if err := r.recordCheck(ctx, ref.URL, func(ctx context.Context) error {
					return r.restic.Check(ctx, ref.URL)
				}, l); err != nil {
					failures = append(failures, fmt.Sprintf("%s: %s", ref.URL, err.Error()))
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
		ctx := restic.WithPassword(ctx, r.resticConf.PasswordFor(entry.RepoName))
		if err := r.recordCheck(ctx, entry.URL, func(ctx context.Context) error {
			return r.restic.Check(ctx, entry.URL)
		}, logger); err != nil {
			failed = append(failed, fmt.Sprintf("%s: %s", entry.URL, err.Error()))
		}
	}
	return failed
}

// recordCheck runs a restic check for one repo, records its duration gauge
// with repo and success labels, persists the result, and returns any error.
func (r *Runner) recordCheck(ctx context.Context, repo string, check func(context.Context) error, logger *slog.Logger) error {
	start := time.Now()
	err := check(ctx)
	ok := err == nil
	r.meters.CheckDuration.Record(context.WithoutCancel(ctx), time.Since(start).Seconds(),
		metric.WithAttributes(
			attribute.String("repo", repo),
			attribute.Bool("success", ok),
		),
	)
	if err != nil {
		logger.Error("check: repository check failed", "repo", repo, "error", err)
		if perr := r.repoReg.MarkCheckComplete(repo, false); perr != nil {
			logger.Warn("failed to persist check status", "repo", repo, "error", perr)
		}
		return err
	}
	logger.Info("check: repository ok", "repo", repo)
	if perr := r.repoReg.MarkCheckComplete(repo, true); perr != nil {
		logger.Warn("failed to persist check status", "repo", repo, "error", perr)
	}
	return nil
}
