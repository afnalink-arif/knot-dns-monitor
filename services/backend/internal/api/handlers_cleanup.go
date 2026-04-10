package api

import (
	"fmt"
	"net/http"
	"os/exec"
	"strconv"
	"strings"
)

type CleanupCategory struct {
	Type        string `json:"type"`
	Count       int    `json:"count"`
	Size        string `json:"size"`
	Reclaimable string `json:"reclaimable"`
}

type CleanupInfo struct {
	Categories       []CleanupCategory `json:"categories"`
	TotalReclaimable string            `json:"total_reclaimable"`
}

func (s *Server) handleGetCleanupInfo(w http.ResponseWriter, r *http.Request) {
	info := CleanupInfo{}

	out, err := exec.CommandContext(r.Context(), "docker", "system", "df", "--format", "{{.Type}}\t{{.TotalCount}}\t{{.Size}}\t{{.Reclaimable}}").Output()
	if err == nil {
		var reclaimableSizes []string
		for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
			fields := strings.Split(line, "\t")
			if len(fields) < 4 {
				continue
			}
			count, _ := strconv.Atoi(fields[1])
			cat := CleanupCategory{
				Type:        fields[0],
				Count:       count,
				Size:        fields[2],
				Reclaimable: fields[3],
			}
			info.Categories = append(info.Categories, cat)
			reclaimableSizes = append(reclaimableSizes, fields[3])
		}
		info.TotalReclaimable = sumDockerSizes(reclaimableSizes)
	}

	writeJSON(w, info)
}

func (s *Server) handleRunCleanup(w http.ResponseWriter, r *http.Request) {
	// Get reclaimable before cleanup
	beforeInfo := s.getDockerReclaimable(r)

	results := []string{}

	// Prune all unused images (not just dangling)
	out, err := exec.CommandContext(r.Context(), "docker", "image", "prune", "-af").CombinedOutput()
	if err == nil {
		for _, line := range strings.Split(string(out), "\n") {
			if strings.HasPrefix(line, "Total reclaimed space:") {
				results = append(results, "Images pruned: "+strings.TrimPrefix(line, "Total reclaimed space: "))
			}
		}
	}

	// Prune all build cache
	out, err = exec.CommandContext(r.Context(), "docker", "builder", "prune", "-af").CombinedOutput()
	if err == nil {
		for _, line := range strings.Split(string(out), "\n") {
			if strings.HasPrefix(line, "Total:") {
				results = append(results, "Build cache pruned: "+strings.TrimSpace(strings.TrimPrefix(line, "Total:")))
			}
		}
	}

	// Prune stopped containers
	out, err = exec.CommandContext(r.Context(), "docker", "container", "prune", "-f").CombinedOutput()
	if err == nil {
		for _, line := range strings.Split(string(out), "\n") {
			if strings.HasPrefix(line, "Total reclaimed space:") {
				results = append(results, "Containers pruned: "+strings.TrimPrefix(line, "Total reclaimed space: "))
			}
		}
	}

	// Get reclaimable after cleanup
	afterInfo := s.getDockerReclaimable(r)

	writeJSON(w, map[string]interface{}{
		"message":          "Cleanup complete",
		"details":          results,
		"before_reclaimable": beforeInfo,
		"after_reclaimable":  afterInfo,
	})
}

func (s *Server) getDockerReclaimable(r *http.Request) string {
	out, err := exec.CommandContext(r.Context(), "docker", "system", "df", "--format", "{{.Reclaimable}}").Output()
	if err != nil {
		return "0 B"
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	return sumDockerSizes(lines)
}

// sumDockerSizes sums human-readable sizes like "1.2GB", "345MB", "12.5kB"
func sumDockerSizes(lines []string) string {
	totalBytes := float64(0)
	for _, s := range lines {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		// Strip parenthesized percentage like "(100%)"
		if idx := strings.Index(s, " ("); idx > 0 {
			s = s[:idx]
		}
		val, unit := parseDockerSize(s)
		totalBytes += val * unit
	}
	return formatBytes(totalBytes)
}

func parseDockerSize(s string) (float64, float64) {
	s = strings.TrimSpace(s)
	multipliers := []struct {
		suffix string
		mult   float64
	}{
		{"TB", 1e12}, {"GB", 1e9}, {"MB", 1e6}, {"kB", 1e3}, {"B", 1},
	}
	for _, m := range multipliers {
		if strings.HasSuffix(s, m.suffix) {
			numStr := strings.TrimSuffix(s, m.suffix)
			val, err := strconv.ParseFloat(numStr, 64)
			if err == nil {
				return val, m.mult
			}
		}
	}
	return 0, 1
}

func formatBytes(b float64) string {
	if b >= 1e9 {
		return fmt.Sprintf("%.1f GB", b/1e9)
	}
	if b >= 1e6 {
		return fmt.Sprintf("%.1f MB", b/1e6)
	}
	if b >= 1e3 {
		return fmt.Sprintf("%.1f KB", b/1e3)
	}
	return fmt.Sprintf("%.0f B", b)
}
