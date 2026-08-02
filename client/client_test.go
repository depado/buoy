package client

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/depado/buoy/internal/types"
)

func TestTriggerBackup(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if r.URL.Path != "/api/v1/backup" {
			t.Errorf("expected /api/v1/backup, got %s", r.URL.Path)
		}

		containers := r.URL.Query()["container"]
		if len(containers) != 2 || containers[0] != "uptime-kuma" || containers[1] != "beszel" {
			t.Errorf("unexpected containers: %v", containers)
		}

		_ = json.NewEncoder(w).Encode([]types.APIBackupResult{
			{Container: "uptime-kuma", OK: true},
			{Container: "beszel", OK: true},
		})
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "")
	results, err := c.TriggerBackup([]string{"uptime-kuma", "beszel"})
	if err != nil {
		t.Fatalf("TriggerBackup: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
	if !results[0].OK || results[0].Container != "uptime-kuma" {
		t.Errorf("unexpected result[0]: %+v", results[0])
	}
}

func TestTriggerBackupAll(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("all") != "true" {
			t.Error("expected ?all=true")
		}

		_ = json.NewEncoder(w).Encode([]types.APIBackupResult{
			{Container: "uptime-kuma", OK: true},
			{Container: "beszel", OK: false, Error: "broken"},
		})
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "")
	results, err := c.TriggerBackupAll()
	if err != nil {
		t.Fatalf("TriggerBackupAll: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
	if results[1].OK || results[1].Error != "broken" {
		t.Errorf("expected failure, got %+v", results[1])
	}
}

func TestTriggerProjectBackup(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("project") != "myapp" {
			t.Errorf("expected project=myapp, got %q", r.URL.Query().Get("project"))
		}

		_ = json.NewEncoder(w).Encode([]types.APIBackupResult{
			{Container: "myapp", OK: true},
		})
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "")
	result, err := c.TriggerProjectBackup("myapp", nil)
	if err != nil {
		t.Fatalf("TriggerProjectBackup: %v", err)
	}
	if !result.OK || result.Container != "myapp" {
		t.Errorf("unexpected result: %+v", result)
	}
}

func TestTriggerProjectBackup_Services(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("project") != "myapp" {
			t.Errorf("expected project=myapp, got %q", r.URL.Query().Get("project"))
		}
		containers := r.URL.Query()["container"]
		if len(containers) != 2 || containers[0] != "db" || containers[1] != "api" {
			t.Errorf("unexpected containers: %v", containers)
		}

		_ = json.NewEncoder(w).Encode([]types.APIBackupResult{
			{Container: "myapp", OK: true},
		})
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "")
	result, err := c.TriggerProjectBackup("myapp", []string{"db", "api"})
	if err != nil {
		t.Fatalf("TriggerProjectBackup: %v", err)
	}
	if !result.OK || result.Container != "myapp" {
		t.Errorf("unexpected result: %+v", result)
	}
}

func TestTriggerBackup_NoContainers(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode([]types.APIBackupResult{})
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "")
	results, err := c.TriggerBackup(nil)
	if err != nil {
		t.Fatalf("TriggerBackup: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("expected 0 results, got %d", len(results))
	}
}
