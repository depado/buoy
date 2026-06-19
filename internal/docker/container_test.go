package docker

import (
	"testing"
	"time"

	"github.com/depado/buoy/internal/restic"
)

func TestSplitAndTrim(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  []string
	}{
		{"empty", "", nil},
		{"single", "foo", []string{"foo"}},
		{"comma separated", "foo,bar,baz", []string{"foo", "bar", "baz"}},
		{"trailing whitespace", "foo , bar , baz", []string{"foo", "bar", "baz"}},
		{"leading whitespace", "  foo,  bar", []string{"foo", "bar"}},
		{"empty middle element", "foo,,bar", []string{"foo", "bar"}},
		{"empty trailing element", "foo,", []string{"foo"}},
		{"empty leading element", ",foo", []string{"foo"}},
		{"only commas", ",,,", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := splitAndTrim(tt.input)
			if len(got) != len(tt.want) {
				t.Fatalf("len mismatch: got %d, want %d", len(got), len(tt.want))
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("index %d: got %q, want %q", i, got[i], tt.want[i])
				}
			}
		})
	}
}

func TestParseRetention(t *testing.T) {
	tests := []struct {
		name             string
		labels           map[string]string
		defaultRetention string
		want             restic.RetentionPolicy
	}{
		{
			name:   "empty label, empty default",
			labels: map[string]string{},
			want:   restic.RetentionPolicy{},
		},
		{
			name:             "empty label, default set",
			labels:           map[string]string{},
			defaultRetention: "keep-daily:14",
			want:             restic.RetentionPolicy{KeepDaily: 14},
		},
		{
			name:   "label set with all keys",
			labels: map[string]string{"buoy.retention": "keep-daily:30,keep-weekly:4,keep-monthly:12,keep-yearly:2"},
			want:   restic.RetentionPolicy{KeepDaily: 30, KeepWeekly: 4, KeepMonthly: 12, KeepYearly: 2},
		},
		{
			name:   "keep-within string value",
			labels: map[string]string{"buoy.retention": "keep-within:7d"},
			want:   restic.RetentionPolicy{KeepWithin: "7d"},
		},
		{
			name:   "malformed key:value silently skipped",
			labels: map[string]string{"buoy.retention": "bad_entry"},
			want:   restic.RetentionPolicy{},
		},
		{
			name:   "mixed valid and invalid entries",
			labels: map[string]string{"buoy.retention": "keep-daily:10,bad_entry,keep-weekly:3"},
			want:   restic.RetentionPolicy{KeepDaily: 10, KeepWeekly: 3},
		},
		{
			name:   "keep-within with spaces",
			labels: map[string]string{"buoy.retention": "keep-within: 7d  , keep-daily : 5"},
			want:   restic.RetentionPolicy{KeepDaily: 5, KeepWithin: "7d"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rc := &restic.RetentionPolicy{}
			parseRetention(tt.labels, tt.defaultRetention, rc, nil)
			if rc.KeepDaily != tt.want.KeepDaily {
				t.Errorf("KeepDaily: got %d, want %d", rc.KeepDaily, tt.want.KeepDaily)
			}
			if rc.KeepWeekly != tt.want.KeepWeekly {
				t.Errorf("KeepWeekly: got %d, want %d", rc.KeepWeekly, tt.want.KeepWeekly)
			}
			if rc.KeepMonthly != tt.want.KeepMonthly {
				t.Errorf("KeepMonthly: got %d, want %d", rc.KeepMonthly, tt.want.KeepMonthly)
			}
			if rc.KeepYearly != tt.want.KeepYearly {
				t.Errorf("KeepYearly: got %d, want %d", rc.KeepYearly, tt.want.KeepYearly)
			}
			if rc.KeepWithin != tt.want.KeepWithin {
				t.Errorf("KeepWithin: got %q, want %q", rc.KeepWithin, tt.want.KeepWithin)
			}
		})
	}
}

func TestRepoPath(t *testing.T) {
	tests := []struct {
		name string
		ctr  Container
		base string
		want string
	}{
		{
			name: "standalone container",
			ctr:  Container{Name: "myapp"},
			base: "/backup",
			want: "/backup/myapp",
		},
		{
			name: "compose stack container",
			ctr:  Container{Name: "web", ComposeProject: "myproject", ComposeService: "web"},
			base: "/backup",
			want: "/backup/myproject/web",
		},
		{
			name: "name with leading slash stripped",
			ctr:  Container{Name: "/myapp"},
			base: "/backup",
			want: "/backup/myapp",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.ctr.RepoPath(tt.base)
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestIsComposeStack(t *testing.T) {
	tests := []struct {
		name string
		ctr  Container
		want bool
	}{
		{"project set", Container{ComposeProject: "proj"}, true},
		{"project empty", Container{}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.ctr.IsComposeStack()
			if got != tt.want {
				t.Errorf("got %v, want %v", got, tt.want)
			}
		})
	}
}

func TestLogAttrs(t *testing.T) {
	tests := []struct {
		name string
		ctr  Container
		want []any
	}{
		{
			name: "compose stack",
			ctr:  Container{Name: "web", ComposeProject: "myproject", ComposeService: "web"},
			want: []any{"project", "myproject", "service", "web"},
		},
		{
			name: "standalone",
			ctr:  Container{Name: "myapp"},
			want: []any{"container", "myapp"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.ctr.LogAttrs()
			if len(got) != len(tt.want) {
				t.Fatalf("len mismatch: got %d, want %d", len(got), len(tt.want))
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("index %d: got %v, want %v", i, got[i], tt.want[i])
				}
			}
		})
	}
}

func TestParseBackupConfig(t *testing.T) {
	tests := []struct {
		name             string
		labels           map[string]string
		defaultSchedule  string
		defaultRetention string
		want             BackupConfig
	}{
		{
			name:   "buoy.enabled=true",
			labels: map[string]string{"buoy.enabled": "true"},
			want: BackupConfig{
				Enabled:     true,
				StopTimeout: 30 * time.Second,
				Retention:   restic.RetentionPolicy{KeepDaily: 7},
			},
		},
		{
			name:   "buoy.enabled=false (default)",
			labels: map[string]string{},
			want: BackupConfig{
				StopTimeout: 30 * time.Second,
				Retention:   restic.RetentionPolicy{KeepDaily: 7},
			},
		},
		{
			name: "no schedule, empty default",
			labels: map[string]string{
				"buoy.enabled": "true",
			},
			want: BackupConfig{
				Enabled:     true,
				StopTimeout: 30 * time.Second,
				Retention:   restic.RetentionPolicy{KeepDaily: 7},
			},
		},
		{
			name:            "no schedule, default set",
			labels:          map[string]string{"buoy.enabled": "true"},
			defaultSchedule: "@daily",
			want: BackupConfig{
				Enabled:     true,
				Schedule:    "@daily",
				StopTimeout: 30 * time.Second,
				Retention:   restic.RetentionPolicy{KeepDaily: 7},
			},
		},
		{
			name:   "buoy.stop-timeout=5m",
			labels: map[string]string{"buoy.stop-timeout": "5m"},
			want: BackupConfig{
				StopTimeout: 5 * time.Minute,
				Retention:   restic.RetentionPolicy{KeepDaily: 7},
			},
		},
		{
			name:   "invalid stop-timeout -> default 30s",
			labels: map[string]string{"buoy.stop-timeout": "not_a_duration"},
			want: BackupConfig{
				StopTimeout: 30 * time.Second,
				Retention:   restic.RetentionPolicy{KeepDaily: 7},
			},
		},
		{
			name:   "include-volumes",
			labels: map[string]string{"buoy.include-volumes": "vol1, vol2"},
			want: BackupConfig{
				IncludeVolumes: []string{"vol1", "vol2"},
				StopTimeout:    30 * time.Second,
				Retention:      restic.RetentionPolicy{KeepDaily: 7},
			},
		},
		{
			name:   "exclude-volumes",
			labels: map[string]string{"buoy.exclude-volumes": "vol1, vol2"},
			want: BackupConfig{
				ExcludeVolumes: []string{"vol1", "vol2"},
				StopTimeout:    30 * time.Second,
				Retention:      restic.RetentionPolicy{KeepDaily: 7},
			},
		},
		{
			name:   "include-mounts",
			labels: map[string]string{"buoy.include-mounts": "/data, /config"},
			want: BackupConfig{
				IncludeMounts: []string{"/data", "/config"},
				StopTimeout:   30 * time.Second,
				Retention:     restic.RetentionPolicy{KeepDaily: 7},
			},
		},
		{
			name:   "exclude-mounts",
			labels: map[string]string{"buoy.exclude-mounts": "/data, /config"},
			want: BackupConfig{
				ExcludeMounts: []string{"/data", "/config"},
				StopTimeout:   30 * time.Second,
				Retention:     restic.RetentionPolicy{KeepDaily: 7},
			},
		},
		{
			name:   "buoy.tags=foo,bar",
			labels: map[string]string{"buoy.tags": "foo, bar"},
			want: BackupConfig{
				Tags:        []string{"foo", "bar"},
				StopTimeout: 30 * time.Second,
				Retention:   restic.RetentionPolicy{KeepDaily: 7},
			},
		},
		{
			name:   "buoy.exclude-patterns=*.log,*.tmp",
			labels: map[string]string{"buoy.exclude-patterns": "*.log, *.tmp"},
			want: BackupConfig{
				ExcludePatterns: []string{"*.log", "*.tmp"},
				StopTimeout:     30 * time.Second,
				Retention:       restic.RetentionPolicy{KeepDaily: 7},
			},
		},
		{
			name: "hook commands",
			labels: map[string]string{
				"buoy.pre-backup-cmd":   "echo pre",
				"buoy.post-backup-cmd":  "echo post",
				"buoy.pre-backup-exec":  "echo pre exec",
				"buoy.post-backup-exec": "echo post exec",
			},
			want: BackupConfig{
				PreBackupCmd:   "echo pre",
				PostBackupCmd:  "echo post",
				PreBackupExec:  "echo pre exec",
				PostBackupExec: "echo post exec",
				StopTimeout:    30 * time.Second,
				Retention:      restic.RetentionPolicy{KeepDaily: 7},
			},
		},
		{
			name: "full config with all labels set",
			labels: map[string]string{
				"buoy.enabled":            "true",
				"buoy.schedule":           "@daily",
				"buoy.repos":              "/custom/repo",
				"buoy.retention":          "keep-daily:30,keep-weekly:4",
				"buoy.stop-before-backup": "true",
				"buoy.stop-timeout":       "2m",
				"buoy.include-volumes":    "data",
				"buoy.exclude-mounts":     "/tmp",
				"buoy.exclude-patterns":   "*.log",
				"buoy.files":              "important.txt",
				"buoy.tags":               "critical, db",
				"buoy.pre-backup-cmd":     "pg_dump > /backup/dump.sql",
				"buoy.post-backup-exec":   "echo done",
			},
			defaultRetention: "keep-daily:7",
			want: BackupConfig{
				Enabled:         true,
				Schedule:        "@daily",
				ReposOverride:   []string{"/custom/repo"},
				StopBefore:      true,
				StopTimeout:     2 * time.Minute,
				IncludeVolumes:  []string{"data"},
				ExcludeMounts:   []string{"/tmp"},
				ExcludePatterns: []string{"*.log"},
				Files:           []string{"important.txt"},
				Tags:            []string{"critical", "db"},
				PreBackupCmd:    "pg_dump > /backup/dump.sql",
				PostBackupExec:  "echo done",
				Retention:       restic.RetentionPolicy{KeepDaily: 30, KeepWeekly: 4},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ParseBackupConfig(tt.labels, tt.defaultSchedule, tt.defaultRetention, nil)
			if got.Enabled != tt.want.Enabled {
				t.Errorf("Enabled: got %v, want %v", got.Enabled, tt.want.Enabled)
			}
			if got.Schedule != tt.want.Schedule {
				t.Errorf("Schedule: got %q, want %q", got.Schedule, tt.want.Schedule)
			}
			if len(got.ReposOverride) != len(tt.want.ReposOverride) || (len(got.ReposOverride) > 0 && got.ReposOverride[0] != tt.want.ReposOverride[0]) {
				t.Errorf("ReposOverride: got %v, want %v", got.ReposOverride, tt.want.ReposOverride)
			}
			if got.StopBefore != tt.want.StopBefore {
				t.Errorf("StopBefore: got %v, want %v", got.StopBefore, tt.want.StopBefore)
			}
			if got.StopTimeout != tt.want.StopTimeout {
				t.Errorf("StopTimeout: got %v, want %v", got.StopTimeout, tt.want.StopTimeout)
			}
			if len(got.IncludeVolumes) != len(tt.want.IncludeVolumes) {
				t.Errorf("IncludeVolumes: got %v, want %v", got.IncludeVolumes, tt.want.IncludeVolumes)
			}
			if len(got.ExcludeVolumes) != len(tt.want.ExcludeVolumes) {
				t.Errorf("ExcludeVolumes: got %v, want %v", got.ExcludeVolumes, tt.want.ExcludeVolumes)
			}
			if len(got.IncludeMounts) != len(tt.want.IncludeMounts) {
				t.Errorf("IncludeMounts: got %v, want %v", got.IncludeMounts, tt.want.IncludeMounts)
			}
			if len(got.ExcludeMounts) != len(tt.want.ExcludeMounts) {
				t.Errorf("ExcludeMounts: got %v, want %v", got.ExcludeMounts, tt.want.ExcludeMounts)
			}
			if len(got.ExcludePatterns) != len(tt.want.ExcludePatterns) {
				t.Errorf("ExcludePatterns: got %v, want %v", got.ExcludePatterns, tt.want.ExcludePatterns)
			}
			if len(got.Files) != len(tt.want.Files) {
				t.Errorf("Files: got %v, want %v", got.Files, tt.want.Files)
			}
			if len(got.Tags) != len(tt.want.Tags) {
				t.Errorf("Tags: got %v, want %v", got.Tags, tt.want.Tags)
			}
			if got.PreBackupCmd != tt.want.PreBackupCmd {
				t.Errorf("PreBackupCmd: got %q, want %q", got.PreBackupCmd, tt.want.PreBackupCmd)
			}
			if got.PostBackupCmd != tt.want.PostBackupCmd {
				t.Errorf("PostBackupCmd: got %q, want %q", got.PostBackupCmd, tt.want.PostBackupCmd)
			}
			if got.PreBackupExec != tt.want.PreBackupExec {
				t.Errorf("PreBackupExec: got %q, want %q", got.PreBackupExec, tt.want.PreBackupExec)
			}
			if got.PostBackupExec != tt.want.PostBackupExec {
				t.Errorf("PostBackupExec: got %q, want %q", got.PostBackupExec, tt.want.PostBackupExec)
			}
			if got.Retention.KeepDaily != tt.want.Retention.KeepDaily {
				t.Errorf("Retention.KeepDaily: got %d, want %d", got.Retention.KeepDaily, tt.want.Retention.KeepDaily)
			}
			if got.Retention.KeepWeekly != tt.want.Retention.KeepWeekly {
				t.Errorf("Retention.KeepWeekly: got %d, want %d", got.Retention.KeepWeekly, tt.want.Retention.KeepWeekly)
			}
			if got.Retention.KeepMonthly != tt.want.Retention.KeepMonthly {
				t.Errorf("Retention.KeepMonthly: got %d, want %d", got.Retention.KeepMonthly, tt.want.Retention.KeepMonthly)
			}
			if got.Retention.KeepYearly != tt.want.Retention.KeepYearly {
				t.Errorf("Retention.KeepYearly: got %d, want %d", got.Retention.KeepYearly, tt.want.Retention.KeepYearly)
			}
			if got.Retention.KeepWithin != tt.want.Retention.KeepWithin {
				t.Errorf("Retention.KeepWithin: got %q, want %q", got.Retention.KeepWithin, tt.want.Retention.KeepWithin)
			}
		})
	}
}
