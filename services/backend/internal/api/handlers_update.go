package api

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
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
		// Clear cached update check so overview reflects up-to-date state
		s.localUpdateCache.Store(json.RawMessage(`{"update_available":false,"commits_behind":0,"commit_log":[]}`))
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

// --- Auto-update scheduler ---

type AutoUpdateConfig struct {
	Enabled    bool       `json:"auto_update_enabled"`
	Hour       int        `json:"auto_update_hour"`
	Day        int        `json:"auto_update_day"` // 0=daily, 1=Mon, 2=Tue, ..., 7=Sun
	LastUpdate *time.Time `json:"last_auto_update"`
}

func (s *Server) getAutoUpdateConfig() AutoUpdateConfig {
	cfg := AutoUpdateConfig{Hour: 3}
	s.pg.QueryRow(context.Background(),
		`SELECT auto_update_enabled, auto_update_hour, auto_update_day, last_auto_update
		 FROM server_config WHERE id = 1`,
	).Scan(&cfg.Enabled, &cfg.Hour, &cfg.Day, &cfg.LastUpdate)
	return cfg
}

func (s *Server) handleGetAutoUpdateConfig(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, s.getAutoUpdateConfig())
}

func (s *Server) handleUpdateAutoUpdateConfig(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Enabled *bool `json:"auto_update_enabled"`
		Hour    *int  `json:"auto_update_hour"`
		Day     *int  `json:"auto_update_day"`
	}
	if err := decodeJSON(r, &req); err != nil {
		http.Error(w, `{"error":"invalid request"}`, http.StatusBadRequest)
		return
	}

	ctx := r.Context()
	if req.Enabled != nil {
		s.pg.Exec(ctx, "UPDATE server_config SET auto_update_enabled = $1, updated_at = NOW() WHERE id = 1", *req.Enabled)
	}
	if req.Hour != nil {
		h := *req.Hour
		if h < 0 { h = 0 }
		if h > 23 { h = 23 }
		s.pg.Exec(ctx, "UPDATE server_config SET auto_update_hour = $1, updated_at = NOW() WHERE id = 1", h)
	}
	if req.Day != nil {
		d := *req.Day
		if d < 0 { d = 0 }
		if d > 7 { d = 7 }
		s.pg.Exec(ctx, "UPDATE server_config SET auto_update_day = $1, updated_at = NOW() WHERE id = 1", d)
	}

	writeJSON(w, map[string]string{"message": "auto-update config updated"})
}

// runAutoUpdate checks every minute if an auto-update is due.
// Uses maintenanceMu to avoid running simultaneously with RPZ sync.
func (s *Server) runAutoUpdate(ctx context.Context) {
	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()

	log.Println("Auto-update scheduler started")
	for {
		select {
		case <-ctx.Done():
			log.Println("Auto-update scheduler stopped")
			return
		case <-ticker.C:
			cfg := s.getAutoUpdateConfig()
			if !cfg.Enabled {
				continue
			}

			// Check if already updated today/this week
			if cfg.LastUpdate != nil {
				minInterval := 23 * time.Hour // at least 23h between updates
				if cfg.Day > 0 {
					minInterval = 6 * 24 * time.Hour // at least 6 days for weekly
				}
				if time.Since(*cfg.LastUpdate) < minInterval {
					continue
				}
			}

			// Check time (in server timezone)
			tz := s.getServerTimezone()
			loc, err := time.LoadLocation(tz)
			if err != nil {
				loc = time.FixedZone("WIB", 7*60*60)
			}
			nowLocal := time.Now().In(loc)

			if nowLocal.Hour() != cfg.Hour {
				continue
			}

			// Check day of week (0=daily, 1=Mon...7=Sun)
			if cfg.Day > 0 {
				wday := int(nowLocal.Weekday()) // 0=Sun, 1=Mon...6=Sat
				if wday == 0 { wday = 7 }       // remap Sun to 7
				if wday != cfg.Day {
					continue
				}
			}

			// Acquire maintenance lock
			if !s.maintenanceMu.TryLock() {
				log.Println("Auto-update: skipped, maintenance lock held (RPZ sync in progress?)")
				continue
			}

			log.Printf("Auto-update triggered (%02d:00 %s)", cfg.Hour, tz)
			s.doAutoUpdate()
			s.maintenanceMu.Unlock()
		}
	}
}

func (s *Server) doAutoUpdate() {
	projectDir := s.cfg.ProjectDir
	cmd := exec.Command("/bin/bash", projectDir+"/update.sh")
	cmd.Dir = projectDir
	cmd.Env = append(os.Environ(), "NONINTERACTIVE=1", "TERM=dumb")

	output, err := cmd.CombinedOutput()
	if err != nil {
		log.Printf("Auto-update failed: %v\n%s", err, string(output))
	} else {
		log.Printf("Auto-update completed successfully")
	}

	// Record timestamp
	s.pg.Exec(context.Background(),
		"UPDATE server_config SET last_auto_update = NOW(), updated_at = NOW() WHERE id = 1")
}

func decodeJSON(r *http.Request, v interface{}) error {
	return json.NewDecoder(r.Body).Decode(v)
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
