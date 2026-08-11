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
	for _, ref := range repos {
		ctx := restic.WithPassword(ctx, r.effectivePassword(cfg, ref.Name))

		l := logger.With("repo", ref.URL)

		start := time.Now()
		repoOK := true
		if err := r.restic.Forget(ctx, ref.URL, policy, ctr.Name); err != nil {
			l.Warn("forget failed", "error", err)
			issues = append(issues, fmt.Sprintf("forget on %s: %s", ref.URL, err.Error()))
			repoOK = false
		}
		if err := r.restic.Prune(ctx, ref.URL); err != nil {
			l.Warn("prune failed", "error", err)
			issues = append(issues, fmt.Sprintf("prune on %s: %s", ref.URL, err.Error()))
			repoOK = false
		}
		l.Info("retention applied", slog.Duration("duration", time.Since(start)))
		r.meters.RetentionDuration.Record(context.WithoutCancel(ctx), time.Since(start).Seconds(),
			metric.WithAttributes(containerAttrs(ctr,
				attribute.String("repo", ref.URL),
				attribute.Bool("success", repoOK),
			)...),
		)
	}
	return issues
}
