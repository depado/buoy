package api

import (
	"net/http"

	"github.com/depado/buoy/client"
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
		ctx := restic.WithPassword(r.Context(), s.resticConf.PasswordForEntry(entry.RepoName, entry.URL))
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

	var total types.Stats
	perRepo := make([]client.RepoStats, 0, len(entries))
	for _, entry := range entries {
		st, err := s.restic.Stats(restic.WithPassword(r.Context(), s.resticConf.PasswordForEntry(entry.RepoName, entry.URL)), entry.URL)
		if err != nil {
			perRepo = append(perRepo, client.RepoStats{Repo: entry.URL, Error: err.Error()})
			continue
		}
		perRepo = append(perRepo, client.RepoStats{Repo: entry.URL, Stats: st})
		total.TotalSize += st.TotalSize
		total.TotalFileCount += st.TotalFileCount
		total.TotalBlobCount += st.TotalBlobCount
		total.SnapshotsCount += st.SnapshotsCount
		total.TotalUncompressedSize += st.TotalUncompressedSize
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"total": &total,
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
		err := s.restic.Unlock(restic.WithPassword(r.Context(), s.resticConf.PasswordForEntry(entry.RepoName, entry.URL)), entry.URL)
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
		err := s.restic.Forget(restic.WithPassword(r.Context(), s.resticConf.PasswordForEntry(entry.RepoName, entry.URL)), entry.URL, policy, entry.ContainerName)
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
		err := s.restic.Prune(restic.WithPassword(r.Context(), s.resticConf.PasswordForEntry(entry.RepoName, entry.URL)), entry.URL)
		result := client.Result{Repo: entry.URL, OK: err == nil}
		if err != nil {
			result.Error = err.Error()
		}
		results = append(results, result)
	}
	writeJSON(w, http.StatusOK, results)
}
