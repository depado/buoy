package backup

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	"github.com/depado/buoy/internal/restic"
	"github.com/depado/buoy/internal/types"
)

func (r *Runner) applyRetention(ctx context.Context, ctr *types.Container, cfg types.BackupConfig, repos []types.RepoRef, logger *slog.Logger) []string {
	logger.Debug("applying retention", "policy", cfg.Retention, "repos", len(repos))
	policy := cfg.Retention

	var issues []string
	for i, ref := range repos {
		l := logger.With("repo", ref.URL)

		repoCtx, cancel, err := repoCtx(ctx, r.effectiveRepoTimeout(cfg, ref.Name))
		if err != nil {
			issues = append(issues, fmt.Sprintf("forget on %s: %s, skipped %d remaining repo(s)", ref.URL, err, len(repos)-i-1))
			break
		}
		repoCtx = restic.WithPassword(repoCtx, r.effectivePassword(cfg, ref.Name))

		start := time.Now()
		repoOK := true
		if err := r.restic.Forget(repoCtx, ref.URL, policy, ctr.Name); err != nil {
			l.Warn("forget failed", "error", err)
			issues = append(issues, fmt.Sprintf("forget on %s: %s", ref.URL, err.Error()))
			repoOK = false
		}
		l.Info("retention applied", slog.Duration("duration", time.Since(start)))
		r.meters.RetentionDuration.Record(context.WithoutCancel(repoCtx), time.Since(start).Seconds(),
			metric.WithAttributes(containerAttrs(ctr,
				attribute.String("repo", ref.URL),
				attribute.Bool("success", repoOK),
			)...),
		)
		cancel()
	}
	return issues
}
