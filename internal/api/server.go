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

	"github.com/depado/buoy/internal/config"
	"github.com/depado/buoy/internal/registry"
	"github.com/depado/buoy/internal/restic"
	"github.com/depado/buoy/internal/scheduler"
)

// API route paths. The client package imports these constants.
const (
	RouteHealth    = "/api/v1/health"
	RouteScheduled = "/api/v1/scheduled"
	RouteRepos     = "/api/v1/repos"
	RouteCheck     = "/api/v1/repos/check"
	RouteStats     = "/api/v1/repos/stats"
	RouteUnlock    = "/api/v1/repos/unlock"
	RouteForget    = "/api/v1/repos/forget"
	RoutePrune     = "/api/v1/repos/prune"
	RouteBackup    = "/api/v1/backup"
)

type Server struct {
	reg          *registry.Registry
	restic       *restic.Client
	scheduler    *scheduler.Scheduler
	resticConf   *config.ResticConf
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
		version:      version,
		logger:       logger,
		backupActive: backupActive,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET "+RouteHealth, s.handleHealth)
	mux.HandleFunc("GET "+RouteScheduled, s.handleListScheduled)
	mux.HandleFunc("GET "+RouteRepos, s.handleListRepos)
	mux.HandleFunc("GET "+RouteCheck, s.handleReposCheck)
	mux.HandleFunc("GET "+RouteStats, s.handleReposStats)
	mux.HandleFunc("POST "+RouteUnlock, s.handleReposUnlock)
	mux.HandleFunc("POST "+RouteForget, s.handleReposForget)
	mux.HandleFunc("POST "+RoutePrune, s.handleReposPrune)
	mux.HandleFunc("POST "+RouteBackup, s.handleTriggerBackup)

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
			if r.URL.Path == RouteHealth {
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
	writeJSON(w, http.StatusOK, s.scheduler.ListScheduled())
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.Warn("failed to encode JSON response", "error", err)
	}
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
