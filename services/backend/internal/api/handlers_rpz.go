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
	"regexp"
	"strings"
	"time"
)

// validRPZLabel matches a single valid DNS label for libknot:
// starts and ends with alphanumeric, contains only alphanumeric and hyphens
var validRPZLabel = regexp.MustCompile(`^[a-zA-Z0-9]([a-zA-Z0-9-]*[a-zA-Z0-9])?$`)

// isValidRPZOwner validates an RPZ owner name for strict libknot compatibility.
// Each label must start/end with alphanumeric and contain only alphanumeric + hyphens.
// Underscores, special chars, and labels starting/ending with hyphens are rejected.
func isValidRPZOwner(owner string) bool {
	name := strings.TrimSuffix(owner, ".")
	if name == "" {
		return false
	}
	// Handle wildcard
	name = strings.TrimPrefix(name, "*.")
	labels := strings.Split(name, ".")
	if len(labels) < 2 { // need at least 2 labels (domain + zone suffix)
		return false
	}
	for _, l := range labels {
		if l == "" || !validRPZLabel.MatchString(l) {
			return false
		}
	}
	return true
}

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

	// Cleanup stale temp files from previous failed syncs
	os.Remove(tmpFile)
	os.Remove(tmpFile + ".converted")

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

	// Convert CNAME targets to "." for kresd compatibility
	// Komdigi uses "CNAME lamanlabuh.aduankonten.id." but kresd only supports "CNAME ."
	sendEvent("[INFO] Converting zone for kresd compatibility...")
	convertedFile := tmpFile + ".converted"
	result, convertErr := convertRPZForKresd(tmpFile, convertedFile)
	if convertErr != nil {
		sendEvent(fmt.Sprintf("[WARN] Conversion error: %v — using raw zone", convertErr))
		convertedFile = tmpFile
	} else {
		sendEvent(fmt.Sprintf("[OK] Converted %d CNAME records to NXDOMAIN format", result.converted))
		if result.skipped > 0 {
			sendEvent(fmt.Sprintf("[INFO] Skipped %d entries with invalid domain names (non-ASCII, special chars)", result.skipped))
		}
		os.Remove(tmpFile)
		tmpFile = convertedFile
	}

	// Count domains by scanning the file (streaming, not loading all into memory)
	sendEvent("[INFO] Counting domains in zone file...")
	domainCount := countRPZDomains(tmpFile, cfg.ZoneName)
	sendEvent(fmt.Sprintf("[OK] Found %d blocked domains", domainCount))

	// Safety check: verify converted file has real content before replacing
	tmpInfo, err := os.Stat(tmpFile)
	if err != nil || tmpInfo.Size() < 1024 {
		errMsg := fmt.Sprintf("Converted zone file too small (%d bytes) — aborting to protect existing zone", tmpInfo.Size())
		sendEvent(fmt.Sprintf("[ERROR] %s", errMsg))
		os.Remove(tmpFile)
		s.updateRPZSyncStatus("error", errMsg, 0, 0, int(time.Since(startTime).Milliseconds()))
		fmt.Fprintf(w, "event: error\ndata: %s\n\n", errMsg)
		flusher.Flush()
		return
	}

	// Atomic rename: tmp → final
	if err := os.Rename(tmpFile, rpzFile); err != nil {
		data, _ := os.ReadFile(tmpFile)
		if len(data) > 1024 { // only overwrite if we have real data
			os.WriteFile(rpzFile, data, 0644)
		}
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

	// If RPZ is enabled, regenerate kresd config (adds local-data.rpz YAML) and restart
	if cfg.Enabled {
		sendEvent("[INFO] Applying to DNS resolver via native RPZ (shared ruledb)...")
		s.regenerateKresdConfig(true)
		if name := findContainerName("kresd"); name != "" {
			exec.Command("docker", "restart", name).Run()
		}
		sendEvent("[OK] DNS resolver restarted — policy-loader sedang memuat RPZ zone ke ruledb...")
		sendEvent(fmt.Sprintf("[INFO] Loading %d domain ke shared LMDB database. Proses ini berjalan di background.", domainCount))
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
// Custom filter rules use local-data records (small count, inlined).
// RPZ uses native local-data.rpz YAML — loaded once by policy-loader into shared LMDB ruledb,
// then all workers read from the shared DB. No per-worker loading, no 60s timeout issue.
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

	// With native YAML RPZ (local-data.rpz), the zone is loaded once by policy-loader
	// into shared LMDB ruledb. Workers don't load the zone independently, so no OOM risk.
	// We can use auto workers safely now.
	workers := "auto"

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

	// Build local-data section with custom filter rules + RPZ
	var localData strings.Builder
	ctx := context.Background()
	rows, err := s.pg.Query(ctx,
		"SELECT domain FROM filter_rules WHERE enabled = true AND action = 'block' ORDER BY domain")
	customCount := 0

	// Check if we need local-data section at all
	hasCustomRules := false
	hasRPZ := false
	var domains []string

	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var d string
			rows.Scan(&d)
			domains = append(domains, d)
		}
		hasCustomRules = len(domains) > 0
		customCount = len(domains)
	}

	// Check RPZ zone file
	rpzFile := "/etc/knot-resolver/rpz.zone"
	localRpzFile := filepath.Join(projectDir, "config/kresd/rpz.zone")
	if includeRPZ {
		if info, err := os.Stat(localRpzFile); err == nil && info.Size() > 100 {
			hasRPZ = true
		}
	}

	if hasCustomRules || hasRPZ {
		localData.WriteString("local-data:\n")

		// Custom filter records (redirect to block page)
		if hasCustomRules {
			localData.WriteString("  records:\n")
			for _, d := range domains {
				localData.WriteString(fmt.Sprintf("    - owner: %s.\n      ttl: 60\n      rdata: '%s'\n", d, serverIP))
				if !strings.HasPrefix(d, "www.") {
					localData.WriteString(fmt.Sprintf("    - owner: www.%s.\n      ttl: 60\n      rdata: '%s'\n", d, serverIP))
				}
			}
		}

		// RPZ zone file — loaded by policy-loader into shared LMDB ruledb
		// All workers read from shared DB, zone is loaded only ONCE
		if hasRPZ {
			localData.WriteString("  rpz:\n")
			localData.WriteString(fmt.Sprintf("    - file: %s\n", rpzFile))
		}
	}

	log.Printf("Regenerating kresd config: %d custom domains, RPZ native=%v", customCount, includeRPZ)

	config := string(templateData)
	config = strings.ReplaceAll(config, "__WORKERS__", workers)
	config = strings.ReplaceAll(config, "__CACHE_SIZE__", cacheSize)
	config = strings.ReplaceAll(config, "__SUBNET_VIEWS__", subnetViews.String())
	config = strings.ReplaceAll(config, "__LOCAL_DATA__", localData.String())

	os.WriteFile(configPath, []byte(config), 0644)
}

// convertRPZForKresd converts custom CNAME targets to "CNAME ." (NXDOMAIN).
// Komdigi RPZ uses "CNAME lamanlabuh.aduankonten.id." which kresd doesn't support.
// Streams line-by-line to handle 1.4GB+ files without memory issues.
// convertResult holds stats from the conversion
type convertResult struct {
	converted int
	skipped   int
}

func convertRPZForKresd(src, dst string) (convertResult, error) {
	in, err := os.Open(src)
	if err != nil {
		return convertResult{}, err
	}
	defer in.Close()

	out, err := os.Create(dst)
	if err != nil {
		return convertResult{}, err
	}
	defer out.Close()

	writer := bufio.NewWriterSize(out, 256*1024) // 256KB buffer for performance
	scanner := bufio.NewScanner(in)
	scanner.Buffer(make([]byte, 64*1024), 64*1024)

	converted := 0
	skipped := 0
	wroteSOA := false

	for scanner.Scan() {
		line := scanner.Text()

		// Skip empty lines and comments — pass through
		if len(line) == 0 || line[0] == ';' {
			writer.WriteString(line)
			writer.WriteString("\n")
			continue
		}

		// Skip directives like $ORIGIN
		if line[0] == '$' {
			writer.WriteString(line)
			writer.WriteString("\n")
			continue
		}

		fields := strings.Fields(line)
		if len(fields) < 4 {
			continue
		}

		// Detect record type (typically field[3] after owner, TTL, class)
		recType := ""
		for _, f := range fields[1:] {
			upper := strings.ToUpper(f)
			if upper == "SOA" || upper == "NS" || upper == "CNAME" || upper == "A" || upper == "AAAA" || upper == "TXT" {
				recType = upper
				break
			}
		}

		// Write exactly ONE SOA record (required by kresd RPZ parser)
		// Skip NS records (kresd RPZ parser crashes on them)
		// Skip duplicate SOA at end of AXFR
		if recType == "SOA" {
			if !wroteSOA {
				writer.WriteString(line)
				writer.WriteString("\n")
				wroteSOA = true
			}
			continue
		}
		if recType == "NS" {
			continue
		}

		// Only keep CNAME records (the actual RPZ rules)
		if recType != "CNAME" {
			skipped++
			continue
		}

		// Validate domain name strictly for libknot compatibility.
		// libknot crashes on invalid chars, labels starting/ending with hyphens, underscores, etc.
		// Komdigi zone has ~1600 entries that fail strict validation.
		owner := fields[0]
		if !isValidRPZOwner(owner) {
			skipped++
			continue
		}

		// Convert custom CNAME targets to "." (NXDOMAIN)
		// Komdigi uses "CNAME lamanlabuh.aduankonten.id." — kresd only supports "CNAME ."
		// Skip rpz-passthru (whitelisted domains)
		if !strings.Contains(line, "CNAME\t.") && !strings.Contains(line, "CNAME .") &&
			!strings.Contains(strings.ToUpper(line), "RPZ-PASSTHRU") {
			for i, f := range fields {
				if strings.ToUpper(f) == "CNAME" && i+1 < len(fields) {
					target := fields[i+1]
					if target != "." && !strings.HasPrefix(strings.ToLower(target), "rpz-") {
						fields[i+1] = "."
						converted++
					}
					break
				}
			}
			line = strings.Join(fields, "\t")
		}

		writer.WriteString(line)
		writer.WriteString("\n")
	}

	if skipped > 0 {
		log.Printf("RPZ conversion: skipped %d entries (invalid names, unsupported record types)", skipped)
	}

	if err := writer.Flush(); err != nil {
		return convertResult{}, fmt.Errorf("flush: %w", err)
	}
	if err := out.Sync(); err != nil {
		return convertResult{}, fmt.Errorf("sync: %w", err)
	}
	if err := scanner.Err(); err != nil {
		return convertResult{}, fmt.Errorf("scan: %w", err)
	}
	return convertResult{converted: converted, skipped: skipped}, nil
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
