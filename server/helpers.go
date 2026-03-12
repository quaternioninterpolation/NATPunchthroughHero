package main

import (
	"net"
	"net/http"
	"strings"
)

// extractIP gets the client's real IP address from a request,
// respecting trusted proxy X-Forwarded-For headers.
//
// Security: X-Forwarded-For is ONLY trusted if the immediate
// connection comes from a configured trusted proxy IP. Otherwise
// it is ignored to prevent IP spoofing.
var trustedProxies []*net.IPNet

// InitTrustedProxies parses the trusted proxy CIDR list from config.
func InitTrustedProxies(proxies []string) {
	trustedProxies = nil
	for _, p := range proxies {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		// Add /32 if no CIDR notation
		if !strings.Contains(p, "/") {
			p += "/32"
		}
		_, cidr, err := net.ParseCIDR(p)
		if err == nil {
			trustedProxies = append(trustedProxies, cidr)
		}
	}
}

// extractIP gets the real client IP from the request.
func extractIP(r *http.Request) string {
	// Get the direct connection IP
	directIP := r.RemoteAddr
	if host, _, err := net.SplitHostPort(directIP); err == nil {
		directIP = host
	}

	// Only trust X-Forwarded-For if the direct connection is from a trusted proxy
	if len(trustedProxies) > 0 {
		ip := net.ParseIP(directIP)
		if ip != nil {
			for _, cidr := range trustedProxies {
				if cidr.Contains(ip) {
					// Trusted proxy — read X-Forwarded-For
					xff := r.Header.Get("X-Forwarded-For")
					if xff != "" {
						// Take the first (leftmost) IP — this is the original client
						parts := strings.SplitN(xff, ",", 2)
						clientIP := strings.TrimSpace(parts[0])
						if net.ParseIP(clientIP) != nil {
							return clientIP
						}
					}
					// Also check X-Real-IP
					xri := r.Header.Get("X-Real-IP")
					if xri != "" {
						xri = strings.TrimSpace(xri)
						if net.ParseIP(xri) != nil {
							return xri
						}
					}
					break
				}
			}
		}
	}

	return directIP
}

// sanitizeString cleans user input: trims whitespace, limits length,
// and removes HTML/script-like content.
func sanitizeString(s string, maxLen int) string {
	s = strings.TrimSpace(s)
	if len(s) > maxLen {
		s = s[:maxLen]
	}
	// Strip potential HTML/script tags
	s = strings.ReplaceAll(s, "<", "")
	s = strings.ReplaceAll(s, ">", "")
	s = strings.ReplaceAll(s, "&", "")
	return s
}
