package server

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"snet/internal/protocol"
)

// ---- store-level tests ----

func TestSubnetAssignment(t *testing.T) {
	s := NewStore()

	created, err := s.CreateNetwork("AAA==", "dev-owner", "office", "192.168.5.0/24", false)
	if err != nil {
		t.Fatal(err)
	}
	if created.IP != "192.168.5.1" || created.Subnet != "192.168.5.0/24" {
		t.Fatalf("custom subnet create = %q/%q", created.IP, created.Subnet)
	}
	joined, err := s.Join(created.NetworkID, created.PairingCode, "BBB==", "dev-phone")
	if err != nil {
		t.Fatal(err)
	}
	if joined.IP != "192.168.5.2" {
		t.Fatalf("joiner ip = %q, want 192.168.5.2", joined.IP)
	}
	info, err := s.NetworkInfo(created.Token)
	if err != nil {
		t.Fatal(err)
	}
	if info.Name != "office" || info.OwnerDeviceID != "dev-owner" {
		t.Fatalf("info = %+v", info.Network)
	}
}

func TestOneNetworkPerDevice(t *testing.T) {
	s := NewStore()
	if _, err := s.CreateNetwork("AAA==", "dev-single", "", "", false); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateNetwork("BBB==", "dev-single", "", "", false); err == nil {
		t.Fatal("second create by same device should be rejected")
	}
	// a different device may still create
	if _, err := s.CreateNetwork("CCC==", "dev-other", "", "", false); err != nil {
		t.Fatalf("different device create rejected: %v", err)
	}
}

func TestSubnetValidation(t *testing.T) {
	s := NewStore()
	cases := map[string]bool{
		"8.8.8.0/24":     false, // public range
		"10.88.0.0/16":   true,
		"10.88.0.0/25":   false, // too small (prefix > 24)
		"192.168.1.99/24": false, // not a network address
		"2001:db8::/64":  false, // not IPv4
	}
	for subnet, ok := range cases {
		_, err := s.CreateNetwork("K==", "device-1", "", subnet, false)
		if (err == nil) != ok {
			t.Fatalf("subnet %q: ok=%v err=%v", subnet, ok, err)
		}
	}
}

func TestSubnetAutoAssignAndOverlap(t *testing.T) {
	s := NewStore()
	a, err := s.CreateNetwork("A==", "device-a", "", "", false)
	if err != nil {
		t.Fatal(err)
	}
	if a.Subnet != "10.88.0.0/24" {
		t.Fatalf("auto subnet 1 = %q", a.Subnet)
	}
	// explicit overlap rejected
	if _, err := s.CreateNetwork("B==", "device-b", "", "10.88.0.0/24", false); err == nil {
		t.Fatal("overlapping subnet should be rejected")
	}
	// next auto subnet skips the used block
	b, err := s.CreateNetwork("C==", "device-c", "", "", false)
	if err != nil {
		t.Fatal(err)
	}
	if b.Subnet != "10.88.1.0/24" {
		t.Fatalf("auto subnet 2 = %q", b.Subnet)
	}
	if a.IP == b.IP {
		t.Fatal("distinct networks share an address")
	}
}

func TestOwnerAuthorization(t *testing.T) {
	s := NewStore()
	created, err := s.CreateNetwork("AAA==", "dev-owner", "", "", false)
	if err != nil {
		t.Fatal(err)
	}
	joined, err := s.Join(created.NetworkID, created.PairingCode, "BBB==", "dev-intruder")
	if err != nil {
		t.Fatal(err)
	}

	// owner can rename
	if err := s.UpdateNetworkSettings(created.Token, "new-name", "", nil); err != nil {
		t.Fatalf("owner rename: %v", err)
	}
	// non-owner cannot
	if err := s.UpdateNetworkSettings(joined.Token, "hax", "", nil); err == nil {
		t.Fatal("non-owner rename should fail")
	}
	// non-owner cannot kick
	if err := s.KickNode(joined.Token, created.NodeID); err == nil {
		t.Fatal("non-owner kick should fail")
	}
	// non-owner cannot delete
	if err := s.DeleteNetwork(joined.Token); err == nil {
		t.Fatal("non-owner delete should fail")
	}
	// non-owner cannot reset code
	if _, err := s.ResetCode(joined.Token); err == nil {
		t.Fatal("non-owner reset should fail")
	}
	// owner cannot kick own node
	if err := s.KickNode(created.Token, created.NodeID); err == nil {
		t.Fatal("owner kicking own node should fail")
	}
	// owner kicks the joiner
	if err := s.KickNode(created.Token, joined.NodeID); err != nil {
		t.Fatalf("owner kick: %v", err)
	}
	if _, err := s.ListPeers(joined.Token); err == nil {
		t.Fatal("kicked node token should be dead")
	}
}

func TestLegacyClaimAndBind(t *testing.T) {
	s := NewStore()
	// legacy create: no device identity at all
	created, err := s.CreateNetwork("AAA==", "", "", "", false)
	if err != nil {
		t.Fatal(err)
	}
	if created.NetworkID == "" {
		t.Fatal("no network")
	}
	info, err := s.NetworkInfo(created.Token)
	if err == nil {
		t.Fatalf("unclaimed network must refuse owner ops, got %+v", info)
	}

	// bind the owner node to a device via its own token
	if err := s.SetNodeDevice(created.Token, created.NodeID, "dev-owner"); err != nil {
		t.Fatalf("bind: %v", err)
	}
	// other node cannot bind itself to someone else's token
	if _, err := s.Join(created.NetworkID, created.PairingCode, "BBB==", "dev-other"); err != nil {
		t.Fatal(err)
	}
	// claim as the bound device
	if err := s.ClaimNetwork(created.NetworkID, created.Token, "dev-owner"); err != nil {
		t.Fatalf("claim: %v", err)
	}
	// second claim fails
	if err := s.ClaimNetwork(created.NetworkID, created.Token, "dev-owner"); err == nil {
		t.Fatal("double claim should fail")
	}
	// now owner ops work
	if err := s.UpdateNetworkSettings(created.Token, "claimed", "", nil); err != nil {
		t.Fatalf("post-claim rename: %v", err)
	}
}

func TestDeviceRegistration(t *testing.T) {
	s := NewStore()
	if err := s.RegisterDevice("device-1", "PUB1==", ""); err != nil {
		t.Fatal(err)
	}
	if err := s.RegisterDevice("device-1", "PUB1b==", ""); err != nil {
		t.Fatalf("re-register should be idempotent: %v", err)
	}
	if err := s.RegisterDevice("bad id!", "PUB==", ""); err == nil {
		t.Fatal("invalid device id should be rejected")
	}
	devs := s.AdminDevices()
	if len(devs) != 1 || devs[0].PublicKey != "PUB1b==" {
		t.Fatalf("devices = %+v", devs)
	}

	// device appears in DeviceNetworks after joining
	c, _ := s.CreateNetwork("A==", "device-1", "", "", false)
	if _, err := s.Join(c.NetworkID, c.PairingCode, "B==", "device-1"); err != nil {
		t.Fatal(err)
	}
	nets := s.DeviceNetworks("device-1")
	if len(nets) != 1 || nets[0].ID != c.NetworkID {
		t.Fatalf("device networks = %+v", nets)
	}
}

func TestZombieSweep(t *testing.T) {
	s := NewStore()
	created, err := s.CreateNetwork("AAA==", "dev-owner", "", "", false)
	if err != nil {
		t.Fatal(err)
	}

	// fresh network survives an immediate sweep
	victims, err := s.SweepZombies(72 * time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if len(victims) != 0 {
		t.Fatalf("fresh network reaped: %v", victims)
	}
	// ttl <= 0 disables reaping
	if _, err := s.SweepZombies(0); err != nil {
		t.Fatal(err)
	}

	// simulate a network that went idle long ago
	old := time.Now().Add(-10 * time.Hour).Unix()
	s.mu.Lock()
	ns := s.networks[created.NetworkID]
	ns.lastActivityAt = old
	ns.n.LastActivityAt = old
	s.mu.Unlock()

	victims, err = s.SweepZombies(time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if len(victims) != 1 || victims[0] != created.NetworkID {
		t.Fatalf("idle network not reaped: %v", victims)
	}
	if _, err := s.ListPeers(created.Token); err == nil {
		t.Fatal("reaped network token still works")
	}
}

func TestRelayActivityKeepsNetworkAlive(t *testing.T) {
	s := NewStore()
	s.SetRelay("relay.test", 51820, 8)
	created, err := s.CreateNetwork("AAA==", "dev-owner", "", "", false)
	if err != nil {
		t.Fatal(err)
	}
	if created.RelayPort == 0 {
		t.Fatal("relay port not assigned")
	}

	// simulate a WG-only phone (no polling): network goes idle
	old := time.Now().Add(-2 * time.Hour).Unix()
	s.mu.Lock()
	ns := s.networks[created.NetworkID]
	ns.lastActivityAt = old
	ns.n.LastActivityAt = old
	s.mu.Unlock()

	s.MarkRelayActivity(created.RelayPort)
	// sweep must NOT reap it (activity refreshed via relay port)
	victims, err := s.SweepZombies(time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if len(victims) != 0 {
		t.Fatalf("relay-active network reaped: %v", victims)
	}

	// a different port does not protect it
	s.mu.Lock()
	ns.lastActivityAt = old
	ns.n.LastActivityAt = old
	s.mu.Unlock()
	s.MarkRelayActivity(created.RelayPort + 999)
	victims, err = s.SweepZombies(time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if len(victims) != 1 {
		t.Fatalf("wrong-port activity should not keep it alive: %v", victims)
	}
}

func TestLegacyDefaultsOnLoad(t *testing.T) {
	dir := t.TempDir()
	s, err := NewStoreAt(dir + "/snet.db")
	if err != nil {
		t.Fatal(err)
	}
	// simulate a legacy network record written before the v2 schema
	base, err := subnetBase(defaultSubnet)
	if err != nil {
		t.Fatal(err)
	}
	s.mu.Lock()
	s.networks["LEGACY01"] = &networkState{
		n:          protocol.Network{ID: "LEGACY01", OwnerNodeID: "OWNER000"},
		pairing:    &pairing{codeHash: "x"},
		nodes:      map[string]*protocol.Node{"OWNER000": {ID: "OWNER000", IP: "10.88.0.1"}},
		subnetBase: base,
	}
	s.mu.Unlock()
	legacy := networkRecord{
		ID:          "LEGACY01",
		OwnerNodeID: "OWNER000",
		CreatedAt:   time.Now().Add(-2 * time.Hour).UTC().Format(time.RFC3339),
	}
	if err := s.persistNetwork(s.networks["LEGACY01"]); err != nil {
		t.Fatal(err)
	}
	_ = legacy
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	s2, err := NewStoreAt(dir + "/snet.db")
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	ns := s2.networks["LEGACY01"]
	if ns == nil {
		t.Fatal("legacy network lost on reload")
	}
	if ns.n.Subnet != defaultSubnet {
		t.Fatalf("legacy subnet = %q, want %q", ns.n.Subnet, defaultSubnet)
	}
	if ns.lastActivityAt == 0 {
		t.Fatal("legacy lastActivityAt not backfilled")
	}
}

// ---- HTTP-level tests ----

func TestAdminLogin(t *testing.T) {
	ts := httptest.NewServer(NewHandler(NewStore(), Options{AdminUser: "root", AdminPass: "hunter2"}))
	defer ts.Close()

	// wrong credentials
	resp := doJSON(t, http.MethodPost, ts.URL+"/admin/login", "",
		protocol.AdminLoginReq{Username: "root", Password: "wrong"}, nil)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("wrong creds = %d, want 401", resp.StatusCode)
	}
	// valid login
	var login protocol.AdminLoginResp
	resp = doJSON(t, http.MethodPost, ts.URL+"/admin/login", "",
		protocol.AdminLoginReq{Username: "root", Password: "hunter2"}, &login)
	if resp.StatusCode != http.StatusOK || login.Token == "" || login.Expires == 0 {
		t.Fatalf("login = %d %+v", resp.StatusCode, login)
	}
	// session token works on admin routes
	resp = doJSON(t, http.MethodGet, ts.URL+"/admin/networks", login.Token, nil, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("session auth = %d", resp.StatusCode)
	}
	// garbage token rejected
	resp = doJSON(t, http.MethodGet, ts.URL+"/admin/networks", "garbage", nil, nil)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("garbage token = %d, want 401", resp.StatusCode)
	}
}

func TestAdminLoginRateLimit(t *testing.T) {
	ts := httptest.NewServer(NewHandler(NewStore(), Options{AdminUser: "root", AdminPass: "pw"}))
	defer ts.Close()
	for i := 0; i < 5; i++ {
		doJSON(t, http.MethodPost, ts.URL+"/admin/login", "",
			protocol.AdminLoginReq{Username: "root", Password: "nope"}, nil)
	}
	resp := doJSON(t, http.MethodPost, ts.URL+"/admin/login", "",
		protocol.AdminLoginReq{Username: "root", Password: "pw"}, nil)
	if resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("6th login = %d, want 429", resp.StatusCode)
	}
}

func TestStaticTokenStillWorks(t *testing.T) {
	ts := httptest.NewServer(NewHandler(NewStore(), Options{AdminToken: "static", AdminUser: "root", AdminPass: "pw"}))
	defer ts.Close()
	resp := doJSON(t, http.MethodGet, ts.URL+"/admin/networks", "static", nil, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("static token = %d, want 200", resp.StatusCode)
	}
}

func TestOwnerHTTPEndpoints(t *testing.T) {
	ts, _ := newTestServer(t)

	var created protocol.CreateNetworkResp
	resp := doJSON(t, http.MethodPost, ts.URL+"/api/v1/networks", "",
		protocol.CreateNetworkReq{PublicKey: "AAA==", DeviceID: "dev-owner", Name: "office", Subnet: "10.99.0.0/24"}, &created)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create = %d", resp.StatusCode)
	}
	var joined protocol.JoinResp
	resp = doJSON(t, http.MethodPost, ts.URL+"/api/v1/networks/"+created.NetworkID+"/join", "",
		protocol.JoinReq{Code: created.PairingCode, PublicKey: "BBB==", DeviceID: "dev-phone"}, &joined)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("join = %d", resp.StatusCode)
	}

	// network info (owner)
	var info protocol.NetworkInfoResp
	resp = doJSON(t, http.MethodGet, ts.URL+"/api/v1/networks/"+created.NetworkID, created.Token, nil, &info)
	if resp.StatusCode != http.StatusOK || info.Name != "office" || len(info.Nodes) != 2 {
		t.Fatalf("info = %d %+v", resp.StatusCode, info)
	}
	// network info (non-owner) -> 404
	resp = doJSON(t, http.MethodGet, ts.URL+"/api/v1/networks/"+created.NetworkID, joined.Token, nil, nil)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("non-owner info = %d, want 401", resp.StatusCode)
	}
	// owner kick via members path
	resp = doJSON(t, http.MethodDelete, ts.URL+"/api/v1/networks/"+created.NetworkID+"/members/"+joined.NodeID, created.Token, nil, nil)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("kick = %d", resp.StatusCode)
	}
	// reset code (owner)
	var reset protocol.ResetCodeResp
	resp = doJSON(t, http.MethodPost, ts.URL+"/api/v1/networks/"+created.NetworkID+"/code", created.Token, nil, &reset)
	if resp.StatusCode != http.StatusOK || reset.PairingCode == "" {
		t.Fatalf("reset = %d", resp.StatusCode)
	}
	// rename (owner)
	resp = doJSON(t, http.MethodPatch, ts.URL+"/api/v1/networks/"+created.NetworkID, created.Token,
		protocol.NetworkSettingsReq{Name: "renamed"}, nil)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("rename = %d", resp.StatusCode)
	}
	// delete (owner)
	resp = doJSON(t, http.MethodDelete, ts.URL+"/api/v1/networks/"+created.NetworkID, created.Token, nil, nil)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("delete = %d", resp.StatusCode)
	}
}

func TestDeviceBindAndClaimHTTP(t *testing.T) {
	ts, _ := newTestServer(t)
	var created protocol.CreateNetworkResp
	doJSON(t, http.MethodPost, ts.URL+"/api/v1/networks", "",
		protocol.CreateNetworkReq{PublicKey: "AAA=="}, &created)

	// bind node to device
	resp := doJSON(t, http.MethodPost, ts.URL+"/api/v1/networks/"+created.NetworkID+"/nodes/"+created.NodeID+"/device",
		created.Token, protocol.SetNodeDeviceReq{DeviceID: "dev-owner"}, nil)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("bind = %d", resp.StatusCode)
	}
	// claim
	resp = doJSON(t, http.MethodPost, ts.URL+"/api/v1/networks/"+created.NetworkID+"/claim",
		created.Token, protocol.ClaimReq{DeviceID: "dev-owner"}, nil)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("claim = %d", resp.StatusCode)
	}
	// register device endpoint
	resp = doJSON(t, http.MethodPost, ts.URL+"/api/v1/devices", "",
		protocol.RegisterDeviceReq{DeviceID: "dev-owner", PublicKey: "AAA=="}, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("register = %d", resp.StatusCode)
	}
}

func TestAdminDevicesEndpoint(t *testing.T) {
	ts, _ := newTestServerOpts(t, Options{AdminToken: "secret"})
	var created protocol.CreateNetworkResp
	doJSON(t, http.MethodPost, ts.URL+"/api/v1/networks", "",
		protocol.CreateNetworkReq{PublicKey: "AAA==", DeviceID: "dev-owner"}, &created)

	var devs protocol.AdminDevicesResp
	resp := doJSON(t, http.MethodGet, ts.URL+"/admin/devices", "secret", nil, &devs)
	if resp.StatusCode != http.StatusOK || len(devs.Devices) != 1 || devs.Devices[0].ID != "dev-owner" {
		t.Fatalf("devices = %d %+v", resp.StatusCode, devs)
	}

	var nets protocol.NetworksResp
	resp = doJSON(t, http.MethodGet, ts.URL+"/admin/devices/dev-owner/networks", "secret", nil, &nets)
	if resp.StatusCode != http.StatusOK || len(nets.Networks) != 1 {
		t.Fatalf("device networks = %d %+v", resp.StatusCode, nets)
	}
}
