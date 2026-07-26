package api

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/depado/buoy/client"
	"github.com/depado/buoy/internal/config"
	"github.com/depado/buoy/internal/registry"
	"github.com/depado/buoy/internal/restic"
	"github.com/depado/buoy/internal/scheduler"
	"github.com/depado/buoy/internal/types"
)

type Server struct {
	reg          *registry.Registry
	restic       *restic.Client
	scheduler    *scheduler.Scheduler
	resticConf   *config.ResticConf
	token        string
	version      string
	srv          *http.Server
	logger       *slog.Logger
	backupActive func() bool
}

func New(reg *registry.Registry, rc *restic.Client, sched *scheduler.Scheduler, resticConf *config.ResticConf, token, host string, port int, version string, logger *slog.Logger, backupActive func() bool) *Server {
	s := &Server{
		reg:          reg,
		restic:       rc,
		scheduler:    sched,
		resticConf:   resticConf,
		token:        token,
		version:      version,
		logger:       logger,
		backupActive: backupActive,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/health", s.handleHealth)
	mux.HandleFunc("GET /api/v1/scheduled", s.handleListScheduled)
	mux.HandleFunc("GET /api/v1/repos", s.handleListRepos)
	mux.HandleFunc("POST /api/v1/repos/check", s.handleReposCheck)
	mux.HandleFunc("POST /api/v1/repos/stats", s.handleReposStats)
	mux.HandleFunc("POST /api/v1/repos/unlock", s.handleReposUnlock)
	mux.HandleFunc("POST /api/v1/repos/forget", s.handleReposForget)
	mux.HandleFunc("POST /api/v1/repos/prune", s.handleReposPrune)
	mux.HandleFunc("POST /api/v1/backup", s.handleTriggerBackup)

	s.srv = &http.Server{
		Addr:              fmt.Sprintf("%s:%d", host, port),
		Handler:           withAuth(token)(mux),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	return s
}

func (s *Server) Start() error {
	s.logger.Info("api server listening", "addr", s.srv.Addr)
	return s.srv.ListenAndServe()
}

func (s *Server) passwordForRepo(entry registry.RepoEntry) string {
	if entry.RepoName != "" {
		return s.resticConf.PasswordFor(entry.RepoName)
	}
	return s.resticConf.PasswordForURL(entry.URL)
}

func (s *Server) Shutdown(ctx context.Context) error {
	return s.srv.Shutdown(ctx)
}

func (s *Server) rejectIfBusy(w http.ResponseWriter) bool {
	if s.backupActive != nil && s.backupActive() {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "cannot run destructive operation while a backup is in progress"})
		return true
	}
	return false
}

func withAuth(token string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/api/v1/health" {
				next.ServeHTTP(w, r)
				return
			}
			if token == "" {
				next.ServeHTTP(w, r)
				return
			}
			auth := r.Header.Get("Authorization")
			trimmed := strings.TrimPrefix(auth, "Bearer ")
			if !strings.HasPrefix(auth, "Bearer ") || subtle.ConstantTimeCompare([]byte(trimmed), []byte(token)) != 1 {
				writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{
		"message": "ok",
		"version": s.version,
	})
}

func (s *Server) handleListScheduled(w http.ResponseWriter, r *http.Request) {
	if s.scheduler == nil {
		writeJSON(w, http.StatusOK, []client.ScheduledEntry{})
		return
	}
	writeJSON(w, http.StatusOK, s.scheduler.ListScheduled())
}

func (s *Server) handleListRepos(w http.ResponseWriter, r *http.Request) {
	opts := listRepoOpts(r)
	entries, err := s.reg.ListRepos(opts...)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, entries)
}

func (s *Server) handleReposCheck(w http.ResponseWriter, r *http.Request) {
	readData := r.URL.Query().Get("read-data") == "true"
	entries, err := s.reg.ListRepos(listRepoOpts(r)...)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if len(entries) == 0 {
		writeJSON(w, http.StatusOK, map[string]string{"message": "no repos to check"})
		return
	}

	results := make([]client.Result, 0, len(entries))
	for _, entry := range entries {
		ctx := restic.WithPassword(r.Context(), s.passwordForRepo(entry))
		var checkErr error
		if readData {
			checkErr = s.restic.CheckReadData(ctx, entry.URL)
		} else {
			checkErr = s.restic.Check(ctx, entry.URL)
		}
		result := client.Result{Repo: entry.URL, OK: checkErr == nil}
		if checkErr != nil {
			result.Error = checkErr.Error()
		}
		results = append(results, result)
		if err := s.reg.MarkCheckComplete(entry.URL, checkErr == nil); err != nil {
			s.logger.Warn("failed to persist check status", "repo", entry.URL, "error", err)
		}
	}
	writeJSON(w, http.StatusOK, results)
}

func (s *Server) handleReposStats(w http.ResponseWriter, r *http.Request) {
	entries, err := s.reg.ListRepos(listRepoOpts(r)...)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if len(entries) == 0 {
		writeJSON(w, http.StatusOK, map[string]string{"message": "no repos"})
		return
	}

	var total restic.Stats
	perRepo := make([]client.RepoStats, 0, len(entries))
	for _, entry := range entries {
		st, err := s.restic.Stats(restic.WithPassword(r.Context(), s.passwordForRepo(entry)), entry.URL)
		if err != nil {
			perRepo = append(perRepo, client.RepoStats{Repo: entry.URL, Error: err.Error()})
			continue
		}
		perRepo = append(perRepo, client.RepoStats{Repo: entry.URL, Stats: &client.Stats{
			TotalSize:              st.TotalSize,
			TotalFileCount:         st.TotalFileCount,
			TotalBlobCount:         st.TotalBlobCount,
			SnapshotsCount:         st.SnapshotsCount,
			TotalUncompressedSize:  st.TotalUncompressedSize,
			CompressionRatio:       st.CompressionRatio,
			CompressionProgress:    st.CompressionProgress,
			CompressionSpaceSaving: st.CompressionSpaceSaving,
		}})
		total.TotalSize += st.TotalSize
		total.TotalFileCount += st.TotalFileCount
		total.TotalBlobCount += st.TotalBlobCount
		total.SnapshotsCount += st.SnapshotsCount
		total.TotalUncompressedSize += st.TotalUncompressedSize
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"total": &client.Stats{
			TotalSize:              total.TotalSize,
			TotalFileCount:         total.TotalFileCount,
			TotalBlobCount:         total.TotalBlobCount,
			SnapshotsCount:         total.SnapshotsCount,
			TotalUncompressedSize:  total.TotalUncompressedSize,
			CompressionRatio:       total.CompressionRatio,
			CompressionProgress:    total.CompressionProgress,
			CompressionSpaceSaving: total.CompressionSpaceSaving,
		},
		"repos": perRepo,
	})
}

func (s *Server) handleReposUnlock(w http.ResponseWriter, r *http.Request) {
	if s.rejectIfBusy(w) {
		return
	}
	entries, err := s.reg.ListRepos(listRepoOpts(r)...)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if len(entries) == 0 {
		writeJSON(w, http.StatusOK, map[string]string{"message": "no repos to unlock"})
		return
	}

	results := make([]client.Result, 0, len(entries))
	for _, entry := range entries {
		err := s.restic.Unlock(restic.WithPassword(r.Context(), s.passwordForRepo(entry)), entry.URL)
		result := client.Result{Repo: entry.URL, OK: err == nil}
		if err != nil {
			result.Error = err.Error()
		}
		results = append(results, result)
	}
	writeJSON(w, http.StatusOK, results)
}

func (s *Server) handleReposForget(w http.ResponseWriter, r *http.Request) {
	if s.rejectIfBusy(w) {
		return
	}
	retentionStr := r.URL.Query().Get("retention")
	if retentionStr == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "retention query parameter is required"})
		return
	}
	policy := types.ParseRetentionPolicy(retentionStr)

	entries, err := s.reg.ListRepos(listRepoOpts(r)...)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if len(entries) == 0 {
		writeJSON(w, http.StatusOK, map[string]string{"message": "no repos to forget"})
		return
	}

	results := make([]client.Result, 0, len(entries))
	for _, entry := range entries {
		err := s.restic.Forget(restic.WithPassword(r.Context(), s.passwordForRepo(entry)), entry.URL, policy, entry.ContainerName)
		result := client.Result{Repo: entry.URL, OK: err == nil}
		if err != nil {
			result.Error = err.Error()
		}
		results = append(results, result)
	}
	writeJSON(w, http.StatusOK, results)
}

func (s *Server) handleReposPrune(w http.ResponseWriter, r *http.Request) {
	if s.rejectIfBusy(w) {
		return
	}
	entries, err := s.reg.ListRepos(listRepoOpts(r)...)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if len(entries) == 0 {
		writeJSON(w, http.StatusOK, map[string]string{"message": "no repos to prune"})
		return
	}

	results := make([]client.Result, 0, len(entries))
	for _, entry := range entries {
		err := s.restic.Prune(restic.WithPassword(r.Context(), s.passwordForRepo(entry)), entry.URL)
		result := client.Result{Repo: entry.URL, OK: err == nil}
		if err != nil {
			result.Error = err.Error()
		}
		results = append(results, result)
	}
	writeJSON(w, http.StatusOK, results)
}

func (s *Server) handleTriggerBackup(w http.ResponseWriter, r *http.Request) {
	all := r.URL.Query().Get("all") == "true"
	containers := r.URL.Query()["container"]
	project := r.URL.Query().Get("project")

	if project != "" {
		err := s.scheduler.TriggerProjectBackup(r.Context(), project, containers)
		result := client.BackupResult{Container: project, OK: err == nil}
		if err != nil {
			result.Error = err.Error()
		}
		writeJSON(w, http.StatusOK, []client.BackupResult{result})
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

	results := make([]client.BackupResult, len(containers))
	var wg sync.WaitGroup
	for i, target := range containers {
		wg.Add(1)
		go func(idx int, name string) {
			defer wg.Done()
			err := s.scheduler.TriggerBackup(r.Context(), name)
			results[idx] = client.BackupResult{Container: name, OK: err == nil}
			if err != nil {
				results[idx].Error = err.Error()
			}
		}(i, target)
	}
	wg.Wait()
	writeJSON(w, http.StatusOK, results)
}

func (s *Server) triggerAll(ctx context.Context) []client.BackupResult {
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
	results := make([]client.BackupResult, n)
	var wg sync.WaitGroup

	for i, proj := range projects {
		wg.Add(1)
		go func(idx int, p string) {
			defer wg.Done()
			err := s.scheduler.TriggerProjectBackup(ctx, p, nil)
			results[idx] = client.BackupResult{Container: p, OK: err == nil}
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
			results[idx] = client.BackupResult{Container: n, OK: err == nil}
			if err != nil {
				results[idx].Error = err.Error()
			}
		}(len(projects)+i, name)
	}
	wg.Wait()
	return results
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func listRepoOpts(r *http.Request) []registry.ListOption {
	var opts []registry.ListOption
	switch r.URL.Query().Get("orphaned") {
	case "true":
		opts = append(opts, registry.OnlyOrphaned())
	case "false":
		opts = append(opts, registry.ExcludeOrphaned())
	}
	if repo := r.URL.Query().Get("repo"); repo != "" {
		opts = append(opts, registry.FilterByRepo(repo))
	}
	return opts
}
