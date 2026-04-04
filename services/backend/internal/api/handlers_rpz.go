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
	"path/filepath"
	"strings"
	"time"
)

type RPZConfig struct {
	Enabled        bool       `json:"enabled"`
	MasterServers  string     `json:"master_servers"`
	ZoneName       string     `json:"zone_name"`
	LastSync       *time.Time `json:"last_sync"`
	LastSyncStatus string     `json:"last_sync_status"`
	LastSyncError  string     `json:"last_sync_error"`
	DomainCount    int        `json:"domain_count"`
	FileSizeBytes  int64      `json:"file_size_bytes"`
	SyncDurationMs int        `json:"sync_duration_ms"`
}

func (s *Server) getRPZConfig() RPZConfig {
	cfg := RPZConfig{
		MasterServers: "139.255.196.202,182.23.79.202,103.154.123.130",
		ZoneName:      "trustpositifkominfo",
	}
	ctx := context.Background()
	s.pg.QueryRow(ctx,
		`SELECT enabled, master_servers, zone_name, last_sync, last_sync_status, last_sync_error,
		        domain_count, file_size_bytes, sync_duration_ms
		 FROM rpz_config WHERE id = 1`,
	).Scan(&cfg.Enabled, &cfg.MasterServers, &cfg.ZoneName, &cfg.LastSync, &cfg.LastSyncStatus,
		&cfg.LastSyncError, &cfg.DomainCount, &cfg.FileSizeBytes, &cfg.SyncDurationMs)
	return cfg
}

func (s *Server) handleGetRPZConfig(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, s.getRPZConfig())
}

func (s *Server) handleUpdateRPZConfig(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Enabled       *bool   `json:"enabled"`
		MasterServers *string `json:"master_servers"`
		ZoneName      *string `json:"zone_name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid request"}`, http.StatusBadRequest)
		return
	}

	ctx := r.Context()
	s.pg.Exec(ctx, `INSERT INTO rpz_config (id) VALUES (1) ON CONFLICT DO NOTHING`)

	if req.Enabled != nil {
		s.pg.Exec(ctx, "UPDATE rpz_config SET enabled = $1, updated_at = NOW() WHERE id = 1", *req.Enabled)
	}
	if req.MasterServers != nil {
		s.pg.Exec(ctx, "UPDATE rpz_config SET master_servers = $1, updated_at = NOW() WHERE id = 1", *req.MasterServers)
	}
	if req.ZoneName != nil {
		s.pg.Exec(ctx, "UPDATE rpz_config SET zone_name = $1, updated_at = NOW() WHERE id = 1", *req.ZoneName)
	}

	// Regenerate kresd config to reflect enable/disable change
	rpzCfg := s.getRPZConfig()
	s.regenerateKresdConfig(rpzCfg.Enabled)
	if name := findContainerName("kresd"); name != "" {
		exec.Command("docker", "restart", name).Run()
	}

	writeJSON(w, map[string]string{"message": "RPZ config updated"})
}

// handleRPZSync triggers an AXFR sync from the master servers.
// The zone file is saved directly for kresd's native policy.rpz() to load.
// This avoids converting 500K+ domains to local-data YAML records.
func (s *Server) handleRPZSync(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, `{"error":"streaming not supported"}`, http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")

	sendEvent := func(msg string) {
		fmt.Fprintf(w, "data: %s\n\n", msg)
		flusher.Flush()
	}

	cfg := s.getRPZConfig()
	masters := strings.Split(cfg.MasterServers, ",")
	if len(masters) == 0 {
		sendEvent("[ERROR] No master servers configured")
		fmt.Fprintf(w, "event: error\ndata: no masters\n\n")
		flusher.Flush()
		return
	}

	sendEvent("[INFO] Starting RPZ zone transfer...")
	startTime := time.Now()

	// Try each master server — stream dig output to file to avoid holding all in memory
	rpzDir := filepath.Join(s.cfg.ProjectDir, "config", "kresd")
	rpzFile := filepath.Join(rpzDir, "rpz.zone")
	tmpFile := rpzFile + ".tmp"

	var usedMaster string
	var axfrErr error

	for _, master := range masters {
		master = strings.TrimSpace(master)
		if master == "" {
			continue
		}
		sendEvent(fmt.Sprintf("[INFO] Trying AXFR from %s...", master))

		// Stream AXFR directly to file to avoid memory spike
		// Zone file is ~1.3GB so use a long timeout (30 min) and +tcp for large transfer
		axfrCtx, axfrCancel := context.WithTimeout(context.Background(), 30*time.Minute)
		cmd := exec.CommandContext(axfrCtx,
			"sh", "-c", fmt.Sprintf(
				"dig AXFR @%s %s +noidnout +tcp +time=300 +tries=2 +nocomments +nostats +nocmd > %s 2>/dev/null",
				master, cfg.ZoneName, tmpFile))

		// Monitor file size during download (progress updates)
		done := make(chan error, 1)
		go func() { done <- cmd.Run() }()

		progressTicker := time.NewTicker(5 * time.Second)
		defer progressTicker.Stop()
	axfrLoop:
		for {
			select {
			case axfrErr = <-done:
				break axfrLoop
			case <-progressTicker.C:
				if info, err := os.Stat(tmpFile); err == nil {
					sizeMB := float64(info.Size()) / 1024 / 1024
					elapsed := time.Since(startTime).Seconds()
					sendEvent(fmt.Sprintf("[INFO] Downloading... %.1f MB (%.0fs)", sizeMB, elapsed))
				}
			}
		}
		axfrCancel()

		// Verify we got real zone data (not just SOA/NS from unregistered IP)
		if info, err := os.Stat(tmpFile); err == nil && axfrErr == nil {
			fileSize := info.Size()
			if head, err := readFileHead(tmpFile, 1024); err == nil && strings.Contains(head, cfg.ZoneName) {
				// Check if we got meaningful data — just SOA+NS is typically < 1KB
				if fileSize > 10*1024 { // > 10KB = likely real data
					usedMaster = master
					sendEvent(fmt.Sprintf("[OK] Got %.1f MB from %s", float64(fileSize)/1024/1024, master))
					break
				}
				// Small response = probably unregistered IP, only got SOA/NS
				sendEvent(fmt.Sprintf("[WARN] %s returned only %d bytes (possibly IP not registered at Komdigi)", master, fileSize))
			} else if fileSize == 0 {
				sendEvent(fmt.Sprintf("[WARN] %s returned empty response", master))
			} else {
				sendEvent(fmt.Sprintf("[WARN] %s returned invalid zone data", master))
			}
		} else {
			sendEvent(fmt.Sprintf("[WARN] Failed from %s: %v", master, axfrErr))
		}
	}

	if usedMaster == "" {
		errMsg := "Semua master server gagal memberikan zone data. Kemungkinan IP server ini belum terdaftar di Komdigi. Daftar di: https://s.komdigi.go.id/FormKoneksiRPZ"
		if axfrErr != nil {
			errMsg = fmt.Sprintf("Zone transfer gagal: %v — Pastikan IP server sudah terdaftar di s.komdigi.go.id/FormKoneksiRPZ", axfrErr)
		}
		sendEvent(fmt.Sprintf("[ERROR] %s", errMsg))
		os.Remove(tmpFile)
		s.updateRPZSyncStatus("error", errMsg, 0, 0, 0)
		fmt.Fprintf(w, "event: error\ndata: %s\n\n", errMsg)
		flusher.Flush()
		return
	}

	sendEvent(fmt.Sprintf("[OK] Zone transfer from %s successful", usedMaster))

	// Count domains by scanning the file (streaming, not loading all into memory)
	sendEvent("[INFO] Counting domains in zone file...")
	domainCount := countRPZDomains(tmpFile, cfg.ZoneName)
	sendEvent(fmt.Sprintf("[OK] Found %d blocked domains", domainCount))

	// Atomic rename: tmp → final
	if err := os.Rename(tmpFile, rpzFile); err != nil {
		// Fallback: copy
		data, _ := os.ReadFile(tmpFile)
		os.WriteFile(rpzFile, data, 0644)
		os.Remove(tmpFile)
	}

	duration := time.Since(startTime)
	fileInfo, _ := os.Stat(rpzFile)
	fileSize := int64(0)
	if fileInfo != nil {
		fileSize = fileInfo.Size()
	}

	s.updateRPZSyncStatus("success", "", domainCount, fileSize, int(duration.Milliseconds()))

	sendEvent(fmt.Sprintf("[OK] Sync complete: %d domains, %.1f MB, %dms",
		domainCount, float64(fileSize)/1024/1024, duration.Milliseconds()))

	// If RPZ is enabled, regenerate kresd config (adds policy.rpz Lua) and restart
	if cfg.Enabled {
		sendEvent("[INFO] Applying to DNS resolver via native RPZ policy...")
		s.regenerateKresdConfig(true)
		if name := findContainerName("kresd"); name != "" {
			exec.Command("docker", "restart", name).Run()
		}
		sendEvent("[OK] DNS resolver restarted — kresd sedang memuat RPZ zone ke trie...")
		sendEvent(fmt.Sprintf("[INFO] Proses loading %d domain ke memori bisa memakan 1-5 menit. Pantau Kresd Memory di dashboard.", domainCount))
	} else {
		sendEvent("[INFO] RPZ is disabled — zone file saved but not applied to resolver")
	}

	fmt.Fprintf(w, "event: done\ndata: sync complete\n\n")
	flusher.Flush()
}

func (s *Server) handleRPZStats(w http.ResponseWriter, r *http.Request) {
	cfg := s.getRPZConfig()

	rpzFile := filepath.Join(s.cfg.ProjectDir, "config", "kresd", "rpz.zone")
	fileExists := false
	if _, err := os.Stat(rpzFile); err == nil {
		fileExists = true
	}

	// Get kresd memory usage
	kresdMem := getKresdMemoryMB()

	writeJSON(w, map[string]interface{}{
		"config":         cfg,
		"file_exists":    fileExists,
		"kresd_memory_mb": kresdMem,
	})
}

func (s *Server) updateRPZSyncStatus(status, errMsg string, domainCount int, fileSize int64, durationMs int) {
	ctx := context.Background()
	s.pg.Exec(ctx, `INSERT INTO rpz_config (id) VALUES (1) ON CONFLICT DO NOTHING`)
	s.pg.Exec(ctx,
		`UPDATE rpz_config SET last_sync = NOW(), last_sync_status = $1, last_sync_error = $2,
		 domain_count = $3, file_size_bytes = $4, sync_duration_ms = $5, updated_at = NOW() WHERE id = 1`,
		status, errMsg, domainCount, fileSize, durationMs)
}

// regenerateKresdConfig rebuilds kresd YAML config.
// Custom filter rules use local-data (small count).
// RPZ uses native policy.rpz() Lua — kresd loads zone file with optimized trie, no YAML bloat.
func (s *Server) regenerateKresdConfig(includeRPZ bool) {
	projectDir := s.cfg.ProjectDir
	templatePath := filepath.Join(projectDir, "config/kresd/config.yaml.template")
	configPath := filepath.Join(projectDir, "config/kresd/config.yaml")

	templateData, err := os.ReadFile(templatePath)
	if err != nil {
		log.Printf("Failed to read kresd template: %v", err)
		return
	}

	envVars := loadEnvFile(filepath.Join(projectDir, ".env"))
	serverIP := envVars["SERVER_IP"]
	if serverIP == "" {
		serverIP = "0.0.0.0"
	}
	cacheSize := envVars["CACHE_SIZE"]
	if cacheSize == "" {
		cacheSize = "8G"
	}

	// Build subnet views
	var subnetViews strings.Builder
	if subnets := envVars["ALLOWED_SUBNETS"]; subnets != "" {
		for _, subnet := range strings.Split(subnets, ",") {
			subnet = strings.TrimSpace(subnet)
			if subnet != "" {
				subnetViews.WriteString(fmt.Sprintf("  - subnets: ['%s']\n    answer: allow\n", subnet))
			}
		}
	}

	// Build local-data for CUSTOM filter rules only (small count, OK to inline)
	var localData strings.Builder
	ctx := context.Background()
	rows, err := s.pg.Query(ctx,
		"SELECT domain FROM filter_rules WHERE enabled = true AND action = 'block' ORDER BY domain")
	customCount := 0
	if err == nil {
		defer rows.Close()
		var domains []string
		for rows.Next() {
			var d string
			rows.Scan(&d)
			domains = append(domains, d)
		}
		if len(domains) > 0 {
			localData.WriteString("local-data:\n")
			localData.WriteString("  records:\n")
			for _, d := range domains {
				localData.WriteString(fmt.Sprintf("    - owner: %s.\n      ttl: 60\n      rdata: '%s'\n", d, serverIP))
				if !strings.HasPrefix(d, "www.") {
					localData.WriteString(fmt.Sprintf("    - owner: www.%s.\n      ttl: 60\n      rdata: '%s'\n", d, serverIP))
				}
			}
			customCount = len(domains)
		}
	}

	// Build RPZ Lua snippet — uses kresd's native policy.rpz()
	// This loads the zone file into kresd's internal trie — ~10x more memory efficient
	// than converting 500K domains to local-data YAML records
	rpzLua := "-- RPZ disabled"
	if includeRPZ {
		rpzFile := "/etc/knot-resolver/rpz.zone"
		// Check if zone file has content
		localRpzFile := filepath.Join(projectDir, "config/kresd/rpz.zone")
		if info, err := os.Stat(localRpzFile); err == nil && info.Size() > 100 {
			rpzLua = fmt.Sprintf(
				`-- RPZ Trust Positif Komdigi (native kresd policy)
    -- policy.rpz loads zone file into optimized trie data structure
    -- Auto file-watch: kresd reloads when file changes (no restart needed)
    policy.add(policy.rpz(policy.DENY_MSG('Diblokir oleh DNS Filter - Komdigi Trust Positif'), '%s', true))`,
				rpzFile)
		} else {
			rpzLua = "-- RPZ enabled but zone file empty/missing — run sync first"
		}
	}

	log.Printf("Regenerating kresd config: %d custom domains, RPZ native=%v", customCount, includeRPZ)

	config := string(templateData)
	config = strings.ReplaceAll(config, "__CACHE_SIZE__", cacheSize)
	config = strings.ReplaceAll(config, "__SUBNET_VIEWS__", subnetViews.String())
	config = strings.ReplaceAll(config, "__LOCAL_DATA__", localData.String())
	config = strings.ReplaceAll(config, "__RPZ_LUA__", rpzLua)

	os.WriteFile(configPath, []byte(config), 0644)
}

// countRPZDomains counts unique blocked domains by scanning the zone file line by line.
// Streaming approach — doesn't load entire file into memory.
//
// RPZ zone format: records have the zone name as suffix, e.g.:
//   badsite.com.trustpositifkominfo.  IN  CNAME  .
//   *.badsite.com.trustpositifkominfo.  IN  CNAME  .
// We strip the zone suffix to get the actual domain.
func countRPZDomains(path, zoneName string) int {
	f, err := os.Open(path)
	if err != nil {
		return 0
	}
	defer f.Close()

	count := 0
	zoneSuffix := "." + strings.ToLower(zoneName)
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 64*1024), 64*1024)

	for scanner.Scan() {
		line := scanner.Text()
		if len(line) == 0 || line[0] == ';' || line[0] == '$' {
			continue
		}

		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}

		owner := strings.ToLower(fields[0])
		owner = strings.TrimSuffix(owner, ".")

		// Skip rpz-passthru (whitelisted)
		lineUpper := strings.ToUpper(line)
		if strings.Contains(lineUpper, "RPZ-PASSTHRU") {
			continue
		}

		// Extract domain by stripping RPZ zone suffix
		// e.g. "badsite.com.trustpositifkominfo" → "badsite.com"
		domain := owner
		if idx := strings.Index(owner, zoneSuffix); idx > 0 {
			domain = owner[:idx]
		} else if owner == strings.ToLower(zoneName) || owner == "@" {
			// Skip SOA/NS records for the zone apex itself
			continue
		}

		// Skip wildcard prefix for counting (*.domain counts as same domain)
		domain = strings.TrimPrefix(domain, "*.")

		if domain != "" && strings.Contains(domain, ".") {
			count++
		}
	}

	return count
}

// readFileHead reads first n bytes of a file
func readFileHead(path string, n int) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	buf := make([]byte, n)
	nr, err := f.Read(buf)
	if nr > 0 {
		return string(buf[:nr]), nil
	}
	return "", err
}

// getKresdMemoryMB returns kresd container memory usage in MB
func getKresdMemoryMB() float64 {
	name := findContainerName("kresd")
	if name == "" {
		return 0
	}
	out, err := exec.Command("docker", "stats", name, "--no-stream", "--format", "{{.MemUsage}}").Output()
	if err != nil {
		return 0
	}
	// Format: "123.4MiB / 1.5GiB"
	s := strings.TrimSpace(string(out))
	parts := strings.Split(s, "/")
	if len(parts) == 0 {
		return 0
	}
	mem := strings.TrimSpace(parts[0])
	mem = strings.ReplaceAll(mem, " ", "")
	if strings.HasSuffix(mem, "GiB") {
		mem = strings.TrimSuffix(mem, "GiB")
		var v float64
		fmt.Sscanf(mem, "%f", &v)
		return v * 1024
	}
	if strings.HasSuffix(mem, "MiB") {
		mem = strings.TrimSuffix(mem, "MiB")
		var v float64
		fmt.Sscanf(mem, "%f", &v)
		return v
	}
	return 0
}
