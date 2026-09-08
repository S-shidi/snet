package server

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"snet/internal/protocol"
)

func TestRegisterRateLimit(t *testing.T) {
	ts, _ := newTestServerOpts(t, Options{})
	blocked := 0
	for i := 0; i < defaultRegisterPerMin+5; i++ {
		resp := doJSON(t, http.MethodPost, ts.URL+"/api/v1/devices", "",
			protocol.RegisterDeviceReq{DeviceID: "dev-reg-limit", PublicKey: testKey(i)}, nil)
		if resp.StatusCode == http.StatusTooManyRequests {
			blocked++
		} else if resp.StatusCode != http.StatusOK {
			t.Fatalf("register %d = %d, want 200 or 429", i, resp.StatusCode)
		}
	}
	if blocked == 0 {
		t.Fatal("register endpoint is not rate limited")
	}
}

func TestPublicKeyValidation(t *testing.T) {
	s := NewStore()
	if _, err := s.CreateNetwork("not-base64!!", "dev-pubkey1", "", "", false); err == nil {
		t.Fatal("malformed public key should be rejected")
	}
	if _, err := s.CreateNetwork("c2hvcnQ=", "dev-pubkey2", "", "", false); err == nil {
		t.Fatal("non-32-byte public key should be rejected")
	}
	if _, err := s.CreateNetwork(testKey(1), "dev-pubkey3", "", "", false); err != nil {
		t.Fatalf("valid key rejected: %v", err)
	}
	if _, err := s.CreateNetwork("", "dev-pubkey4", "", "", false); err != nil {
		t.Fatalf("empty key should stay allowed: %v", err)
	}
}

func TestSetEndpointValidation(t *testing.T) {
	s := NewStore()
	c, err := s.CreateNetwork(testKey(1), "dev-ep-01", "", "", false)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetEndpoint(c.Token, "no-port-here", ""); err == nil {
		t.Fatal("endpoint without port should be rejected")
	}
	if err := s.SetEndpoint(c.Token, "host.example.com:99999", ""); err == nil {
		t.Fatal("out-of-range port should be rejected")
	}
	if err := s.SetEndpoint(c.Token, "host.example.com:51820", ""); err != nil {
		t.Fatalf("valid endpoint rejected: %v", err)
	}
	if err := s.SetEndpoint(c.Token, "", ""); err != nil {
		t.Fatalf("empty endpoint should be allowed to clear: %v", err)
	}
}

func TestSetEndpointV6RoundTrip(t *testing.T) {
	s := NewStore()
	c, err := s.CreateNetwork(testKey(1), "dev-ep-v6", "", "", false)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetEndpointV6(c.Token, "[2001:db8::1]:51820"); err != nil {
		t.Fatalf("set v6 endpoint: %v", err)
	}
	peers, err := s.ListPeers(c.Token)
	if err != nil {
		t.Fatalf("list peers: %v", err)
	}
	if peers.Self == nil || peers.Self.EndpointV6 != "[2001:db8::1]:51820" {
		t.Fatalf("self endpointV6 = %+v, want [2001:db8::1]:51820", peers.Self)
	}
	if err := s.SetEndpointV6(c.Token, "not::valid"); err == nil {
		t.Fatal("malformed v6 endpoint should be rejected")
	}
}

func TestBindReportsDeviceName(t *testing.T) {
	s := NewStore()
	tok, _ := genOneCode(t, s)
	if _, err := s.BindDevice(tok, "dev-named-01", testKey(1), "  办公室电脑  "); err != nil {
		t.Fatal(err)
	}
	var dev protocol.Device
	for _, d := range s.AdminDevices() {
		if d.ID == "dev-named-01" {
			dev = d
		}
	}
	if dev.Name != "办公室电脑" {
		t.Fatalf("bind-reported name = %q", dev.Name)
	}

	// Re-bind and register must not clobber the existing name.
	if _, err := s.BindDevice(tok, "dev-named-01", testKey(1), "其它名字"); err != nil {
		t.Fatal(err)
	}
	if err := s.RegisterDevice("dev-named-01", testKey(2), "又一个名字"); err != nil {
		t.Fatal(err)
	}
	for _, d := range s.AdminDevices() {
		if d.ID == "dev-named-01" && d.Name != "办公室电脑" {
			t.Fatalf("client re-report clobbered admin-visible name: %q", d.Name)
		}
	}

	// Unnamed devices stay unnamed; names surface through admin views.
	c, _ := s.CreateNetwork(testKey(3), "dev-named-01", "", "", false)
	nodes, _ := s.AdminNodes(c.NetworkID)
	if len(nodes) != 1 || nodes[0].DeviceName != "办公室电脑" {
		t.Fatalf("AdminNodes DeviceName = %+v", nodes)
	}

	code2, _ := genOneCode(t, s)
	if _, err := s.BindDevice(code2, "dev-bare-02", testKey(4), ""); err != nil {
		t.Fatal(err)
	}
	codes := s.AdminAuthCodes()
	var bare *protocol.AuthCodeBindingInfo
	for i := range codes {
		for j := range codes[i].BoundDevices {
			b := &codes[i].BoundDevices[j]
			if b.DeviceID == "dev-bare-02" {
				bare = b
			}
			if b.DeviceID == "dev-named-01" && b.DeviceName != "办公室电脑" {
				t.Fatalf("AdminAuthCodes DeviceName = %q", b.DeviceName)
			}
		}
	}
	if bare == nil || bare.DeviceName != "" {
		t.Fatalf("unnamed device should report empty name, got %+v", bare)
	}

	// Pending join requests carry the device name too.
	code3, _ := genOneCode(t, s)
	if _, err := s.BindDevice(code3, "dev-pend-03", testKey(8), "待审设备"); err != nil {
		t.Fatal(err)
	}
	code4, _ := genOneCode(t, s)
	if _, err := s.BindDevice(code4, "dev-owner4x", testKey(9), ""); err != nil {
		t.Fatal(err)
	}
	c2, err := s.CreateNetwork(testKey(7), "dev-owner4x", "审批网", "", true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Join(c2.NetworkID, c2.PairingCode, testKey(6), "dev-pend-03"); err != nil {
		t.Fatal(err)
	}
	pending, err := s.AdminPending(c2.NetworkID)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, p := range pending {
		if p.DeviceID == "dev-pend-03" && p.DeviceName == "待审设备" {
			found = true
		}
	}
	if !found {
		t.Fatalf("AdminPending missing deviceName: %+v", pending)
	}
}

func TestDeviceNameCapped(t *testing.T) {
	s := NewStore()
	long := strings.Repeat("名", 100)
	if _, err := s.CreateNetwork(testKey(1), "dev-cap-owner", "", "", false); err != nil {
		t.Fatal(err)
	}
	c, _ := s.CreateNetwork(testKey(2), "dev-cap-a", "", "", false)
	if _, err := s.Join(c.NetworkID, c.PairingCode, testKey(3), "dev-cap-b"); err != nil {
		t.Fatal(err)
	}
	if err := s.RegisterDevice("dev-cap-c", "", long); err != nil {
		t.Fatalf("long name should be capped, not rejected: %v", err)
	}
	for _, d := range s.AdminDevices() {
		if got := len([]rune(d.Name)); got > maxNameLen {
			t.Fatalf("device %s name = %d runes, want <= %d", d.ID, got, maxNameLen)
		}
	}
}

func TestAdminLogoutInvalidatesSession(t *testing.T) {
	ts, _ := newTestServerOpts(t, Options{AdminUser: "admin", AdminPass: "secret"})
	var login protocol.AdminLoginResp
	resp := doJSON(t, http.MethodPost, ts.URL+"/admin/login", "",
		map[string]string{"username": "admin", "password": "secret"}, &login)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("login = %d", resp.StatusCode)
	}
	ok := doJSON(t, http.MethodGet, ts.URL+"/admin/stats", login.Token, nil, nil)
	if ok.StatusCode != http.StatusOK {
		t.Fatalf("stats with session = %d", ok.StatusCode)
	}
	out := doJSON(t, http.MethodPost, ts.URL+"/admin/logout", login.Token, nil, nil)
	if out.StatusCode != http.StatusNoContent {
		t.Fatalf("logout = %d, want 204", out.StatusCode)
	}
	after := doJSON(t, http.MethodGet, ts.URL+"/admin/stats", login.Token, nil, nil)
	if after.StatusCode != http.StatusUnauthorized {
		t.Fatalf("stats after logout = %d, want 401", after.StatusCode)
	}
}

func TestSecurityHeadersAndNoStore(t *testing.T) {
	ts, _ := newTestServerOpts(t, Options{AdminUser: "admin", AdminPass: "secret"})
	check := func(path, token string) map[string]string {
		req, _ := http.NewRequest(http.MethodGet, ts.URL+path, nil)
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		m := make(map[string]string)
		for k := range resp.Header {
			m[k] = resp.Header.Get(k)
		}
		return m
	}
	h := check("/admin", "")
	for _, k := range []string{"X-Content-Type-Options", "Referrer-Policy", "Content-Security-Policy"} {
		if h[k] == "" {
			t.Fatalf("%s missing on /admin", k)
		}
	}
	if !strings.Contains(h["Cache-Control"], "no-store") {
		t.Fatalf("/admin Cache-Control = %q", h["Cache-Control"])
	}
	h = check("/admin/stats", "secret")
	if !strings.Contains(h["Cache-Control"], "no-store") {
		t.Fatalf("/admin/stats Cache-Control = %q", h["Cache-Control"])
	}
}

func TestBadJSONNoDecoderLeak(t *testing.T) {
	ts, _ := newTestServerOpts(t, Options{})
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/v1/networks", strings.NewReader("{bad json"))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("bad json = %d, want 400", resp.StatusCode)
	}
	var e protocol.ErrResp
	_ = json.NewDecoder(resp.Body).Decode(&e)
	if strings.Contains(e.Error, "invalid character") || strings.Contains(e.Error, "looking for") {
		t.Fatalf("decoder internals leaked: %q", e.Error)
	}
}
