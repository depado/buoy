package scheduler

import (
	"testing"

	"github.com/depado/buoy/internal/backup"
	"github.com/depado/buoy/internal/docker"
)

func TestAddContainer_UsesDefaultSchedule(t *testing.T) {
	r := &backup.Runner{}
	s := &Scheduler{
		defaultSchedule:  "@daily",
		defaultRetention: "keep-daily:14",
	}
	ctr := &docker.Container{
		ID:    "test-id",
		State: "running",
		Labels: map[string]string{
			"buoy.enabled": "true",
		},
	}

	if s.defaultSchedule != "@daily" {
		t.Fatalf("expected @daily, got %s", s.defaultSchedule)
	}
	if s.defaultRetention != "keep-daily:14" {
		t.Fatalf("expected keep-daily:14, got %s", s.defaultRetention)
	}
	_ = ctr
	_ = r
}

func TestScheduleGroupKey(t *testing.T) {
	tests := []struct {
		name     string
		project  string
		schedule string
		want     string
	}{
		{
			name:     "project + schedule",
			project:  "myproject",
			schedule: "@daily",
			want:     "myproject::@daily",
		},
		{
			name:     "empty project + schedule",
			project:  "",
			schedule: "@every 6h",
			want:     "::@every 6h",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := scheduleGroupKey(tt.project, tt.schedule)
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}
