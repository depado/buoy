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

func (r *Runner) PruneKnownRepos(ctx context.Context) {
	ctx, span := r.tracer.Start(ctx, "buoy.prune")
	var pruneErr error
	defer span.End()
	defer func() {
		if pruneErr != nil {
			span.RecordError(pruneErr)
			span.SetStatus(codes.Error, pruneErr.Error())
		}
	}()

	l := r.logger.With("component", "prune")

	entries, err := r.repoReg.ListRepos(registry.ExcludeOrphaned())
	if err != nil {
		l.Error("prune: failed to list repos from registry", "error", err)
		pruneErr = err
		return
	}
	var failures []string
	if len(entries) > 0 {
		failures = r.pruneRepoEntries(ctx, entries, l)
	} else {
		containers, err := r.docker.ListBackupContainers(ctx)
		if err != nil {
			l.Error("prune: failed to list containers", "error", err)
			pruneErr = err
			return
		}

		seen := make(map[string]bool)
		for _, ctr := range containers {
			cfg := r.parseConfig(ctr.Labels)
			repos, err := r.repoReg.SyncContainer(&ctr, cfg)
			if err != nil {
				l.Warn("prune: failed to resolve repos", "container", ctr.Name, "error", err)
				continue
			}
			for _, ref := range repos {
				if seen[ref.URL] {
					continue
				}
				seen[ref.URL] = true
				ctx := restic.WithPassword(ctx, r.effectivePassword(cfg, ref.Name))
				if err := r.recordPrune(ctx, ref.URL, l); err != nil {
					failures = append(failures, fmt.Sprintf("%s: %s", ref.URL, err.Error()))
				}
			}
		}
	}

	if len(failures) > 0 {
		msg := strings.Join(failures, "\n")
		r.notifier.SendBackupError("repository-prune", msg)
	}
}

func (r *Runner) pruneRepoEntries(ctx context.Context, entries []types.RepoEntry, logger *slog.Logger) []string {
	var failed []string
	seen := make(map[string]bool)
	for _, entry := range entries {
		if seen[entry.URL] {
			continue
		}
		seen[entry.URL] = true
		ctx := restic.WithPassword(ctx, r.resticConf.PasswordFor(entry.RepoName))
		if err := r.recordPrune(ctx, entry.URL, logger); err != nil {
			failed = append(failed, fmt.Sprintf("%s: %s", entry.URL, err.Error()))
		}
	}
	return failed
}

// recordPrune runs restic prune for one repo and records its duration gauge
// with repo and success labels.
func (r *Runner) recordPrune(ctx context.Context, repo string, logger *slog.Logger) error {
	start := time.Now()
	err := r.restic.Prune(ctx, repo)
	ok := err == nil
	r.meters.PruneDuration.Record(context.WithoutCancel(ctx), time.Since(start).Seconds(),
		metric.WithAttributes(
			attribute.String("repo", repo),
			attribute.Bool("success", ok),
		),
	)
	if err != nil {
		logger.Error("prune: repository prune failed", "repo", repo, "error", err)
		return err
	}
	logger.Info("prune: repository pruned", "repo", repo)
	return nil
}
