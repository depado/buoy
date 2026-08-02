package backup

import (
	"testing"

	"github.com/depado/buoy/internal/config"
	"github.com/depado/buoy/internal/docker"
)

func TestDeduplicateByService(t *testing.T) {
	tests := []struct {
		name     string
		input    []*docker.Container
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
			input: []*docker.Container{
				{ComposeService: "web", Name: "web1"},
			},
			wantCnt:  1,
			wantSvcs: []string{"web"},
		},
		{
			name: "duplicates by service name -> one kept",
			input: []*docker.Container{
				{ComposeService: "web", Name: "web1"},
				{ComposeService: "web", Name: "web2"},
			},
			wantCnt:  1,
			wantSvcs: []string{"web"},
		},
		{
			name: "standalone containers (no service) -> never deduplicated",
			input: []*docker.Container{
				{Name: "standalone1"},
				{Name: "standalone2"},
			},
			wantCnt:  2,
			wantSvcs: []string{"", ""},
		},
		{
			name: "mix of duped services and standalones",
			input: []*docker.Container{
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
		cfg      docker.BackupConfig
		repoName string
		want     string
	}{
		{"container label wins", docker.BackupConfig{Password: "ctr-pass"}, "s3", "ctr-pass"},
		{"per-repo password", docker.BackupConfig{}, "s3", "s3-pass"},
		{"global fallback", docker.BackupConfig{}, "local", "global"},
		{"unknown repo uses global", docker.BackupConfig{}, "unknown", "global"},
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
