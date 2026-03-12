package main

import (
	"bufio"
	"log"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
)

// IPFilter provides IP-based access control with support for
// individual IPs, CIDR ranges, and file-based lists.
//
// Modes:
//   - "off":       no filtering
//   - "blocklist": block listed IPs, allow everything else
//   - "allowlist": allow only listed IPs (private server mode)
type IPFilter struct {
	mu        sync.RWMutex
	mode      string
	blocklist []*net.IPNet
	allowlist []*net.IPNet
	blockIPs  map[string]bool // individual IPs for O(1) lookup
	allowIPs  map[string]bool
}

// NewIPFilter creates an IP filter from the given configuration.
func NewIPFilter(cfg IPFilterConfig) *IPFilter {
	f := &IPFilter{
		mode:     cfg.Mode,
		blockIPs: make(map[string]bool),
		allowIPs: make(map[string]bool),
	}

	f.blocklist = parseIPList(cfg.Blocklist, f.blockIPs)
	f.allowlist = parseIPList(cfg.Allowlist, f.allowIPs)

	return f
}

// IsAllowed checks if the given IP string is permitted by the filter rules.
func (f *IPFilter) IsAllowed(ipStr string) bool {
	f.mu.RLock()
	defer f.mu.RUnlock()

	if f.mode == "off" {
		return true
	}

	ip := net.ParseIP(ipStr)
	if ip == nil {
		// Can't parse? Block by default for safety.
		return false
	}

	switch f.mode {
	case "allowlist":
		return f.matchesAllowlist(ipStr, ip)
	case "blocklist":
		return !f.matchesBlocklist(ipStr, ip)
	default:
		return true
	}
}

// AddToBlocklist adds an IP or CIDR to the runtime blocklist.
// This does NOT persist to disk — use for auto-blocking.
func (f *IPFilter) AddToBlocklist(entry string) {
	f.mu.Lock()
	defer f.mu.Unlock()

	entry = strings.TrimSpace(entry)
	if strings.Contains(entry, "/") {
		_, cidr, err := net.ParseCIDR(entry)
		if err == nil {
			f.blocklist = append(f.blocklist, cidr)
		}
	} else {
		f.blockIPs[entry] = true
	}
}

// RemoveFromBlocklist removes an IP from the runtime blocklist.
func (f *IPFilter) RemoveFromBlocklist(entry string) {
	f.mu.Lock()
	defer f.mu.Unlock()

	entry = strings.TrimSpace(entry)
	delete(f.blockIPs, entry)

	// For CIDR ranges, rebuild without the matching entry
	if strings.Contains(entry, "/") {
		_, target, err := net.ParseCIDR(entry)
		if err != nil {
			return
		}
		var filtered []*net.IPNet
		for _, cidr := range f.blocklist {
			if cidr.String() != target.String() {
				filtered = append(filtered, cidr)
			}
		}
		f.blocklist = filtered
	}
}

// GetBlocklist returns the current blocklist entries as strings.
func (f *IPFilter) GetBlocklist() []string {
	f.mu.RLock()
	defer f.mu.RUnlock()

	var result []string
	for ip := range f.blockIPs {
		result = append(result, ip)
	}
	for _, cidr := range f.blocklist {
		result = append(result, cidr.String())
	}
	return result
}

// Reload reloads the filter from new configuration.
// Called on SIGHUP or admin API request.
func (f *IPFilter) Reload(cfg IPFilterConfig) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.mode = cfg.Mode
	newBlockIPs := make(map[string]bool)
	newAllowIPs := make(map[string]bool)

	f.blocklist = parseIPList(cfg.Blocklist, newBlockIPs)
	f.allowlist = parseIPList(cfg.Allowlist, newAllowIPs)

	// Merge runtime blocked IPs (from auto-block) with new config
	for ip := range f.blockIPs {
		newBlockIPs[ip] = true
	}
	f.blockIPs = newBlockIPs
	f.allowIPs = newAllowIPs
}

// Middleware returns an HTTP middleware that enforces IP filtering.
func (f *IPFilter) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ip := extractIP(r)
		if !f.IsAllowed(ip) {
			http.Error(w, `{"error":"forbidden","message":"Access denied"}`, http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// matchesBlocklist checks if an IP matches any blocklist entry.
func (f *IPFilter) matchesBlocklist(ipStr string, ip net.IP) bool {
	if f.blockIPs[ipStr] {
		return true
	}
	for _, cidr := range f.blocklist {
		if cidr.Contains(ip) {
			return true
		}
	}
	return false
}

// matchesAllowlist checks if an IP matches any allowlist entry.
func (f *IPFilter) matchesAllowlist(ipStr string, ip net.IP) bool {
	if f.allowIPs[ipStr] {
		return true
	}
	for _, cidr := range f.allowlist {
		if cidr.Contains(ip) {
			return true
		}
	}
	return false
}

// parseIPList parses a mixed list of IPs, CIDRs, and file references.
// Individual IPs go into the ipMap for O(1) lookup; CIDRs are returned.
func parseIPList(entries []string, ipMap map[string]bool) []*net.IPNet {
	var cidrs []*net.IPNet

	for _, entry := range entries {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}

		// File reference: "file:///path/to/list.txt"
		if strings.HasPrefix(entry, "file://") {
			path := strings.TrimPrefix(entry, "file://")
			fileCIDRs := loadIPFile(path, ipMap)
			cidrs = append(cidrs, fileCIDRs...)
			continue
		}

		// CIDR range
		if strings.Contains(entry, "/") {
			_, cidr, err := net.ParseCIDR(entry)
			if err != nil {
				log.Printf("[ipfilter] invalid CIDR %q: %v", entry, err)
				continue
			}
			cidrs = append(cidrs, cidr)
			continue
		}

		// Individual IP
		if net.ParseIP(entry) != nil {
			ipMap[entry] = true
		} else {
			log.Printf("[ipfilter] invalid IP %q", entry)
		}
	}

	return cidrs
}

// loadIPFile reads a file with one IP/CIDR per line (# comments allowed).
func loadIPFile(path string, ipMap map[string]bool) []*net.IPNet {
	var cidrs []*net.IPNet

	f, err := os.Open(path)
	if err != nil {
		log.Printf("[ipfilter] could not open file %q: %v", path, err)
		return cidrs
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		if strings.Contains(line, "/") {
			_, cidr, err := net.ParseCIDR(line)
			if err != nil {
				continue
			}
			cidrs = append(cidrs, cidr)
		} else if net.ParseIP(line) != nil {
			ipMap[line] = true
		}
	}

	return cidrs
}
