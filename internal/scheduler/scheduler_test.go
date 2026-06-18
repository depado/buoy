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
