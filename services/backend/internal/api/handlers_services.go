package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os/exec"
	"strings"
)

var managedServices = []string{
	"kresd", "dnstap-ingester", "prometheus", "node-exporter",
	"clickhouse", "redis", "postgres", "frontend", "caddy", "backend",
}

type ServiceStatus struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	Health string `json:"health,omitempty"`
}

func (s *Server) handleListServices(w http.ResponseWriter, r *http.Request) {
	args := s.composeArgs()
	services := []ServiceStatus{}

	for _, svc := range managedServices {
		st := ServiceStatus{Name: svc, Status: "unknown"}

		cmdArgs := append(args, "ps", "--format", "json", svc)
		out, err := exec.CommandContext(r.Context(), "docker", cmdArgs...).Output()
		if err == nil && len(out) > 0 {
			var info struct {
				State  string `json:"State"`
				Health string `json:"Health"`
			}
			if json.Unmarshal(out, &info) == nil {
				st.Status = info.State
				st.Health = info.Health
			}
		}

		services = append(services, st)
	}

	writeJSON(w, services)
}

func (s *Server) handleRestartService(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Service string `json:"service"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Service == "" {
		http.Error(w, `{"error":"service name required"}`, http.StatusBadRequest)
		return
	}

	// Validate service name
	valid := false
	for _, svc := range managedServices {
		if svc == req.Service {
			valid = true
			break
		}
	}
	if !valid {
		http.Error(w, `{"error":"invalid service name"}`, http.StatusBadRequest)
		return
	}

	args := s.composeArgs()

	// For backend, restart in background since it kills itself
	if req.Service == "backend" {
		go func() {
			cmdArgs := append(args, "restart", "backend")
			exec.Command("docker", cmdArgs...).Run()
		}()
		writeJSON(w, map[string]string{"message": "backend restarting"})
		return
	}

	cmdArgs := append(args, "restart", req.Service)
	out, err := exec.CommandContext(r.Context(), "docker", cmdArgs...).CombinedOutput()
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"%s","output":"%s"}`,
			err.Error(), strings.TrimSpace(string(out))), http.StatusInternalServerError)
		return
	}

	writeJSON(w, map[string]string{"message": req.Service + " restarted"})
}

func (s *Server) handleRestartAll(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, `{"error":"streaming not supported"}`, http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")

	args := s.composeArgs()

	// Restart order (same as update.sh)
	groups := []struct {
		label    string
		services []string
	}{
		{"Infrastructure", []string{"clickhouse", "redis", "postgres"}},
		{"DNS pipeline", []string{"dnstap-ingester", "kresd"}},
		{"Monitoring", []string{"prometheus", "node-exporter"}},
		{"Frontend", []string{"frontend", "caddy"}},
	}

	for _, g := range groups {
		fmt.Fprintf(w, "data: Restarting %s...\n\n", g.label)
		flusher.Flush()

		for _, svc := range g.services {
			cmdArgs := append(args, "restart", svc)
			out, err := exec.CommandContext(r.Context(), "docker", cmdArgs...).CombinedOutput()
			if err != nil {
				fmt.Fprintf(w, "data: [ERROR] %s: %s\n\n", svc, strings.TrimSpace(string(out)))
			} else {
				fmt.Fprintf(w, "data: [OK] %s restarted\n\n", svc)
			}
			flusher.Flush()
		}
	}

	// Restart backend last (kills this container)
	fmt.Fprintf(w, "data: Restarting backend...\n\n")
	flusher.Flush()

	go func() {
		cmdArgs := append(args, "restart", "backend")
		exec.Command("docker", cmdArgs...).Run()
	}()

	fmt.Fprintf(w, "event: done\ndata: All services restarted\n\n")
	flusher.Flush()
}

// composeArgs returns docker compose args that work from inside a container.
// For restart/ps operations, we use the container-accessible /project path
// since these don't create new bind mounts (unlike build/up).
func (s *Server) composeArgs() []string {
	return []string{"compose", "-f", s.cfg.ProjectDir + "/docker-compose.yml"}
}
