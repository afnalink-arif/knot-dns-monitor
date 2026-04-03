package api

import (
	"bufio"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"sync"
)

var updateMu sync.Mutex

type UpdateCheckResponse struct {
	CurrentVersion  string   `json:"current_version"`
	CurrentCommit   string   `json:"current_commit"`
	LatestCommit    string   `json:"latest_commit"`
	UpdateAvailable bool     `json:"update_available"`
	CommitsBehind   int      `json:"commits_behind"`
	CommitLog       []string `json:"commit_log"`
}

func (s *Server) handleVersion(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, map[string]string{
		"version": s.cfg.Version,
	})
}

func (s *Server) handleUpdateCheck(w http.ResponseWriter, r *http.Request) {
	projectDir := s.cfg.ProjectDir

	// git fetch
	fetchCmd := exec.CommandContext(r.Context(), "git", "fetch", "origin")
	fetchCmd.Dir = projectDir
	fetchCmd.Run() // ignore errors (offline, etc)

	resp := UpdateCheckResponse{
		CurrentVersion: s.cfg.Version,
		CommitLog:      []string{},
	}

	// current commit
	if out, err := gitOutput(r, projectDir, "rev-parse", "--short", "HEAD"); err == nil {
		resp.CurrentCommit = out
	}

	// latest commit on remote
	if out, err := gitOutput(r, projectDir, "rev-parse", "--short", "origin/main"); err == nil {
		resp.LatestCommit = out
	}

	// commits behind
	if out, err := gitOutput(r, projectDir, "rev-list", "--count", "HEAD..origin/main"); err == nil {
		fmt.Sscanf(out, "%d", &resp.CommitsBehind)
	}

	resp.UpdateAvailable = resp.CommitsBehind > 0

	// commit log
	if resp.UpdateAvailable {
		if out, err := gitOutput(r, projectDir, "log", "--oneline", "HEAD..origin/main"); err == nil && out != "" {
			resp.CommitLog = strings.Split(out, "\n")
		}
	}

	writeJSON(w, resp)
}

func (s *Server) handleUpdateExecute(w http.ResponseWriter, r *http.Request) {
	if !updateMu.TryLock() {
		http.Error(w, `{"error":"update already in progress"}`, http.StatusConflict)
		return
	}
	defer updateMu.Unlock()

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, `{"error":"streaming not supported"}`, http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	projectDir := s.cfg.ProjectDir
	cmd := exec.Command("/bin/bash", projectDir+"/update.sh")
	cmd.Dir = projectDir
	cmd.Env = append(os.Environ(), "NONINTERACTIVE=1", "TERM=dumb")

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		fmt.Fprintf(w, "event: error\ndata: failed to create pipe: %s\n\n", err)
		flusher.Flush()
		return
	}
	cmd.Stderr = cmd.Stdout

	if err := cmd.Start(); err != nil {
		fmt.Fprintf(w, "event: error\ndata: failed to start update: %s\n\n", err)
		flusher.Flush()
		return
	}

	scanner := bufio.NewScanner(stdout)
	for scanner.Scan() {
		line := stripANSI(scanner.Text())
		fmt.Fprintf(w, "data: %s\n\n", line)
		flusher.Flush()
	}

	if err := cmd.Wait(); err != nil {
		fmt.Fprintf(w, "event: error\ndata: update failed: %s\n\n", err)
	} else {
		fmt.Fprintf(w, "event: done\ndata: Update complete\n\n")
	}
	flusher.Flush()
}

func (s *Server) handleUpdateStatus(w http.ResponseWriter, r *http.Request) {
	locked := updateMu.TryLock()
	if locked {
		updateMu.Unlock()
	}
	writeJSON(w, map[string]bool{
		"in_progress": !locked,
	})
}

func gitOutput(r *http.Request, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(r.Context(), "git", args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func stripANSI(s string) string {
	var result strings.Builder
	i := 0
	for i < len(s) {
		if s[i] == '\033' {
			// skip ESC sequence
			i++
			if i < len(s) && s[i] == '[' {
				i++
				for i < len(s) && !((s[i] >= 'A' && s[i] <= 'Z') || (s[i] >= 'a' && s[i] <= 'z')) {
					i++
				}
				if i < len(s) {
					i++
				}
			}
		} else {
			result.WriteByte(s[i])
			i++
		}
	}
	return result.String()
}
