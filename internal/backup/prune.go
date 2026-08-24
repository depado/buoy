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
		budgetExhausted := false
		for _, ctr := range containers {
			if budgetExhausted {
				break
			}
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
				repoCtx, cancel, err := r.maintenanceRepoCtx(ctx, ref.Name)
				if err != nil {
					failures = append(failures, fmt.Sprintf("maintenance budget exhausted: %s", err.Error()))
					budgetExhausted = true
					break
				}
				repoCtx = restic.WithPassword(repoCtx, r.effectivePassword(cfg, ref.Name))
				if err := r.recordPrune(repoCtx, ref.URL, l); err != nil {
					failures = append(failures, fmt.Sprintf("%s: %s", ref.URL, err.Error()))
				}
				cancel()
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
	for i, entry := range entries {
		if seen[entry.URL] {
			continue
		}
		seen[entry.URL] = true

		repoCtx, cancel, err := r.maintenanceRepoCtx(ctx, entry.RepoName)
		if err != nil {
			remaining := 0
			for _, e := range entries[i:] {
				if !seen[e.URL] {
					remaining++
				}
			}
			failed = append(failed, fmt.Sprintf("%s: %s", entry.URL, err.Error()))
			failed = append(failed, fmt.Sprintf("maintenance budget exhausted, skipped %d remaining repo(s)", remaining))
			break
		}
		repoCtx = restic.WithPassword(repoCtx, r.resticConf.PasswordFor(entry.RepoName))
		if err := r.recordPrune(repoCtx, entry.URL, logger); err != nil {
			failed = append(failed, fmt.Sprintf("%s: %s", entry.URL, err.Error()))
		}
		cancel()
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
		// A killed/aborted prune leaves a stale exclusive lock behind; clear
		// it so the next backup isn't blocked. Uses WithoutCancel since the
		// failure often comes from the context being expired.
		if uerr := r.restic.Unlock(context.WithoutCancel(ctx), repo); uerr != nil {
			logger.Warn("prune: unlock failed after prune error", "repo", repo, "error", uerr)
		}
		return err
	}
	logger.Info("prune: repository pruned", "repo", repo)
	return nil
}
