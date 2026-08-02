package api

import (
	"context"
	"net/http"

	"github.com/depado/buoy/internal/restic"
	"github.com/depado/buoy/internal/types"
)

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
	s.runRepoOp(w, r, nil, func(ctx context.Context, entry types.RepoEntry) (bool, error) {
		if readData {
			return s.restic.CheckReadData(ctx, entry.URL) == nil, nil
		}
		return s.restic.Check(ctx, entry.URL) == nil, nil
	})
}

func (s *Server) handleReposStats(w http.ResponseWriter, r *http.Request) {
	entries, err := s.reg.ListRepos(listRepoOpts(r)...)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if len(entries) == 0 {
		writeJSON(w, http.StatusOK, []types.RepoStats{})
		return
	}

	var total types.Stats
	perRepo := make([]types.RepoStats, 0, len(entries))
	for _, entry := range entries {
		st, err := s.restic.Stats(restic.WithPassword(r.Context(), s.resticConf.PasswordFor(entry.RepoName)), entry.URL)
		if err != nil {
			perRepo = append(perRepo, types.RepoStats{Repo: entry.URL, Error: err.Error()})
			continue
		}
		perRepo = append(perRepo, types.RepoStats{Repo: entry.URL, Stats: st})
		total.TotalSize += st.TotalSize
		total.TotalFileCount += st.TotalFileCount
		total.TotalBlobCount += st.TotalBlobCount
		total.SnapshotsCount += st.SnapshotsCount
		total.TotalUncompressedSize += st.TotalUncompressedSize
	}
	writeJSON(w, http.StatusOK, types.StatsResponse{Total: &total, Repos: perRepo})
}

func (s *Server) handleReposUnlock(w http.ResponseWriter, r *http.Request) {
	s.runRepoOp(w, r, s.rejectIfBusy, func(ctx context.Context, entry types.RepoEntry) (bool, error) {
		return s.restic.Unlock(ctx, entry.URL) == nil, nil
	})
}

func (s *Server) handleReposForget(w http.ResponseWriter, r *http.Request) {
	retentionStr := r.URL.Query().Get("retention")
	if retentionStr == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "retention query parameter is required"})
		return
	}
	policy := types.ParseRetentionPolicy(retentionStr)
	if policy.IsZero() {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "retention policy contains no recognized rules"})
		return
	}
	s.runRepoOp(w, r, s.rejectIfBusy, func(ctx context.Context, entry types.RepoEntry) (bool, error) {
		return s.restic.Forget(ctx, entry.URL, policy, entry.ContainerName) == nil, nil
	})
}

func (s *Server) handleReposPrune(w http.ResponseWriter, r *http.Request) {
	s.runRepoOp(w, r, s.rejectIfBusy, func(ctx context.Context, entry types.RepoEntry) (bool, error) {
		return s.restic.Prune(ctx, entry.URL) == nil, nil
	})
}

// runRepoOp lists repos matching the request options, applies the optional
// busy-guard, then runs fn against each entry. Results are collected and
// returned as a JSON array of client.Result.
func (s *Server) runRepoOp(
	w http.ResponseWriter,
	r *http.Request,
	busyGuard func(http.ResponseWriter) bool,
	fn func(context.Context, types.RepoEntry) (bool, error),
) {
	if busyGuard != nil && busyGuard(w) {
		return
	}
	entries, err := s.reg.ListRepos(listRepoOpts(r)...)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if len(entries) == 0 {
		writeJSON(w, http.StatusOK, []types.RepoResult{})
		return
	}

	results := make([]types.RepoResult, 0, len(entries))
	for _, entry := range entries {
		ctx := restic.WithPassword(r.Context(), s.resticConf.PasswordFor(entry.RepoName))
		ok, _ := fn(ctx, entry)
		result := types.RepoResult{Repo: entry.URL, OK: ok}
		if !ok {
			result.Error = "operation failed"
		}
		results = append(results, result)
	}
	writeJSON(w, http.StatusOK, results)
}
