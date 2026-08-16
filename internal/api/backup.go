package api

import (
	"context"
	"net/http"
	"slices"
	"strings"
	"sync"

	"github.com/depado/buoy/internal/types"
)

func (s *Server) handleTriggerBackup(w http.ResponseWriter, r *http.Request) {
	all := r.URL.Query().Get("all") == "true"
	containers := r.URL.Query()["container"]
	project := r.URL.Query().Get("project")

	if project != "" {
		err := s.scheduler.TriggerProjectBackup(r.Context(), project, containers)
		result := types.BackupResult{Container: project, OK: err == nil}
		if err != nil {
			result.Error = err.Error()
		}
		writeJSON(w, http.StatusOK, []types.BackupResult{result})
		return
	}

	if all {
		results := s.triggerAll(r.Context())
		writeJSON(w, http.StatusOK, results)
		return
	}

	if len(containers) == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "specify ?all=true, ?project=<name>, or ?container=<name>"})
		return
	}
	if slices.Contains(containers, "") {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "container parameter must not be empty"})
		return
	}

	results := make([]types.BackupResult, len(containers))
	var wg sync.WaitGroup
	for i, target := range containers {
		wg.Add(1)
		go func(idx int, name string) {
			defer wg.Done()
			err := s.scheduler.TriggerBackup(r.Context(), name)
			results[idx] = types.BackupResult{
				Container: name,
				OK:        err == nil,
				Queued:    err != nil && strings.Contains(err.Error(), "queued"),
			}
			if err != nil {
				results[idx].Error = err.Error()
			}
		}(i, target)
	}
	wg.Wait()
	writeJSON(w, http.StatusOK, results)
}

func (s *Server) triggerAll(ctx context.Context) []types.BackupResult {
	entries := s.scheduler.ListScheduled()
	if len(entries) == 0 {
		return nil
	}

	seen := make(map[string]bool)
	var projects []string
	var standalones []string
	for _, e := range entries {
		if e.ComposeProject != "" {
			if !seen[e.ComposeProject] {
				seen[e.ComposeProject] = true
				projects = append(projects, e.ComposeProject)
			}
		} else {
			standalones = append(standalones, e.ContainerName)
		}
	}

	n := len(projects) + len(standalones)
	results := make([]types.BackupResult, n)
	var wg sync.WaitGroup

	for i, proj := range projects {
		wg.Add(1)
		go func(idx int, p string) {
			defer wg.Done()
			err := s.scheduler.TriggerProjectBackup(ctx, p, nil)
			results[idx] = types.BackupResult{Container: p, OK: err == nil}
			if err != nil {
				results[idx].Error = err.Error()
			}
		}(i, proj)
	}
	for i, name := range standalones {
		wg.Add(1)
		go func(idx int, n string) {
			defer wg.Done()
			err := s.scheduler.TriggerBackup(ctx, n)
			results[idx] = types.BackupResult{Container: n, OK: err == nil}
			if err != nil {
				results[idx].Error = err.Error()
			}
		}(len(projects)+i, name)
	}
	wg.Wait()
	return results
}
