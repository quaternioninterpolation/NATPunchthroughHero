package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"testing"
)

// --- Mode: off ---

func TestIPFilter_Off_AllowsEverything(t *testing.T) {
	f := NewIPFilter(IPFilterConfig{Mode: "off"})

	for _, ip := range []string{"1.2.3.4", "10.0.0.1", "::1", "invalid"} {
		if !f.IsAllowed(ip) {
			t.Errorf("mode=off should allow %q", ip)
		}
	}
}

// --- Mode: blocklist ---

func TestIPFilter_Blocklist_BlocksListed(t *testing.T) {
	f := NewIPFilter(IPFilterConfig{
		Mode:      "blocklist",
		Blocklist: []string{"1.2.3.4", "10.0.0.1"},
	})

	if f.IsAllowed("1.2.3.4") {
		t.Error("1.2.3.4 should be blocked")
	}
	if f.IsAllowed("10.0.0.1") {
		t.Error("10.0.0.1 should be blocked")
	}
}

func TestIPFilter_Blocklist_AllowsUnlisted(t *testing.T) {
	f := NewIPFilter(IPFilterConfig{
		Mode:      "blocklist",
		Blocklist: []string{"1.2.3.4"},
	})

	if !f.IsAllowed("5.6.7.8") {
		t.Error("5.6.7.8 should be allowed (not in blocklist)")
	}
}

func TestIPFilter_Blocklist_CIDR(t *testing.T) {
	f := NewIPFilter(IPFilterConfig{
		Mode:      "blocklist",
		Blocklist: []string{"10.0.0.0/24"},
	})

	if f.IsAllowed("10.0.0.50") {
		t.Error("10.0.0.50 should be blocked by CIDR 10.0.0.0/24")
	}
	if f.IsAllowed("10.0.0.255") {
		t.Error("10.0.0.255 should be blocked by CIDR 10.0.0.0/24")
	}
	if !f.IsAllowed("10.0.1.1") {
		t.Error("10.0.1.1 should be allowed (outside CIDR)")
	}
}

// --- Mode: allowlist ---

func TestIPFilter_Allowlist_AllowsListed(t *testing.T) {
	f := NewIPFilter(IPFilterConfig{
		Mode:      "allowlist",
		Allowlist: []string{"192.168.1.1", "192.168.1.2"},
	})

	if !f.IsAllowed("192.168.1.1") {
		t.Error("listed IP should be allowed")
	}
}

func TestIPFilter_Allowlist_DeniesUnlisted(t *testing.T) {
	f := NewIPFilter(IPFilterConfig{
		Mode:      "allowlist",
		Allowlist: []string{"192.168.1.1"},
	})

	if f.IsAllowed("10.0.0.1") {
		t.Error("unlisted IP should be denied in allowlist mode")
	}
}

func TestIPFilter_Allowlist_CIDR(t *testing.T) {
	f := NewIPFilter(IPFilterConfig{
		Mode:      "allowlist",
		Allowlist: []string{"10.0.0.0/16"},
	})

	if !f.IsAllowed("10.0.5.100") {
		t.Error("10.0.5.100 should be allowed by CIDR 10.0.0.0/16")
	}
	if f.IsAllowed("11.0.0.1") {
		t.Error("11.0.0.1 should be denied (outside CIDR)")
	}
}

// --- Invalid IP ---

func TestIPFilter_InvalidIP_Blocked(t *testing.T) {
	f := NewIPFilter(IPFilterConfig{Mode: "blocklist"})

	if f.IsAllowed("not-an-ip") {
		t.Error("unparseable IP should be blocked for safety")
	}
}

// --- AddToBlocklist ---

func TestIPFilter_AddToBlocklist_IP(t *testing.T) {
	f := NewIPFilter(IPFilterConfig{Mode: "blocklist"})

	if !f.IsAllowed("5.5.5.5") {
		t.Fatal("should be allowed before adding")
	}

	f.AddToBlocklist("5.5.5.5")

	if f.IsAllowed("5.5.5.5") {
		t.Error("should be blocked after AddToBlocklist")
	}
}

func TestIPFilter_AddToBlocklist_CIDR(t *testing.T) {
	f := NewIPFilter(IPFilterConfig{Mode: "blocklist"})

	f.AddToBlocklist("172.16.0.0/12")

	if f.IsAllowed("172.20.5.10") {
		t.Error("172.20.5.10 should be blocked by CIDR 172.16.0.0/12")
	}
}

// --- RemoveFromBlocklist ---

func TestIPFilter_RemoveFromBlocklist_IP(t *testing.T) {
	f := NewIPFilter(IPFilterConfig{
		Mode:      "blocklist",
		Blocklist: []string{"1.2.3.4"},
	})

	if f.IsAllowed("1.2.3.4") {
		t.Fatal("should be blocked initially")
	}

	f.RemoveFromBlocklist("1.2.3.4")

	if !f.IsAllowed("1.2.3.4") {
		t.Error("should be allowed after removal")
	}
}

func TestIPFilter_RemoveFromBlocklist_CIDR(t *testing.T) {
	f := NewIPFilter(IPFilterConfig{
		Mode:      "blocklist",
		Blocklist: []string{"10.0.0.0/24"},
	})

	if f.IsAllowed("10.0.0.50") {
		t.Fatal("should be blocked initially")
	}

	f.RemoveFromBlocklist("10.0.0.0/24")

	if !f.IsAllowed("10.0.0.50") {
		t.Error("should be allowed after CIDR removal")
	}
}

// --- GetBlocklist ---

func TestIPFilter_GetBlocklist(t *testing.T) {
	f := NewIPFilter(IPFilterConfig{
		Mode:      "blocklist",
		Blocklist: []string{"1.2.3.4", "10.0.0.0/24"},
	})
	f.AddToBlocklist("5.5.5.5")

	list := f.GetBlocklist()
	sort.Strings(list)

	// Should contain all entries
	found := map[string]bool{}
	for _, e := range list {
		found[e] = true
	}

	if !found["1.2.3.4"] {
		t.Error("missing 1.2.3.4 from blocklist")
	}
	if !found["5.5.5.5"] {
		t.Error("missing 5.5.5.5 from blocklist")
	}
	if !found["10.0.0.0/24"] {
		t.Error("missing 10.0.0.0/24 from blocklist")
	}
}

// --- Reload ---

func TestIPFilter_Reload_PreservesRuntimeBlocks(t *testing.T) {
	f := NewIPFilter(IPFilterConfig{Mode: "blocklist"})

	// Add runtime block
	f.AddToBlocklist("9.9.9.9")

	// Reload with new config
	f.Reload(IPFilterConfig{
		Mode:      "blocklist",
		Blocklist: []string{"1.1.1.1"},
	})

	// Runtime block should persist
	if f.IsAllowed("9.9.9.9") {
		t.Error("runtime block should persist after Reload")
	}
	// New config block should be present
	if f.IsAllowed("1.1.1.1") {
		t.Error("new config block should be present after Reload")
	}
}

func TestIPFilter_Reload_ChangesMode(t *testing.T) {
	f := NewIPFilter(IPFilterConfig{Mode: "off"})

	if !f.IsAllowed("1.2.3.4") {
		t.Fatal("mode=off should allow everything")
	}

	f.Reload(IPFilterConfig{
		Mode:      "blocklist",
		Blocklist: []string{"1.2.3.4"},
	})

	if f.IsAllowed("1.2.3.4") {
		t.Error("should be blocked after reloading to blocklist mode")
	}
}

// --- File-based loading ---

func TestIPFilter_FileBasedBlocklist(t *testing.T) {
	// Create temp file with IPs
	dir := t.TempDir()
	path := filepath.Join(dir, "blocklist.txt")
	content := "# comment\n6.6.6.6\n10.0.0.0/8\n\n  7.7.7.7  \n"
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	f := NewIPFilter(IPFilterConfig{
		Mode:      "blocklist",
		Blocklist: []string{"file://" + path},
	})

	if f.IsAllowed("6.6.6.6") {
		t.Error("6.6.6.6 from file should be blocked")
	}
	if f.IsAllowed("7.7.7.7") {
		t.Error("7.7.7.7 from file should be blocked")
	}
	if f.IsAllowed("10.50.0.1") {
		t.Error("10.50.0.1 should be blocked by file CIDR 10.0.0.0/8")
	}
	if !f.IsAllowed("8.8.8.8") {
		t.Error("8.8.8.8 should be allowed (not in file)")
	}
}

func TestIPFilter_FileMissing_NoError(t *testing.T) {
	// Should log an error but not panic
	f := NewIPFilter(IPFilterConfig{
		Mode:      "blocklist",
		Blocklist: []string{"file:///nonexistent/path/block.txt"},
	})

	// Still functions
	if !f.IsAllowed("1.2.3.4") {
		t.Error("should be allowed when file is missing")
	}
}

// --- Middleware ---

func TestIPFilter_Middleware_Blocks(t *testing.T) {
	f := NewIPFilter(IPFilterConfig{
		Mode:      "blocklist",
		Blocklist: []string{"10.0.0.1"},
	})

	handler := f.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/test", nil)
	req.RemoteAddr = "10.0.0.1:12345"
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Errorf("got %d, want 403 Forbidden", w.Code)
	}
}

func TestIPFilter_Middleware_Allows(t *testing.T) {
	f := NewIPFilter(IPFilterConfig{
		Mode:      "blocklist",
		Blocklist: []string{"10.0.0.1"},
	})

	handler := f.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/test", nil)
	req.RemoteAddr = "5.5.5.5:12345"
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("got %d, want 200", w.Code)
	}
}

// --- IPv6 ---

func TestIPFilter_IPv6_Blocklist(t *testing.T) {
	f := NewIPFilter(IPFilterConfig{
		Mode:      "blocklist",
		Blocklist: []string{"::1", "fe80::/10"},
	})

	if f.IsAllowed("::1") {
		t.Error("::1 should be blocked")
	}
	if f.IsAllowed("fe80::1") {
		t.Error("fe80::1 should be blocked by CIDR fe80::/10")
	}
	if !f.IsAllowed("2001:db8::1") {
		t.Error("2001:db8::1 should be allowed")
	}
}
