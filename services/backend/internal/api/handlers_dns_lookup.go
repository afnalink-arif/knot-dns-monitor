package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os/exec"
	"regexp"
	"strings"
	"time"
)

var validDomainRe = regexp.MustCompile(`^[a-zA-Z0-9]([a-zA-Z0-9\-]*[a-zA-Z0-9])?(\.[a-zA-Z0-9]([a-zA-Z0-9\-]*[a-zA-Z0-9])?)*\.[a-zA-Z]{2,}$`)

// resolveKresdIP resolves the "kresd" docker service name to an IP address.
// dig can't use docker hostnames directly — it needs an IP.
func resolveKresdIP() string {
	addrs, err := net.LookupHost("kresd")
	if err == nil && len(addrs) > 0 {
		return addrs[0]
	}
	return "kresd" // fallback
}

type DNSLookupRequest struct {
	Domain string `json:"domain"`
	Type   string `json:"type"`
	Server string `json:"server"` // "local" or "external"
}

type DNSRecord struct {
	Name  string `json:"name"`
	TTL   int    `json:"ttl"`
	Type  string `json:"type"`
	Value string `json:"value"`
}

type DNSLookupResult struct {
	Domain      string      `json:"domain"`
	Server      string      `json:"server"`
	ServerAddr  string      `json:"server_addr"`
	QueryType   string      `json:"query_type"`
	Status      string      `json:"status"` // "ok", "nxdomain", "blocked", "error"
	StatusText  string      `json:"status_text"`
	Records     []DNSRecord `json:"records"`
	RawOutput   string      `json:"raw_output"`
	QueryTimeMs int         `json:"query_time_ms"`
	Blocked     bool        `json:"blocked"`
	BlockReason string      `json:"block_reason,omitempty"`
}

func (s *Server) handleDNSLookup(w http.ResponseWriter, r *http.Request) {
	var req DNSLookupRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid request"}`, http.StatusBadRequest)
		return
	}

	// Sanitize domain
	req.Domain = strings.TrimSpace(strings.ToLower(req.Domain))
	req.Domain = strings.TrimSuffix(req.Domain, ".")
	// Strip protocol prefix if user pastes a URL
	req.Domain = strings.TrimPrefix(req.Domain, "https://")
	req.Domain = strings.TrimPrefix(req.Domain, "http://")
	// Strip path
	if idx := strings.Index(req.Domain, "/"); idx > 0 {
		req.Domain = req.Domain[:idx]
	}

	if !validDomainRe.MatchString(req.Domain) {
		http.Error(w, `{"error":"invalid domain name"}`, http.StatusBadRequest)
		return
	}

	if req.Type == "" {
		req.Type = "A"
	}
	allowedTypes := map[string]bool{
		"A": true, "AAAA": true, "CNAME": true, "MX": true,
		"NS": true, "TXT": true, "SOA": true, "SRV": true, "PTR": true, "ANY": true,
	}
	req.Type = strings.ToUpper(req.Type)
	if !allowedTypes[req.Type] {
		http.Error(w, `{"error":"unsupported record type"}`, http.StatusBadRequest)
		return
	}

	// Determine DNS server
	// Resolve docker service name to IP (dig can't resolve docker hostnames reliably)
	serverAddr := resolveKresdIP()
	serverLabel := "Local Resolver (kresd)"
	if req.Server == "google" {
		serverAddr = "8.8.8.8"
		serverLabel = "Google DNS (8.8.8.8)"
	} else if req.Server == "cloudflare" {
		serverAddr = "1.1.1.1"
		serverLabel = "Cloudflare DNS (1.1.1.1)"
	} else if req.Server == "quad9" {
		serverAddr = "9.9.9.9"
		serverLabel = "Quad9 DNS (9.9.9.9)"
	}

	// Run dig — use background context to avoid inheriting any request deadline
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	start := time.Now()
	cmd := exec.CommandContext(ctx, "dig", fmt.Sprintf("@%s", serverAddr), req.Domain, req.Type, "+noall", "+answer", "+authority", "+stats", "+comments")
	out, err := cmd.CombinedOutput()
	elapsed := time.Since(start)

	result := DNSLookupResult{
		Domain:      req.Domain,
		Server:      serverLabel,
		ServerAddr:  serverAddr,
		QueryType:   req.Type,
		QueryTimeMs: int(elapsed.Milliseconds()),
		Records:     []DNSRecord{},
	}

	if err != nil {
		result.Status = "error"
		result.StatusText = fmt.Sprintf("Query failed: %v", err)
		result.RawOutput = string(out)
		writeJSON(w, result)
		return
	}

	rawOutput := string(out)
	result.RawOutput = rawOutput

	// Parse status from comments
	if strings.Contains(rawOutput, "NXDOMAIN") {
		result.Status = "nxdomain"
		result.StatusText = "Domain tidak ditemukan (NXDOMAIN)"
	} else if strings.Contains(rawOutput, "REFUSED") {
		result.Status = "error"
		result.StatusText = "Query ditolak (REFUSED)"
	} else if strings.Contains(rawOutput, "SERVFAIL") {
		result.Status = "error"
		result.StatusText = "Server gagal (SERVFAIL)"
	} else {
		result.Status = "ok"
		result.StatusText = "Berhasil"
	}

	// Parse answer records
	for _, line := range strings.Split(rawOutput, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, ";") || strings.HasPrefix(line, ";;") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) >= 5 {
			ttl := 0
			fmt.Sscanf(fields[1], "%d", &ttl)
			rec := DNSRecord{
				Name:  strings.TrimSuffix(fields[0], "."),
				TTL:   ttl,
				Type:  fields[3],
				Value: strings.Join(fields[4:], " "),
			}
			result.Records = append(result.Records, rec)
		}
	}

	// Check if domain is blocked (local server only)
	if req.Server == "local" {
		result.Blocked = checkIfBlocked(result, s)
		if result.Blocked {
			result.Status = "blocked"
			result.StatusText = "Domain ini DIBLOKIR oleh DNS filter"
			result.BlockReason = detectBlockReason(req.Domain, s)
		}
	}

	writeJSON(w, result)
}

// checkIfBlocked determines if the DNS result indicates blocking
func checkIfBlocked(result DNSLookupResult, s *Server) bool {
	// NXDOMAIN from local resolver could mean RPZ block
	if result.Status == "nxdomain" {
		// Check if domain exists in filter rules
		var count int
		s.pg.QueryRow(context.Background(),
			"SELECT count(*) FROM filter_rules WHERE domain = $1 AND enabled = true", result.Domain).Scan(&count)
		if count > 0 {
			return true
		}
		// Could be RPZ — check if RPZ is enabled
		rpzCfg := s.getRPZConfig()
		if rpzCfg.Enabled {
			return true // NXDOMAIN + RPZ enabled = likely blocked by RPZ
		}
	}

	// Check if A record points to our server IP (block page redirect)
	serverIP := loadEnvFile(s.cfg.ProjectDir + "/.env")["SERVER_IP"]
	if serverIP != "" {
		for _, rec := range result.Records {
			if rec.Type == "A" && rec.Value == serverIP {
				return true
			}
		}
	}

	return false
}

// detectBlockReason checks why a domain is blocked
func detectBlockReason(domain string, s *Server) string {
	// Check custom filter rules
	var category string
	err := s.pg.QueryRow(context.Background(),
		"SELECT category FROM filter_rules WHERE domain = $1 AND enabled = true LIMIT 1", domain).Scan(&category)
	if err == nil {
		return fmt.Sprintf("Custom filter rule (kategori: %s)", category)
	}

	// Check RPZ
	rpzCfg := s.getRPZConfig()
	if rpzCfg.Enabled {
		return "RPZ Trust Positif Komdigi"
	}

	return "Unknown"
}

// handleDNSLookupCompare does a parallel lookup on local + external to compare
func compareLookup(domain, qtype string, s *Server) (local, external DNSLookupResult) {
	ch := make(chan DNSLookupResult, 2)

	lookup := func(server, label string) {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		start := time.Now()
		cmd := exec.CommandContext(ctx, "dig", fmt.Sprintf("@%s", server), domain, qtype, "+short")
		out, _ := cmd.CombinedOutput()
		elapsed := time.Since(start)

		result := DNSLookupResult{
			Domain:      domain,
			Server:      label,
			ServerAddr:  server,
			QueryType:   qtype,
			QueryTimeMs: int(elapsed.Milliseconds()),
			RawOutput:   strings.TrimSpace(string(out)),
			Records:     []DNSRecord{},
		}

		// Parse +short output
		for _, line := range strings.Split(result.RawOutput, "\n") {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			rtype := qtype
			if net.ParseIP(line) != nil {
				if strings.Contains(line, ":") {
					rtype = "AAAA"
				} else {
					rtype = "A"
				}
			}
			result.Records = append(result.Records, DNSRecord{
				Name: domain, Type: rtype, Value: line,
			})
		}

		if len(result.Records) == 0 {
			result.Status = "nxdomain"
		} else {
			result.Status = "ok"
		}

		ch <- result
	}

	go lookup(resolveKresdIP(), "Local (kresd)")
	go lookup("8.8.8.8", "Google DNS")

	r1 := <-ch
	r2 := <-ch

	if r1.Server == "Local (kresd)" {
		return r1, r2
	}
	return r2, r1
}
