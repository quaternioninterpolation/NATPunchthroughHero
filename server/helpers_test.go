package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// --- extractIP ---

func TestExtractIP_RemoteAddr(t *testing.T) {
	req := httptest.NewRequest("GET", "/", nil)
	req.RemoteAddr = "1.2.3.4:12345"

	ip := extractIP(req)
	if ip != "1.2.3.4" {
		t.Errorf("extractIP = %q, want %q", ip, "1.2.3.4")
	}
}

func TestExtractIP_RemoteAddr_NoPort(t *testing.T) {
	req := httptest.NewRequest("GET", "/", nil)
	req.RemoteAddr = "1.2.3.4"

	ip := extractIP(req)
	if ip != "1.2.3.4" {
		t.Errorf("extractIP = %q, want %q", ip, "1.2.3.4")
	}
}

func TestExtractIP_XForwardedFor_NotTrusted(t *testing.T) {
	// Without trusted proxies, XFF should be ignored
	InitTrustedProxies(nil)

	req := httptest.NewRequest("GET", "/", nil)
	req.RemoteAddr = "10.0.0.1:12345"
	req.Header.Set("X-Forwarded-For", "5.5.5.5, 10.0.0.1")

	ip := extractIP(req)
	if ip != "10.0.0.1" {
		t.Errorf("extractIP = %q, want %q (XFF should be ignored without trusted proxies)", ip, "10.0.0.1")
	}
}

func TestExtractIP_XForwardedFor_Trusted(t *testing.T) {
	InitTrustedProxies([]string{"10.0.0.1"})
	defer InitTrustedProxies(nil) // cleanup

	req := httptest.NewRequest("GET", "/", nil)
	req.RemoteAddr = "10.0.0.1:12345"
	req.Header.Set("X-Forwarded-For", "5.5.5.5, 10.0.0.1")

	ip := extractIP(req)
	if ip != "5.5.5.5" {
		t.Errorf("extractIP = %q, want %q (leftmost non-proxy from XFF)", ip, "5.5.5.5")
	}
}

func TestExtractIP_XRealIP_Trusted(t *testing.T) {
	InitTrustedProxies([]string{"10.0.0.1"})
	defer InitTrustedProxies(nil)

	req := httptest.NewRequest("GET", "/", nil)
	req.RemoteAddr = "10.0.0.1:12345"
	req.Header.Set("X-Real-IP", "3.3.3.3")

	ip := extractIP(req)
	if ip != "3.3.3.3" {
		t.Errorf("extractIP = %q, want %q (X-Real-IP from trusted proxy)", ip, "3.3.3.3")
	}
}

func TestExtractIP_IPv6(t *testing.T) {
	req := httptest.NewRequest("GET", "/", nil)
	req.RemoteAddr = "[::1]:12345"

	ip := extractIP(req)
	if ip != "::1" {
		t.Errorf("extractIP = %q, want %q", ip, "::1")
	}
}

// --- sanitizeString ---

func TestSanitizeString_Normal(t *testing.T) {
	result := sanitizeString("Hello World", 100)
	if result != "Hello World" {
		t.Errorf("sanitizeString = %q, want %q", result, "Hello World")
	}
}

func TestSanitizeString_Truncate(t *testing.T) {
	result := sanitizeString("Hello World", 5)
	if result != "Hello" {
		t.Errorf("sanitizeString = %q, want %q", result, "Hello")
	}
}

func TestSanitizeString_EmptyString(t *testing.T) {
	result := sanitizeString("", 100)
	if result != "" {
		t.Errorf("sanitizeString = %q, want empty", result)
	}
}

func TestSanitizeString_StripHTML(t *testing.T) {
	result := sanitizeString("<script>alert('xss')</script>Hello", 100)
	if result == "<script>alert('xss')</script>Hello" {
		// If it keeps HTML, it should at least not break
		// The exact behavior depends on implementation
	}
	// Should not contain raw script tags
	for _, bad := range []string{"<script>", "</script>"} {
		if contains(result, bad) {
			t.Errorf("sanitizeString should strip HTML: got %q", result)
		}
	}
}

func TestSanitizeString_TrimWhitespace(t *testing.T) {
	result := sanitizeString("  Hello  ", 100)
	if result != "Hello" {
		t.Errorf("sanitizeString = %q, want %q", result, "Hello")
	}
}

func TestSanitizeString_ZeroLength(t *testing.T) {
	result := sanitizeString("Hello", 0)
	if result != "" {
		t.Errorf("sanitizeString with maxLen=0 = %q, want empty", result)
	}
}

// --- InitTrustedProxies ---

func TestInitTrustedProxies_NilClearsTrust(t *testing.T) {
	InitTrustedProxies([]string{"10.0.0.1"})
	InitTrustedProxies(nil)

	req := httptest.NewRequest("GET", "/", nil)
	req.RemoteAddr = "10.0.0.1:12345"
	req.Header.Set("X-Forwarded-For", "5.5.5.5")

	ip := extractIP(req)
	if ip != "10.0.0.1" {
		t.Error("after clearing trusted proxies, XFF should be ignored")
	}
}

// Helper to avoid importing strings in test
func contains(s, substr string) bool {
	for i := 0; i+len(substr) <= len(s); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

// Test extractIP with middleware chain (simulating real request flow)
func TestExtractIP_MiddlewareChain(t *testing.T) {
	InitTrustedProxies(nil)

	var capturedIP string
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedIP = extractIP(r)
		w.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequest("GET", "/", nil)
	req.RemoteAddr = "192.168.1.100:54321"

	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if capturedIP != "192.168.1.100" {
		t.Errorf("capturedIP = %q, want %q", capturedIP, "192.168.1.100")
	}
}
