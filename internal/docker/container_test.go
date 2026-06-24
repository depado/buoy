package docker

import (
	"testing"
	"time"

	"github.com/depado/buoy/internal/types"
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
			got := types.SplitTrim(tt.input)
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
		want             types.RetentionPolicy
	}{
		{
			name:   "empty label, empty default",
			labels: map[string]string{},
			want:   types.RetentionPolicy{},
		},
		{
			name:             "empty label, default set",
			labels:           map[string]string{},
			defaultRetention: "keep-daily:14",
			want:             types.RetentionPolicy{KeepDaily: 14},
		},
		{
			name:   "label set with all keys",
			labels: map[string]string{"buoy.retention": "keep-daily:30,keep-weekly:4,keep-monthly:12,keep-yearly:2"},
			want:   types.RetentionPolicy{KeepDaily: 30, KeepWeekly: 4, KeepMonthly: 12, KeepYearly: 2},
		},
		{
			name:   "keep-within string value",
			labels: map[string]string{"buoy.retention": "keep-within:7d"},
			want:   types.RetentionPolicy{KeepWithin: "7d"},
		},
		{
			name:   "malformed key:value silently skipped",
			labels: map[string]string{"buoy.retention": "bad_entry"},
			want:   types.RetentionPolicy{},
		},
		{
			name:   "mixed valid and invalid entries",
			labels: map[string]string{"buoy.retention": "keep-daily:10,bad_entry,keep-weekly:3"},
			want:   types.RetentionPolicy{KeepDaily: 10, KeepWeekly: 3},
		},
		{
			name:   "keep-within with spaces",
			labels: map[string]string{"buoy.retention": "keep-within: 7d  , keep-daily : 5"},
			want:   types.RetentionPolicy{KeepDaily: 5, KeepWithin: "7d"},
		},
		{
			name:   "keep-last",
			labels: map[string]string{"buoy.retention": "keep-last:10"},
			want:   types.RetentionPolicy{KeepLast: 10},
		},
		{
			name:   "keep-hourly",
			labels: map[string]string{"buoy.retention": "keep-hourly:24"},
			want:   types.RetentionPolicy{KeepHourly: 24},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rc := &types.RetentionPolicy{}
			parseRetention(tt.labels, tt.defaultRetention, rc)
			if rc.KeepLast != tt.want.KeepLast {
				t.Errorf("KeepLast: got %d, want %d", rc.KeepLast, tt.want.KeepLast)
			}
			if rc.KeepHourly != tt.want.KeepHourly {
				t.Errorf("KeepHourly: got %d, want %d", rc.KeepHourly, tt.want.KeepHourly)
			}
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
				Retention:   types.RetentionPolicy{},
				MountOpts:   make(map[string]MountBackupOpts),
			},
		},
		{
			name:   "buoy.enabled=false (default)",
			labels: map[string]string{},
			want: BackupConfig{
				StopTimeout: 30 * time.Second,
				Retention:   types.RetentionPolicy{},
				MountOpts:   make(map[string]MountBackupOpts),
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
				Retention:   types.RetentionPolicy{},
				MountOpts:   make(map[string]MountBackupOpts),
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
				Retention:   types.RetentionPolicy{},
				MountOpts:   make(map[string]MountBackupOpts),
			},
		},
		{
			name:   "buoy.stop-timeout=5m",
			labels: map[string]string{"buoy.stop-timeout": "5m"},
			want: BackupConfig{
				StopTimeout: 5 * time.Minute,
				Retention:   types.RetentionPolicy{},
				MountOpts:   make(map[string]MountBackupOpts),
			},
		},
		{
			name:   "invalid stop-timeout -> default 30s",
			labels: map[string]string{"buoy.stop-timeout": "not_a_duration"},
			want: BackupConfig{
				StopTimeout: 30 * time.Second,
				Retention:   types.RetentionPolicy{},
				MountOpts:   make(map[string]MountBackupOpts),
			},
		},
		{
			name:   "buoy.include (unnamed)",
			labels: map[string]string{"buoy.include": "vol1, /data"},
			want: BackupConfig{
				Include:     []MountEntry{{Key: "vol1"}, {Key: "/data"}},
				StopTimeout: 30 * time.Second,
				Retention:   types.RetentionPolicy{},
				MountOpts:   make(map[string]MountBackupOpts),
			},
		},
		{
			name:   "buoy.include (named)",
			labels: map[string]string{"buoy.include": "src=/app/code, data=/app/data"},
			want: BackupConfig{
				Include: []MountEntry{
					{Name: "src", Key: "/app/code"},
					{Name: "data", Key: "/app/data"},
				},
				StopTimeout: 30 * time.Second,
				Retention:   types.RetentionPolicy{},
				MountOpts:   make(map[string]MountBackupOpts),
			},
		},
		{
			name:   "buoy.include (mixed named and unnamed)",
			labels: map[string]string{"buoy.include": "src=/app/code, /tmp/scratch"},
			want: BackupConfig{
				Include: []MountEntry{
					{Name: "src", Key: "/app/code"},
					{Key: "/tmp/scratch"},
				},
				StopTimeout: 30 * time.Second,
				Retention:   types.RetentionPolicy{},
				MountOpts:   make(map[string]MountBackupOpts),
			},
		},
		{
			name:   "buoy.include (empty key skipped)",
			labels: map[string]string{"buoy.include": "src= , /data"},
			want: BackupConfig{
				Include:     []MountEntry{{Key: "/data"}},
				StopTimeout: 30 * time.Second,
				Retention:   types.RetentionPolicy{},
				MountOpts:   make(map[string]MountBackupOpts),
			},
		},
		{
			name:   "buoy.include (duplicate name warned, first wins)",
			labels: map[string]string{"buoy.include": "src=/app, src=/other"},
			want: BackupConfig{
				Include:     []MountEntry{{Name: "src", Key: "/app"}},
				StopTimeout: 30 * time.Second,
				Retention:   types.RetentionPolicy{},
				MountOpts:   make(map[string]MountBackupOpts),
			},
		},
		{
			name:   "buoy.exclude",
			labels: map[string]string{"buoy.exclude": "/data, vol1"},
			want: BackupConfig{
				Exclude:     []string{"/data", "vol1"},
				StopTimeout: 30 * time.Second,
				Retention:   types.RetentionPolicy{},
				MountOpts:   make(map[string]MountBackupOpts),
			},
		},
		{
			name:   "buoy.backup.tags",
			labels: map[string]string{"buoy.backup.tags": "foo, bar"},
			want: BackupConfig{
				BackupTags:  []string{"foo", "bar"},
				StopTimeout: 30 * time.Second,
				Retention:   types.RetentionPolicy{},
				MountOpts:   make(map[string]MountBackupOpts),
			},
		},
		{
			name:   "buoy.backup.exclude",
			labels: map[string]string{"buoy.backup.exclude": "*.log, *.tmp"},
			want: BackupConfig{
				BackupExclude: []string{"*.log", "*.tmp"},
				StopTimeout:   30 * time.Second,
				Retention:     types.RetentionPolicy{},				MountOpts:     make(map[string]MountBackupOpts),
			},
		},
		{
			name: "hook commands",
			labels: map[string]string{
				"buoy.hook.pre.cmd":   "echo pre",
				"buoy.hook.post.cmd":  "echo post",
				"buoy.hook.pre.exec":  "echo pre exec",
				"buoy.hook.post.exec": "echo post exec",
			},
			want: BackupConfig{
				HookPreCmd:   "echo pre",
				HookPostCmd:  "echo post",
				HookPreExec:  "echo pre exec",
				HookPostExec: "echo post exec",
				StopTimeout:  30 * time.Second,
				Retention:    types.RetentionPolicy{},
				MountOpts:    make(map[string]MountBackupOpts),
			},
		},
		{
			name: "per-mount backup opts via wildcard",
			labels: map[string]string{
				"buoy.backup.src.files":   "*.go, *.ts",
				"buoy.backup.src.exclude": "*.tmp",
				"buoy.backup.src.tags":    "source, critical",
				"buoy.backup.data.files":  "*.sql",
			},
			want: BackupConfig{
				StopTimeout: 30 * time.Second,
				Retention:   types.RetentionPolicy{},
				MountOpts: map[string]MountBackupOpts{
					"src":  {Files: []string{"*.go", "*.ts"}, Exclude: []string{"*.tmp"}, Tags: []string{"source", "critical"}},
					"data": {Files: []string{"*.sql"}},
				},
			},
		},
		{
			name: "full config with all labels set",
			labels: map[string]string{
				"buoy.enabled":        "true",
				"buoy.schedule":       "@daily",
				"buoy.repos":          "/custom/repo",
				"buoy.retention":      "keep-daily:30,keep-weekly:4",
				"buoy.stop-before":    "true",
				"buoy.stop-timeout":   "2m",
				"buoy.include":        "data, /tmp",
				"buoy.exclude":        "/scratch",
				"buoy.backup.exclude": "*.log",
				"buoy.backup.files":   "important.txt",
				"buoy.backup.tags":    "critical, db",
				"buoy.hook.pre.cmd":   "pg_dump > /backup/dump.sql",
				"buoy.hook.post.exec": "echo done",
			},
			defaultRetention: "keep-within:7d,keep-daily:7,keep-weekly:4,keep-monthly:6,keep-yearly:3",
			want: BackupConfig{
				Enabled:       true,
				Schedule:      "@daily",
				ReposOverride: []string{"/custom/repo"},
				StopBefore:    true,
				StopTimeout:   2 * time.Minute,
				Include:       []MountEntry{{Key: "data"}, {Key: "/tmp"}},
				Exclude:       []string{"/scratch"},
				BackupExclude: []string{"*.log"},
				BackupFiles:   []string{"important.txt"},
				BackupTags:    []string{"critical", "db"},
				HookPreCmd:    "pg_dump > /backup/dump.sql",
				HookPostExec:  "echo done",
				Retention:     types.RetentionPolicy{KeepDaily: 30, KeepWeekly: 4},
				MountOpts:     make(map[string]MountBackupOpts),
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ParseBackupConfig(tt.labels, tt.defaultSchedule, tt.defaultRetention)
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
			if len(got.Include) != len(tt.want.Include) {
				t.Errorf("Include: got %v, want %v", got.Include, tt.want.Include)
			} else {
				for i := range got.Include {
					if got.Include[i] != tt.want.Include[i] {
						t.Errorf("Include[%d]: got %+v, want %+v", i, got.Include[i], tt.want.Include[i])
					}
				}
			}
			if len(got.Exclude) != len(tt.want.Exclude) {
				t.Errorf("Exclude: got %v, want %v", got.Exclude, tt.want.Exclude)
			} else {
				for i := range got.Exclude {
					if got.Exclude[i] != tt.want.Exclude[i] {
						t.Errorf("Exclude[%d]: got %q, want %q", i, got.Exclude[i], tt.want.Exclude[i])
					}
				}
			}
			if len(got.BackupExclude) != len(tt.want.BackupExclude) {
				t.Errorf("BackupExclude: got %v, want %v", got.BackupExclude, tt.want.BackupExclude)
			}
			if len(got.BackupFiles) != len(tt.want.BackupFiles) {
				t.Errorf("BackupFiles: got %v, want %v", got.BackupFiles, tt.want.BackupFiles)
			}
			if len(got.BackupTags) != len(tt.want.BackupTags) {
				t.Errorf("BackupTags: got %v, want %v", got.BackupTags, tt.want.BackupTags)
			}
			if got.HookPreCmd != tt.want.HookPreCmd {
				t.Errorf("HookPreCmd: got %q, want %q", got.HookPreCmd, tt.want.HookPreCmd)
			}
			if got.HookPostCmd != tt.want.HookPostCmd {
				t.Errorf("HookPostCmd: got %q, want %q", got.HookPostCmd, tt.want.HookPostCmd)
			}
			if got.HookPreExec != tt.want.HookPreExec {
				t.Errorf("HookPreExec: got %q, want %q", got.HookPreExec, tt.want.HookPreExec)
			}
			if got.HookPostExec != tt.want.HookPostExec {
				t.Errorf("HookPostExec: got %q, want %q", got.HookPostExec, tt.want.HookPostExec)
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
			if len(got.MountOpts) != len(tt.want.MountOpts) {
				t.Errorf("MountOpts len: got %d, want %d", len(got.MountOpts), len(tt.want.MountOpts))
			} else {
				for k, v := range got.MountOpts {
					w, ok := tt.want.MountOpts[k]
					if !ok {
						t.Errorf("MountOpts[%q]: unexpected key", k)
						continue
					}
					if len(v.Files) != len(w.Files) || (len(v.Files) > 0 && v.Files[0] != w.Files[0]) {
						t.Errorf("MountOpts[%q].Files: got %v, want %v", k, v.Files, w.Files)
					}
					if len(v.Exclude) != len(w.Exclude) || (len(v.Exclude) > 0 && v.Exclude[0] != w.Exclude[0]) {
						t.Errorf("MountOpts[%q].Exclude: got %v, want %v", k, v.Exclude, w.Exclude)
					}
					if len(v.Tags) != len(w.Tags) || (len(v.Tags) > 0 && v.Tags[0] != w.Tags[0]) {
						t.Errorf("MountOpts[%q].Tags: got %v, want %v", k, v.Tags, w.Tags)
					}
				}
			}
		})
	}
}

func TestMountMatches(t *testing.T) {
	tests := []struct {
		name     string
		mount    Mount
		include  []MountEntry
		exclude  []string
		wantOK   bool
		wantName string
	}{
		{
			name:   "no include or exclude -> all pass",
			mount:  Mount{Name: "vol1", Source: "/data", Destination: "/app"},
			wantOK: true,
		},
		{
			name:     "include by volume name (auto-name)",
			mount:    Mount{Name: "vol1", Source: "/var/lib/docker/volumes/vol1/_data"},
			include:  []MountEntry{{Key: "vol1"}},
			wantOK:   true,
			wantName: "vol1",
		},
		{
			name:    "include by source path",
			mount:   Mount{Type: "bind", Source: "/data", Destination: "/app"},
			include: []MountEntry{{Key: "/data"}},
			wantOK:  true,
		},
		{
			name:     "named entry takes priority over auto-name",
			mount:    Mount{Name: "vol1"},
			include:  []MountEntry{{Name: "custom", Key: "vol1"}},
			wantOK:   true,
			wantName: "custom",
		},
		{
			name:    "include by destination path",
			mount:   Mount{Type: "bind", Source: "/data", Destination: "/app"},
			include: []MountEntry{{Key: "/app"}},
			wantOK:  true,
		},
		{
			name:    "include mismatch",
			mount:   Mount{Name: "vol1"},
			include: []MountEntry{{Key: "other"}},
			wantOK:  false,
		},
		{
			name:    "exclude by source",
			mount:   Mount{Type: "bind", Source: "/tmp"},
			exclude: []string{"/tmp"},
			wantOK:  false,
		},
		{
			name:    "exclude by destination",
			mount:   Mount{Type: "bind", Source: "/data", Destination: "/tmp"},
			exclude: []string{"/tmp"},
			wantOK:  false,
		},
		{
			name:    "exclude by name",
			mount:   Mount{Name: "tmp_vol"},
			exclude: []string{"tmp_vol"},
			wantOK:  false,
		},
		{
			name:    "not excluded",
			mount:   Mount{Name: "vol1"},
			exclude: []string{"other"},
			wantOK:  true,
		},
		{
			name:     "include takes priority over exclude",
			mount:    Mount{Name: "vol1"},
			include:  []MountEntry{{Key: "vol1"}},
			exclude:  []string{"vol1"},
			wantOK:   true,
			wantName: "vol1",
		},
		{
			name:     "named include returns name",
			mount:    Mount{Name: "vol1"},
			include:  []MountEntry{{Name: "src", Key: "vol1"}},
			wantOK:   true,
			wantName: "src",
		},
		{
			name:     "first match wins for duplicate matches",
			mount:    Mount{Name: "vol1"},
			include:  []MountEntry{{Name: "first", Key: "vol1"}, {Name: "second", Key: "vol1"}},
			wantOK:   true,
			wantName: "first",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotName, gotOK := MountMatches(tt.mount, tt.include, tt.exclude)
			if gotOK != tt.wantOK {
				t.Errorf("ok: got %v, want %v", gotOK, tt.wantOK)
			}
			if gotName != tt.wantName {
				t.Errorf("name: got %q, want %q", gotName, tt.wantName)
			}
		})
	}
}
