package api

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/depado/buoy/internal/registry"
	"github.com/depado/buoy/internal/restic"
)

type Server struct {
	reg          *registry.Registry
	restic       *restic.Client
	token        string
	version      string
	srv          *http.Server
	logger       *slog.Logger
	backupActive func() bool
}

func New(reg *registry.Registry, rc *restic.Client, token, host string, port int, version string, logger *slog.Logger, backupActive func() bool) *Server {
	s := &Server{
		reg:          reg,
		restic:       rc,
		token:        token,
		version:      version,
		logger:       logger,
		backupActive: backupActive,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/health", s.handleHealth)
	mux.HandleFunc("GET /api/v1/repos", s.handleListRepos)
	mux.HandleFunc("POST /api/v1/repos/check", s.handleReposCheck)
	mux.HandleFunc("POST /api/v1/repos/stats", s.handleReposStats)
	mux.HandleFunc("POST /api/v1/repos/unlock", s.handleReposUnlock)
	mux.HandleFunc("POST /api/v1/repos/forget", s.handleReposForget)
	mux.HandleFunc("POST /api/v1/repos/prune", s.handleReposPrune)

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
		"status":  "ok",
		"version": s.version,
	})
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

	results := make([]CheckResult, 0, len(entries))
	for _, entry := range entries {
		var checkErr error
		if readData {
			checkErr = s.restic.CheckReadData(r.Context(), entry.URL)
		} else {
			checkErr = s.restic.Check(r.Context(), entry.URL)
		}
		result := CheckResult{Repo: entry.URL, OK: checkErr == nil}
		if checkErr != nil {
			result.Error = checkErr.Error()
		}
		results = append(results, result)
		_ = s.reg.MarkCheckComplete(entry.URL, checkErr == nil)
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
	perRepo := make([]repoStats, 0, len(entries))
	for _, entry := range entries {
		st, err := s.restic.Stats(r.Context(), entry.URL)
		if err != nil {
			perRepo = append(perRepo, repoStats{Repo: entry.URL, Error: err.Error()})
			continue
		}
		perRepo = append(perRepo, repoStats{Repo: entry.URL, Stats: st})
		total.TotalSize += st.TotalSize
		total.TotalFileCount += st.TotalFileCount
		total.TotalBlobCount += st.TotalBlobCount
		total.SnapshotsCount += st.SnapshotsCount
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"total": &total,
		"repos": perRepo,
	})
}

type repoStats struct {
	Repo  string        `json:"repo"`
	Stats *restic.Stats `json:"stats,omitempty"`
	Error string        `json:"error,omitempty"`
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

	results := make([]OpResult, 0, len(entries))
	for _, entry := range entries {
		err := s.restic.Unlock(r.Context(), entry.URL)
		result := OpResult{Repo: entry.URL, OK: err == nil}
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
	policy := restic.ParseRetentionPolicy(retentionStr)

	entries, err := s.reg.ListRepos(listRepoOpts(r)...)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if len(entries) == 0 {
		writeJSON(w, http.StatusOK, map[string]string{"message": "no repos to forget"})
		return
	}

	results := make([]OpResult, 0, len(entries))
	for _, entry := range entries {
		err := s.restic.Forget(r.Context(), entry.URL, policy, entry.ContainerName)
		result := OpResult{Repo: entry.URL, OK: err == nil}
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

	results := make([]OpResult, 0, len(entries))
	for _, entry := range entries {
		err := s.restic.Prune(r.Context(), entry.URL)
		result := OpResult{Repo: entry.URL, OK: err == nil}
		if err != nil {
			result.Error = err.Error()
		}
		results = append(results, result)
	}
	writeJSON(w, http.StatusOK, results)
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
