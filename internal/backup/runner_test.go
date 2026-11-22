package backup

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/depado/buoy/internal/config"
	"github.com/depado/buoy/internal/types"
)

func TestDeduplicateByService(t *testing.T) {
	tests := []struct {
		name     string
		input    []*types.Container
		wantCnt  int
		wantSvcs []string // expected service names in order
	}{
		{
			name:    "empty",
			input:   nil,
			wantCnt: 0,
		},
		{
			name: "single",
			input: []*types.Container{
				{ComposeService: "web", Name: "web1"},
			},
			wantCnt:  1,
			wantSvcs: []string{"web"},
		},
		{
			name: "duplicates by service name -> one kept",
			input: []*types.Container{
				{ComposeService: "web", Name: "web1"},
				{ComposeService: "web", Name: "web2"},
			},
			wantCnt:  1,
			wantSvcs: []string{"web"},
		},
		{
			name: "standalone containers (no service) -> never deduplicated",
			input: []*types.Container{
				{Name: "standalone1"},
				{Name: "standalone2"},
			},
			wantCnt:  2,
			wantSvcs: []string{"", ""},
		},
		{
			name: "mix of duped services and standalones",
			input: []*types.Container{
				{ComposeService: "web", Name: "web1"},
				{Name: "standalone1"},
				{ComposeService: "web", Name: "web2"},
				{Name: "standalone2"},
				{ComposeService: "db", Name: "db1"},
			},
			wantCnt:  4,
			wantSvcs: []string{"web", "", "", "db"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := deduplicateByService(tt.input)
			if len(got) != tt.wantCnt {
				t.Errorf("count: got %d, want %d", len(got), tt.wantCnt)
			}
			if len(got) != len(tt.wantSvcs) {
				t.Fatalf("wantSvcs length mismatch: got %d, want %d", len(got), len(tt.wantSvcs))
			}
			for i, c := range got {
				if c.ComposeService != tt.wantSvcs[i] {
					t.Errorf("index %d: got service %q, want %q", i, c.ComposeService, tt.wantSvcs[i])
				}
			}
		})
	}
}

func TestEffectivePassword(t *testing.T) {
	rc := &config.ResticConf{
		Password: "global",
		Repos: map[string]config.RepoConfig{
			"local": {URL: "/backup"},
			"s3":    {URL: "s3:https://bucket", Password: "s3-pass"},
		},
	}
	r := &Runner{resticConf: rc}

	tests := []struct {
		name     string
		cfg      types.BackupConfig
		repoName string
		want     string
	}{
		{"container label wins", types.BackupConfig{Password: "ctr-pass"}, "s3", "ctr-pass"},
		{"per-repo password", types.BackupConfig{}, "s3", "s3-pass"},
		{"global fallback", types.BackupConfig{}, "local", "global"},
		{"unknown repo uses global", types.BackupConfig{}, "unknown", "global"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := r.effectivePassword(tt.cfg, tt.repoName)
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestEffectiveRepoTimeout(t *testing.T) {
	rc := &config.ResticConf{
		Repos: map[string]config.RepoConfig{
			"b2":    {URL: "b2:bucket", Timeout: "45m"},
			"local": {URL: "/backup"},
		},
	}
	r := &Runner{resticConf: rc, repoTimeout: 30 * time.Minute}

	tests := []struct {
		name     string
		cfg      types.BackupConfig
		repoName string
		want     time.Duration
	}{
		{"per-repo label wins", types.BackupConfig{RepoTimeouts: map[string]time.Duration{"b2": 2 * time.Hour}, RepoTimeout: 1 * time.Hour}, "b2", 2 * time.Hour},
		{"container label beats repo config", types.BackupConfig{RepoTimeout: 1 * time.Hour}, "b2", 1 * time.Hour},
		{"repo config beats global", types.BackupConfig{}, "b2", 45 * time.Minute},
		{"unknown repo falls back to global", types.BackupConfig{}, "sftp", 30 * time.Minute},
		{"per-repo label on other repo ignored", types.BackupConfig{RepoTimeouts: map[string]time.Duration{"b2": 2 * time.Hour}}, "local", 30 * time.Minute},
		{"zero label values fall through", types.BackupConfig{RepoTimeouts: map[string]time.Duration{"b2": 0}}, "b2", 45 * time.Minute},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := r.effectiveRepoTimeout(tt.cfg, tt.repoName)
			if got != tt.want {
				t.Errorf("got %v, want %v", got, tt.want)
			}
		})
	}
}

func TestMaintenanceRepoTimeout(t *testing.T) {
	rc := &config.ResticConf{
		Repos: map[string]config.RepoConfig{
			"b2":    {URL: "b2:bucket", Timeout: "45m"},
			"local": {URL: "/backup"},
		},
	}
	r := &Runner{resticConf: rc, repoTimeout: 30 * time.Minute}

	tests := []struct {
		name     string
		repoName string
		want     time.Duration
	}{
		{"repo config wins", "b2", 45 * time.Minute},
		{"no repo config falls back to global", "local", 30 * time.Minute},
		{"unknown repo falls back to global", "sftp", 30 * time.Minute},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := r.maintenanceRepoTimeout(tt.repoName); got != tt.want {
				t.Errorf("got %v, want %v", got, tt.want)
			}
		})
	}
}

func TestMaintenanceRepoCtx(t *testing.T) {
	r := &Runner{
		resticConf: &config.ResticConf{
			Repos: map[string]config.RepoConfig{"b2": {URL: "b2:bucket", Timeout: "45m"}},
		},
		repoTimeout: 30 * time.Minute,
	}

	t.Run("repo config bounds the context", func(t *testing.T) {
		ctx, cancel, err := r.maintenanceRepoCtx(context.Background(), "b2")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		defer cancel()
		if d, ok := ctx.Deadline(); !ok || time.Until(d) < 44*time.Minute {
			t.Errorf("expected ~45m deadline, got %v", d)
		}
	})

	t.Run("zero timeout leaves context unbounded", func(t *testing.T) {
		r.repoTimeout = 0
		defer func() { r.repoTimeout = 30 * time.Minute }()
		ctx, cancel, err := r.maintenanceRepoCtx(context.Background(), "local")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		defer cancel()
		if _, ok := ctx.Deadline(); ok {
			t.Error("expected no deadline")
		}
	})

	t.Run("nearly-exhausted window refuses to start", func(t *testing.T) {
		parent, pcancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
		defer pcancel()
		_, _, err := r.maintenanceRepoCtx(parent, "b2")
		if err == nil {
			t.Error("expected budget-exhausted error")
		}
	})
}

func TestApplyRetentionSkipsRemainingReposWhenWindowExhausted(t *testing.T) {
	r := &Runner{
		resticConf:  &config.ResticConf{},
		repoTimeout: 30 * time.Minute,
	}
	parent, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()

	repos := []types.RepoRef{
		{Name: "b2", URL: "b2:bucket"},
		{Name: "local", URL: "/backup"},
	}
	issues := r.applyRetention(parent, &types.Container{Name: "gotify"}, types.BackupConfig{}, repos, slog.New(slog.DiscardHandler))

	if len(issues) != 1 {
		t.Fatalf("expected 1 issue, got %d: %v", len(issues), issues)
	}
	if want := "skipped 1 remaining repo(s)"; !strings.Contains(issues[0], want) {
		t.Errorf("issue %q does not mention %q", issues[0], want)
	}
}

func TestSummarizeOutcome(t *testing.T) {
	tests := []struct {
		name      string
		eligible  int
		attempted int
		snapshots int
		failures  []mountError
		wantIs    error
		want      string
	}{
		{"no eligible mounts", 0, 0, 0, nil, errNoEligibleMounts, ""},
		{"single empty mount", 1, 1, 0, nil, errNoSnapshots, ""},
		{"all mounts empty", 2, 2, 0, nil, errNoSnapshots, ""},
		{"one snapshot is enough", 2, 2, 1, nil, nil, ""},
		{"all attempts failed", 1, 2, 0, []mountError{
			{mount: "/data", repo: "local", err: errors.New("restic backup: boom")},
			{mount: "/data", repo: "b2", err: errors.New("restic backup: boom")},
		}, nil, "all 2 mounts failed: /data: boom; /data: boom"},
		{"partial failure", 2, 4, 2, []mountError{
			{repo: "b2", err: errors.New("restic backup: repo check: unreachable")},
		}, nil, "1 failure(s): b2: repo check: unreachable"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := summarizeOutcome(tt.eligible, tt.attempted, tt.snapshots, tt.failures)
			if tt.wantIs != nil {
				if !errors.Is(got, tt.wantIs) {
					t.Fatalf("got %v, want errors.Is %v", got, tt.wantIs)
				}
				return
			}
			if tt.want == "" {
				if got != nil {
					t.Fatalf("got %v, want nil", got)
				}
				return
			}
			if got == nil {
				t.Fatalf("got nil, want %q", tt.want)
			}
			if got.Error() != tt.want {
				t.Errorf("got %q, want %q", got.Error(), tt.want)
			}
		})
	}
}

func TestPasswordFor(t *testing.T) {
	rc := &config.ResticConf{
		Password: "global",
		Repos: map[string]config.RepoConfig{
			"local": {URL: "/backup", Password: "local-pass"},
			"s3":    {URL: "s3:https://bucket", Password: "s3-pass"},
		},
	}
	r := &Runner{resticConf: rc}

	tests := []struct {
		name     string
		repoName string
		want     string
	}{
		{"by repo name", "s3", "s3-pass"},
		{"global fallback", "", "global"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := r.resticConf.PasswordFor(tt.repoName)
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}
