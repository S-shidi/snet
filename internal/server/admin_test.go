package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"virtualnet/internal/protocol"
)

func TestHealthz(t *testing.T) {
	ts, _ := newTestServer(t)
	resp := doJSON(t, http.MethodGet, ts.URL+"/healthz", "", nil, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("healthz status = %d", resp.StatusCode)
	}
}

func TestAdminWebRoot(t *testing.T) {
	noRedirect := func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }
	client := &http.Client{CheckRedirect: noRedirect}

	enabled, _ := newTestServerOpts(t, Options{AdminUser: "admin", AdminPass: "secret"})
	for _, p := range []string{"/", "/admin/", "/admin"} {
		resp, err := client.Get(enabled.URL + p)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if p != "/admin" {
			if resp.StatusCode != http.StatusMovedPermanently {
				t.Fatalf("%s status = %d, want 301", p, resp.StatusCode)
			}
			if loc := resp.Header.Get("Location"); loc != "/admin" {
				t.Fatalf("%s Location = %q, want /admin", p, loc)
			}
		} else if resp.StatusCode != http.StatusOK {
			t.Fatalf("/admin status = %d, want 200", resp.StatusCode)
		}
	}

	disabled, _ := newTestServer(t)
	for _, p := range []string{"/", "/admin/"} {
		resp, err := client.Get(disabled.URL + p)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusNotFound {
			t.Fatalf("disabled %s status = %d, want 404", p, resp.StatusCode)
		}
	}
}

func TestAdminDisabledByDefault(t *testing.T) {
	ts, _ := newTestServer(t)
	resp := doJSON(t, http.MethodGet, ts.URL+"/admin/networks", "", nil, nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("admin without token configured should be 404, got %d", resp.StatusCode)
	}
}

func TestAdminFlow(t *testing.T) {
	ts, _ := newTestServerOpts(t, Options{AdminToken: "secret"})

	// unauthorized
	resp := doJSON(t, http.MethodGet, ts.URL+"/admin/networks", "badtoken", nil, nil)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("admin bad token = %d, want 401", resp.StatusCode)
	}

	// create a network and join one peer
	var created map[string]any
	resp = doJSON(t, http.MethodPost, ts.URL+"/api/v1/networks", "", map[string]any{"publicKey": "AAA=="}, &created)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create = %d", resp.StatusCode)
	}
	nid := created["networkId"].(string)
	code := created["pairingCode"].(string)
	var joined map[string]any
	doJSON(t, http.MethodPost, ts.URL+"/api/v1/networks/"+nid+"/join", "",
		map[string]any{"code": code, "publicKey": "BBB=="}, &joined)

	// list networks (admin)
	var nets []networkSummary
	resp = doJSON(t, http.MethodGet, ts.URL+"/admin/networks", "secret", nil, &nets)
	if resp.StatusCode != http.StatusOK || len(nets) != 1 || nets[0].NodeCount != 2 {
		t.Fatalf("admin networks = %d %+v", resp.StatusCode, nets)
	}

	// list nodes (admin)
	var nodes []map[string]any
	resp = doJSON(t, http.MethodGet, ts.URL+"/admin/networks/"+nid+"/nodes", "secret", nil, &nodes)
	if resp.StatusCode != http.StatusOK || len(nodes) != 2 {
		t.Fatalf("admin nodes = %d %+v", resp.StatusCode, nodes)
	}

	// kick the joiner
	jid := joined["nodeId"].(string)
	resp = doJSON(t, http.MethodDelete, ts.URL+"/admin/networks/"+nid+"/nodes/"+jid, "secret", nil, nil)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("kick = %d", resp.StatusCode)
	}
	resp = doJSON(t, http.MethodGet, ts.URL+"/admin/networks/"+nid+"/nodes", "secret", nil, &nodes)
	if len(nodes) != 1 {
		t.Fatalf("after kick nodes = %d", len(nodes))
	}
	// kicked joiner's token no longer works
	resp = doJSON(t, http.MethodGet, ts.URL+"/api/v1/networks/"+nid+"/peers", joined["token"].(string), nil, nil)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("kicked token should be 401, got %d", resp.StatusCode)
	}

	// reset pairing code: old code fails, new code works
	resp = doJSON(t, http.MethodPost, ts.URL+"/admin/networks/"+nid+"/code", "secret", nil, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("reset code = %d", resp.StatusCode)
	}
	resp = doJSON(t, http.MethodPost, ts.URL+"/api/v1/networks/"+nid+"/join", "",
		map[string]any{"code": code, "publicKey": "CCC=="}, nil)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("old code should be invalid after reset, got %d", resp.StatusCode)
	}
	var j3 map[string]any
	resp = doJSON(t, http.MethodPost, ts.URL+"/api/v1/networks/"+nid+"/join", "",
		map[string]any{"code": "RESETCODE1", "publicKey": "CCC=="}, nil)
	_ = j3
	// the actual new code comes from the reset response body
	var resetBody map[string]string
	doJSON(t, http.MethodPost, ts.URL+"/admin/networks/"+nid+"/code", "secret", nil, &resetBody)
	resp = doJSON(t, http.MethodPost, ts.URL+"/api/v1/networks/"+nid+"/join", "",
		map[string]any{"code": resetBody["code"], "publicKey": "CCC=="}, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("new code join = %d", resp.StatusCode)
	}

	// delete the network
	resp = doJSON(t, http.MethodDelete, ts.URL+"/admin/networks/"+nid, "secret", nil, nil)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("delete network = %d", resp.StatusCode)
	}
	resp = doJSON(t, http.MethodGet, ts.URL+"/admin/networks", "secret", nil, &nets)
	if len(nets) != 0 {
		t.Fatalf("networks after delete = %d", len(nets))
	}
}

func TestCreateRateLimit(t *testing.T) {
	ts, _ := newTestServerOpts(t, Options{CreatePerHour: 5})
	for i := 0; i < 5; i++ {
		resp := doJSON(t, http.MethodPost, ts.URL+"/api/v1/networks", "",
			map[string]any{"publicKey": fmt.Sprintf("KEY-%d", i)}, nil)
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("create %d = %d, want 201", i, resp.StatusCode)
		}
	}
	resp := doJSON(t, http.MethodPost, ts.URL+"/api/v1/networks", "",
		map[string]any{"publicKey": "KEY-6"}, nil)
	if resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("6th create = %d, want 429", resp.StatusCode)
	}
}

func TestJoinRateLimit(t *testing.T) {
	ts, _ := newTestServerOpts(t, Options{JoinPerMinute: 30})
	var created map[string]any
	doJSON(t, http.MethodPost, ts.URL+"/api/v1/networks", "",
		map[string]any{"publicKey": "AAA=="}, &created)
	nid := created["networkId"].(string)
	code := created["pairingCode"].(string)

	for i := 0; i < 30; i++ {
		resp := doJSON(t, http.MethodPost, ts.URL+"/api/v1/networks/"+nid+"/join", "",
			map[string]any{"code": code, "publicKey": fmt.Sprintf("KEY-%d", i)}, nil)
		if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("join %d = %d", i, resp.StatusCode)
		}
	}
	resp := doJSON(t, http.MethodPost, ts.URL+"/api/v1/networks/"+nid+"/join", "",
		map[string]any{"code": code, "publicKey": "KEY-31"}, nil)
	if resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("over-limit join = %d, want 429", resp.StatusCode)
	}
}

func TestTrustProxy(t *testing.T) {
	ts, _ := newTestServerOpts(t, Options{TrustProxy: true, CreatePerHour: 5})
	for i := 0; i < 5; i++ {
		resp := doJSON(t, http.MethodPost, ts.URL+"/api/v1/networks", "",
			map[string]any{"publicKey": fmt.Sprintf("KEY-%d", i)}, nil)
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("create %d = %d", i, resp.StatusCode)
		}
	}
	// real client (127.0.0.1, no XFF) now blocked
	resp := doJSON(t, http.MethodPost, ts.URL+"/api/v1/networks", "",
		map[string]any{"publicKey": "KEY-X"}, nil)
	if resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("real client should be blocked, got %d", resp.StatusCode)
	}

	// spoofed XFF from a fresh IP bypasses the local limit (trusted proxy)
	var buf bytes.Buffer
	_ = json.NewEncoder(&buf).Encode(map[string]any{"publicKey": "KEY-Y"})
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/v1/networks", &buf)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Forwarded-For", "203.0.113.99")
	r, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Body.Close()
	if r.StatusCode != http.StatusCreated {
		t.Fatalf("spoofed XFF should be allowed, got %d", r.StatusCode)
	}
}

func TestAdminCreateNetwork(t *testing.T) {
	ts, s := newTestServerOpts(t, Options{AdminToken: "secret"})

	var created protocol.AdminCreateNetworkResp
	resp := doJSON(t, http.MethodPost, ts.URL+"/admin/networks", "secret",
		map[string]any{"name": "公司内网"}, &created)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("admin create = %d %+v", resp.StatusCode, created)
	}
	if created.NetworkID == "" || created.PairingCode == "" {
		t.Fatalf("create response missing fields: %+v", created)
	}

	// listed as managed, no owner, zero nodes
	var nets []networkSummary
	doJSON(t, http.MethodGet, ts.URL+"/admin/networks", "secret", nil, &nets)
	if len(nets) != 1 || !nets[0].Network.Managed || nets[0].NodeCount != 0 || nets[0].Network.OwnerDeviceID != "" {
		t.Fatalf("managed summary wrong: %+v", nets)
	}

	// pairing code allows more joins than codeMaxUse (unlimited budget)
	for i := 0; i < 8; i++ {
		resp := doJSON(t, http.MethodPost, ts.URL+"/api/v1/networks/"+created.NetworkID+"/join", "",
			map[string]any{"code": created.PairingCode, "publicKey": fmt.Sprintf("KEY-%d", i),
				"deviceId": fmt.Sprintf("dev-%d", i)}, nil)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("join %d = %d", i, resp.StatusCode)
		}
	}

	// a managed network cannot be claimed by a joining client
	var joined protocol.JoinResp
	doJSON(t, http.MethodPost, ts.URL+"/api/v1/networks/"+created.NetworkID+"/join", "",
		map[string]any{"code": created.PairingCode, "publicKey": "CLAIMKEY", "deviceId": "claim-dev"}, &joined)
	resp = doJSON(t, http.MethodPost, ts.URL+"/api/v1/networks/"+created.NetworkID+"/claim", joined.Token,
		map[string]any{"deviceId": "claim-dev"}, nil)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("claim on managed = %d, want 403", resp.StatusCode)
	}
	doJSON(t, http.MethodGet, ts.URL+"/admin/networks", "secret", nil, &nets)
	if nets[0].Network.OwnerDeviceID != "" || !nets[0].Network.Managed {
		t.Fatalf("managed network was claimed: %+v", nets[0].Network)
	}
	_ = s
}

func TestManagedExemptFromSweep(t *testing.T) {
	ts, s := newTestServerOpts(t, Options{AdminToken: "secret"})

	var managed protocol.AdminCreateNetworkResp
	resp := doJSON(t, http.MethodPost, ts.URL+"/admin/networks", "secret",
		map[string]any{"name": "managed"}, &managed)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("admin create = %d", resp.StatusCode)
	}
	created, err := s.CreateNetwork("AAA==", "dev-owner", "", "", false)
	if err != nil {
		t.Fatal(err)
	}

	// age both networks far beyond any reasonable TTL
	old := time.Now().Add(-200 * time.Hour).Unix()
	for _, nid := range []string{managed.NetworkID, created.NetworkID} {
		ns := s.networks[nid]
		ns.lastActivityAt = old
		ns.n.LastActivityAt = old
	}
	victims, err := s.SweepZombies(72 * time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if len(victims) != 1 || victims[0] != created.NetworkID {
		t.Fatalf("victims = %v, want only %s", victims, created.NetworkID)
	}
	if s.networks[managed.NetworkID] == nil {
		t.Fatal("managed network was reaped by zombie sweep")
	}
	if s.networks[created.NetworkID] != nil {
		t.Fatal("client network survived zombie sweep")
	}
}

func TestAdminEditSettings(t *testing.T) {
	ts, s := newTestServerOpts(t, Options{AdminToken: "secret"})

	created, err := s.CreateNetwork("AAA==", "dev-owner", "", "", false)
	if err != nil {
		t.Fatal(err)
	}

	// rename + require approval on a client-owned network (owner check bypassed)
	resp := doJSON(t, http.MethodPatch, ts.URL+"/admin/networks/"+created.NetworkID, "secret",
		map[string]any{"name": "新名字", "approvalRequired": true}, nil)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("admin patch = %d", resp.StatusCode)
	}
	ns := s.networks[created.NetworkID]
	if ns.n.Name != "新名字" || ns.n.ApprovalRequired != true {
		t.Fatalf("settings not applied: %+v", ns.n)
	}

	// subnet change re-allocates node IPs
	resp = doJSON(t, http.MethodPatch, ts.URL+"/admin/networks/"+created.NetworkID, "secret",
		map[string]any{"subnet": "172.16.9.0/24"}, nil)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("subnet patch = %d", resp.StatusCode)
	}
	ip := s.networks[created.NetworkID].nodes[created.NodeID].IP
	if !strings.HasPrefix(ip, "172.16.9.") {
		t.Fatalf("reassign failed: %s", ip)
	}

	// PATCH on a missing network -> 404
	resp = doJSON(t, http.MethodPatch, ts.URL+"/admin/networks/NOPE", "secret",
		map[string]any{"name": "x"}, nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("patch missing = %d, want 404", resp.StatusCode)
	}
}

func TestAdminApproveDeny(t *testing.T) {
	ts, _ := newTestServerOpts(t, Options{AdminToken: "secret"})

	var created map[string]any
	resp := doJSON(t, http.MethodPost, ts.URL+"/api/v1/networks", "",
		map[string]any{"publicKey": "AAA==", "deviceId": "dev-owner", "approvalRequired": true}, &created)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create = %d", resp.StatusCode)
	}
	nid := created["networkId"].(string)
	code := created["pairingCode"].(string)

	var joined protocol.JoinResp
	resp = doJSON(t, http.MethodPost, ts.URL+"/api/v1/networks/"+nid+"/join", "",
		map[string]any{"code": code, "publicKey": "BBB==", "deviceId": "dev-joiner"}, &joined)
	if resp.StatusCode != http.StatusOK || joined.Status != "pending" {
		t.Fatalf("join = %d %+v", resp.StatusCode, joined)
	}

	// admin lists pending requests
	var pending []protocol.PendingNode
	resp = doJSON(t, http.MethodGet, ts.URL+"/admin/networks/"+nid+"/pending", "secret", nil, &pending)
	if resp.StatusCode != http.StatusOK || len(pending) != 1 {
		t.Fatalf("pending list = %d %+v", resp.StatusCode, pending)
	}

	// admin approves
	var approved protocol.PendingStatusResp
	resp = doJSON(t, http.MethodPost, ts.URL+"/admin/networks/"+nid+"/pending/"+pending[0].ID+"/approve", "secret", nil, &approved)
	if resp.StatusCode != http.StatusOK || approved.Status != "approved" || approved.IP == "" || approved.Token == "" {
		t.Fatalf("approve = %d %+v", resp.StatusCode, approved)
	}

	// second pending join then deny it
	var joined2 protocol.JoinResp
	doJSON(t, http.MethodPost, ts.URL+"/api/v1/networks/"+nid+"/join", "",
		map[string]any{"code": code, "publicKey": "CCC==", "deviceId": "dev-joiner2"}, &joined2)
	resp = doJSON(t, http.MethodPost, ts.URL+"/admin/networks/"+nid+"/pending/"+joined2.PendingID+"/deny", "secret", nil, nil)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("deny = %d", resp.StatusCode)
	}
	var st protocol.PendingStatusResp
	resp = doJSON(t, http.MethodGet, ts.URL+"/api/v1/pending/"+joined2.PendingID, "", nil, &st)
	if resp.StatusCode != http.StatusOK || st.Status != "denied" {
		t.Fatalf("denied status = %d %+v", resp.StatusCode, st)
	}
}

func TestAdminPasswordChange(t *testing.T) {
	ts, _ := newTestServerOpts(t, Options{AdminUser: "admin", AdminPass: "oldpass123"})

	var login protocol.AdminLoginResp
	resp := doJSON(t, http.MethodPost, ts.URL+"/admin/login", "",
		map[string]any{"username": "admin", "password": "oldpass123"}, &login)
	if resp.StatusCode != http.StatusOK || login.Token == "" {
		t.Fatalf("login = %d %+v", resp.StatusCode, login)
	}
	resp = doJSON(t, http.MethodGet, ts.URL+"/admin/networks", login.Token, nil, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("session list = %d", resp.StatusCode)
	}

	// wrong old password rejected
	resp = doJSON(t, http.MethodPost, ts.URL+"/admin/password", login.Token,
		map[string]any{"oldPassword": "wrong", "newPassword": "newpass123"}, nil)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("wrong old pw = %d, want 401", resp.StatusCode)
	}
	// too short rejected
	resp = doJSON(t, http.MethodPost, ts.URL+"/admin/password", login.Token,
		map[string]any{"oldPassword": "oldpass123", "newPassword": "short"}, nil)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("short new pw = %d, want 400", resp.StatusCode)
	}

	// change password
	resp = doJSON(t, http.MethodPost, ts.URL+"/admin/password", login.Token,
		map[string]any{"oldPassword": "oldpass123", "newPassword": "newpass456"}, nil)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("change pw = %d", resp.StatusCode)
	}
	// existing sessions invalidated
	resp = doJSON(t, http.MethodGet, ts.URL+"/admin/networks", login.Token, nil, nil)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("old session after change = %d, want 401", resp.StatusCode)
	}
	// old password no longer works
	resp = doJSON(t, http.MethodPost, ts.URL+"/admin/login", "",
		map[string]any{"username": "admin", "password": "oldpass123"}, nil)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("old pw login = %d, want 401", resp.StatusCode)
	}
	// new password works
	resp = doJSON(t, http.MethodPost, ts.URL+"/admin/login", "",
		map[string]any{"username": "admin", "password": "newpass456"}, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("new pw login = %d, want 200", resp.StatusCode)
	}
}

func TestAdminPasswordPersist(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "vnet.db")

	s, err := NewStoreAt(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.EnsureAdminPassword("admin", "bootstrap1"); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	// restart with a different flag password: the stored hash must win
	s2, err := NewStoreAt(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	if _, err := s2.EnsureAdminPassword("admin", "different2"); err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(NewHandler(s2, Options{AdminUser: "admin", AdminPass: "different2"}))
	defer ts.Close()

	resp := doJSON(t, http.MethodPost, ts.URL+"/admin/login", "",
		map[string]any{"username": "admin", "password": "bootstrap1"}, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("stored password should still work after restart, got %d", resp.StatusCode)
	}
	resp = doJSON(t, http.MethodPost, ts.URL+"/admin/login", "",
		map[string]any{"username": "admin", "password": "different2"}, nil)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("flag password must not override stored hash, got %d", resp.StatusCode)
	}
}

func TestAdminPasswordUnavailableForTokenAuth(t *testing.T) {
	ts, _ := newTestServerOpts(t, Options{AdminToken: "secret"})
	resp := doJSON(t, http.MethodPost, ts.URL+"/admin/password", "secret",
		map[string]any{"oldPassword": "x", "newPassword": "yyyyyyyy"}, nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("password route with token-only auth = %d, want 404", resp.StatusCode)
	}
}
