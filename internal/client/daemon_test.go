package client

import (
	"net"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"snet/internal/protocol"
	"snet/internal/server"
)

func newTestDaemon(t *testing.T) (*Daemon, string) {
	t.Helper()
	cfgPath := filepath.Join(t.TempDir(), "config.json")
	cfg, err := LoadConfigAt(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	d := NewDaemonAt(cfg, cfgPath)
	d.SetDeviceIDFile("")
	return d, cfgPath
}

func TestConfigMigrationFromV1(t *testing.T) {
	v1 := `{
  "serverAddr": "https://snet.test",
  "wireguardPort": 51820,
  "privateKey": "aa",
  "networkId": "net123",
  "nodeId": "node1",
  "ip": "10.88.0.5",
  "token": "tok",
  "pairingCode": "pair"
}`
	cfg, err := parseConfig([]byte(v1))
	if err != nil {
		t.Fatal(err)
	}
	nc := cfg.Networks["net123"]
	if nc == nil {
		t.Fatal("legacy network not migrated")
	}
	if nc.NodeID != "node1" || nc.IP != "10.88.0.5" || nc.Token != "tok" || nc.PairingCode != "pair" {
		t.Fatalf("migrated cfg wrong: %+v", nc)
	}
	if nc.Subnet != defaultSubnet || !nc.Active || nc.Port != 51820 {
		t.Fatalf("migrated defaults wrong: %+v", nc)
	}
	if cfg.PrivateKey != "aa" || cfg.ServerAddr != "https://snet.test" {
		t.Fatalf("top-level fields lost: %+v", cfg)
	}
}

// TestNetworkErrorSurfacedInStatus verifies a tunnel bring-up failure is
// recorded and surfaced by the status API, and cleared when the network is
// left, so the UI never shows a network as healthy-but-dead.
func TestNetworkErrorSurfacedInStatus(t *testing.T) {
	d, _ := newTestDaemon(t)
	d.mu.Lock()
	d.cfg.Networks = map[string]*NetworkCfg{
		"n1": {NodeID: "n1", IP: "10.0.0.1", Token: "t", Subnet: "10.0.0.0/24", Active: true},
	}
	d.netErrs["n1"] = "create tun: operation not permitted"
	d.mu.Unlock()

	st, err := d.Status()
	if err != nil {
		t.Fatal(err)
	}
	nets, _ := st["networks"].([]map[string]any)
	if len(nets) != 1 {
		t.Fatalf("networks = %v", nets)
	}
	if nets[0]["active"] != true {
		t.Fatalf("active should stay true (intent kept): %v", nets[0])
	}
	if nets[0]["interface"] != "" {
		t.Fatalf("interface should be empty: %v", nets[0])
	}
	if !strings.Contains(nets[0]["error"].(string), "operation not permitted") {
		t.Fatalf("error not surfaced: %v", nets[0])
	}

	// leaving the network clears the recorded error
	d.mu.Lock()
	d.leaveLocked("n1")
	d.mu.Unlock()
	if _, ok := d.netErrs["n1"]; ok {
		t.Fatal("netErrs not cleared on leave")
	}
	st, _ = d.Status()
	nets, _ = st["networks"].([]map[string]any)
	if nets[0]["error"] != "" {
		t.Fatalf("error should be cleared after leave: %v", nets[0])
	}
}

// TestRetryScheduling verifies a failed bring-up schedules a background retry
// that is cancelled when the daemon closes.
func TestRetryScheduling(t *testing.T) {
	d, _ := newTestDaemon(t)
	d.mu.Lock()
	d.cfg.Networks = map[string]*NetworkCfg{
		"n1": {NodeID: "n1", IP: "10.0.0.1", Token: "t", Subnet: "10.0.0.0/24", Active: true},
	}
	d.scheduleRetryLocked("n1")
	d.mu.Unlock()

	d.mu.Lock()
	if _, ok := d.retryPending["n1"]; !ok {
		t.Fatal("n1 not scheduled")
	}
	if d.retryStop == nil {
		t.Fatal("retry loop not started")
	}
	d.mu.Unlock()

	d.Close()
	d.mu.Lock()
	defer d.mu.Unlock()
	if len(d.retryPending) != 0 {
		t.Fatalf("retry pending not cleared on close: %v", d.retryPending)
	}
	if d.retryStop != nil {
		t.Fatal("retry loop not stopped on close")
	}
}

func TestConfigRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	c := &Config{
		ServerAddr: "https://snet.test",
		Networks: map[string]*NetworkCfg{
			"a": {NodeID: "n1", IP: "10.0.0.2", Token: "t", Subnet: "10.0.0.0/24", Active: true},
		},
	}
	if err := c.SaveAt(path); err != nil {
		t.Fatal(err)
	}
	c2, err := LoadConfigAt(path)
	if err != nil {
		t.Fatal(err)
	}
	if c2.Networks["a"].Token != "t" || c2.Networks["a"].Subnet != "10.0.0.0/24" {
		t.Fatalf("round trip wrong: %+v", c2.Networks["a"])
	}
}

// TestConfigStaleServerCleared verifies the load-time cleanup: a legacy device
// with no binding, no networks and no pending joins has no meaningful server
// address and gets it dropped.
func TestConfigStaleServerCleared(t *testing.T) {
	// legacy unbound config with no networks: stale address dropped
	legacy := []byte(`{"serverAddr":"https://stale.example","privateKey":"aa"}`)
	cfg, err := parseConfig(legacy)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ServerAddr != "" {
		t.Fatalf("stale server not cleared: %q", cfg.ServerAddr)
	}
	// with networks the address is meaningful and must be kept
	withNet := []byte(`{"serverAddr":"https://snet.example","privateKey":"aa","networks":{"n1":{"nodeId":"x","ip":"1.1.1.1","token":"t","active":true}}}`)
	cfg2, err := parseConfig(withNet)
	if err != nil {
		t.Fatal(err)
	}
	if cfg2.ServerAddr != "https://snet.example" {
		t.Fatalf("server dropped despite networks: %q", cfg2.ServerAddr)
	}
}

// TestNormalizeServer covers trailing-slash normalization and scheme-less
// server addresses used for switch/rollback comparisons.
func TestNormalizeServer(t *testing.T) {
	cases := []struct{ in, want string }{
		{"", ""},
		{"https://snet.example", "https://snet.example"},
		{"https://snet.example/", "https://snet.example"},
		{"http://127.0.0.1:8090", "http://127.0.0.1:8090"},
		{"http://127.0.0.1:8090//", "http://127.0.0.1:8090"},
		{"snet.example", "https://snet.example"},
	}
	for _, c := range cases {
		if got := normalizeServer(c.in); got != c.want {
			t.Fatalf("normalizeServer(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestBindRoundTrip verifies the daemon bind flow against a server with the
// enrollment gate on: binding with an admin code unlocks create/join, and
// unbound devices are refused with the enrollment hint.
func TestBindRoundTrip(t *testing.T) {
	s := server.NewStore()
	ts := httptest.NewServer(server.NewHandler(s, server.Options{RequireDeviceAuth: true}))
	defer ts.Close()
	codes, _, err := s.AdminGenerateAuthCodes(2, 1)
	if err != nil {
		t.Fatal(err)
	}

	d, _ := newTestDaemon(t)
	if err := d.Bind(ts.URL, "", codes[0]); err != nil {
		t.Fatalf("bind: %v", err)
	}
	if !d.cfg.Bound() {
		t.Fatalf("Bound() false after bind: %+v", d.cfg)
	}
	st, err := d.Status()
	if err != nil {
		t.Fatal(err)
	}
	if st["bound"] != true {
		t.Fatalf("status after bind = %v", st)
	}

	// enrollment gate now satisfied: creating a network works
	created, err := d.Create(ts.URL, 0, "home", "", false)
	if err != nil {
		t.Fatalf("create after bind: %v", err)
	}
	if created.NetworkID == "" {
		t.Fatal("empty network id")
	}

	// an unbound device is refused by the gate with the hint text
	d2, _ := newTestDaemon(t)
	if _, err := d2.Create(ts.URL, 0, "x", "", false); err == nil || !strings.Contains(err.Error(), "设备未授权") {
		t.Fatalf("unbound create err = %v, want 设备未授权 hint", err)
	}
	// after binding its own code, the same device joins the owner's network
	if err := d2.Bind(ts.URL, "", codes[1]); err != nil {
		t.Fatalf("bind d2: %v", err)
	}
	join, err := d2.Join(ts.URL, 0, created.NetworkID, created.PairingCode)
	if err != nil {
		t.Fatalf("join after bind: %v", err)
	}
	if join.IP != "10.88.0.2" {
		t.Fatalf("joiner ip = %q, want 10.88.0.2", join.IP)
	}

	// switching servers is always allowed; the device belongs to exactly one
	// server at a time, so binding a new server clears the old one's networks
	sB := server.NewStore()
	tsB := httptest.NewServer(server.NewHandler(sB, server.Options{RequireDeviceAuth: true}))
	defer tsB.Close()
	codesB, _, err := sB.AdminGenerateAuthCodes(1, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := d2.Bind(tsB.URL, "", codesB[0]); err != nil {
		t.Fatalf("switch server bind: %v", err)
	}
	if len(d2.cfg.Networks) != 0 {
		t.Fatalf("networks not cleared after server switch: %+v", d2.cfg.Networks)
	}
	if !d2.cfg.Bound() || d2.cfg.ServerAddr != normalizeServer(tsB.URL) {
		t.Fatalf("not bound to the new server: %+v", d2.cfg)
	}
}

// TestJoinViaLinkServer verifies that an invite link carrying its own server
// address targets that server on join: a device already bound to that server
// joins directly, an unbound device on a non-enforcement server joins (server
// switched), and an unbound device against an enforcement server is refused
// with the enrollment hint (the frontend gates this with a bind step).
func TestJoinViaLinkServer(t *testing.T) {
	s := server.NewStore()
	ts := httptest.NewServer(server.NewHandler(s, server.Options{}))
	defer ts.Close()
	d, _ := newTestDaemon(t)

	created, err := d.Create(ts.URL, 0, "home", "", false)
	if err != nil {
		t.Fatal(err)
	}
	link := protocol.BuildLink(created.NetworkID, created.PairingCode, ts.URL, "")

	// an unbound device joins the network named by the link, and ends up
	// pointed at the link's server (non-enforcement server: no bind needed)
	d2, _ := newTestDaemon(t)
	nid, code, linkServer, _, err := protocol.ParseLink(link)
	if err != nil {
		t.Fatal(err)
	}
	join, err := d2.Join(linkServer, 0, nid, code)
	if err != nil {
		t.Fatalf("join via link server: %v", err)
	}
	if join.IP != "10.88.0.2" {
		t.Fatalf("joiner ip = %q, want 10.88.0.2", join.IP)
	}
	if join.Name != "home" {
		t.Fatalf("joiner name = %q, want home", join.Name)
	}
	if got := d2.cfg.Networks[join.NetworkID]; got == nil || got.Name != "home" {
		t.Fatalf("joiner stored name = %+v, want home", got)
	}
	if d2.cfg.ServerAddr != normalizeServer(ts.URL) {
		t.Fatalf("device not pointed at link server: %+v", d2.cfg)
	}
	if d2.cfg.Bound() {
		t.Fatalf("bound() should be false on a non-enforcement join: %+v", d2.cfg)
	}

	// against an enforcement server an unbound device is refused; binding with
	// an auth code first unlocks the same join (the frontend bind-then-join path)
	se := server.NewStore()
	te := httptest.NewServer(server.NewHandler(se, server.Options{RequireDeviceAuth: true}))
	defer te.Close()
	codes, _, err := se.AdminGenerateAuthCodes(1, 1)
	if err != nil {
		t.Fatal(err)
	}
	d3, _ := newTestDaemon(t)
	if err := d3.Bind(te.URL, "", codes[0]); err != nil {
		t.Fatalf("bind: %v", err)
	}
	createdE, err := d3.Create(te.URL, 0, "secure", "", false)
	if err != nil {
		t.Fatal(err)
	}
	linkE := protocol.BuildLink(createdE.NetworkID, createdE.PairingCode, te.URL, "")

	d4, _ := newTestDaemon(t)
	nid, code, linkServer, _, err = protocol.ParseLink(linkE)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d4.Join(linkServer, 0, nid, code); err == nil || !strings.Contains(err.Error(), "设备未授权") {
		t.Fatalf("unbound join against enforcement server err = %v, want 设备未授权", err)
	}
}

func TestSubnetsOverlap(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{
		{"10.88.0.0/24", "10.88.0.0/24", true},
		{"10.88.0.0/24", "10.88.0.0/23", true},
		{"10.88.0.0/24", "10.88.1.0/24", false},
		{"10.88.0.0/24", "192.168.1.0/24", false},
	}
	for _, c := range cases {
		if got := subnetsOverlap(c.a, c.b); got != c.want {
			t.Errorf("subnetsOverlap(%s, %s) = %v, want %v", c.a, c.b, got, c.want)
		}
	}
}

func TestCreateJoinRoundTrip(t *testing.T) {
	ts := httptest.NewServer(server.NewHandler(server.NewStore(), server.Options{}))
	defer ts.Close()
	d, _ := newTestDaemon(t)

	// owner create
	resp, err := d.Create(ts.URL, 51820, "home", "", false)
	if err != nil {
		t.Fatal(err)
	}
	if resp.NetworkID == "" || resp.Token == "" || resp.Subnet == "" {
		t.Fatalf("create resp incomplete: %+v", resp)
	}
	if d.cfg.DeviceID == "" {
		t.Fatal("device id not assigned")
	}
	if !d.cfg.Networks[resp.NetworkID].Owner {
		t.Fatal("owner network not marked owner")
	}

	// second network join on the same server
	join, err := d.Join(ts.URL, 0, resp.NetworkID, resp.PairingCode)
	if err == nil {
		t.Fatalf("join own network should fail, got %+v", join)
	}
	// owner cannot create a second network (one network per device)
	if _, err := d.Create(ts.URL, 0, "office", "192.168.9.0/24", false); err == nil {
		t.Fatal("owner daemon should not be able to create a second network")
	}
	// a fresh daemon (different device) creates a second network, daemon d2 joins it
	d3, d3path := newTestDaemon(t)
	ownerResp, err := d3.Create(ts.URL, 0, "office", "192.168.9.0/24", false)
	if err != nil {
		t.Fatal(err)
	}
	if ownerResp.Subnet != "192.168.9.0/24" {
		t.Fatalf("explicit subnet ignored: %s", ownerResp.Subnet)
	}

	d2, _ := newTestDaemon(t)
	join, err = d2.Join(ts.URL, 0, ownerResp.NetworkID, ownerResp.PairingCode)
	if err != nil {
		t.Fatal(err)
	}
	if join.IP == "" {
		t.Fatal("no ip assigned on join")
	}
	if d2.cfg.Networks[join.NetworkID].Owner {
		t.Fatal("non-owner marked owner")
	}

	// leave keeps the node; rejoin brings it back
	if err := d2.Leave(join.NetworkID); err != nil {
		t.Fatal(err)
	}
	if d2.cfg.Networks[join.NetworkID].Active {
		t.Fatal("leave did not deactivate")
	}
	if err := d2.Rejoin(join.NetworkID); err != nil {
		t.Fatal(err)
	}
	if !d2.cfg.Networks[join.NetworkID].Active {
		t.Fatal("rejoin did not reactivate")
	}

	// netinfo + rename + reset code via owner daemon
	info, err := d3.Info(ownerResp.NetworkID)
	if err != nil {
		t.Fatal(err)
	}
	if len(info.Nodes) < 1 {
		t.Fatalf("netinfo nodes: %+v", info)
	}
	if err := d3.UpdateSettings(ownerResp.NetworkID, "office-renamed", "", nil); err != nil {
		t.Fatal(err)
	}
	code, err := d3.ResetCode(ownerResp.NetworkID)
	if err != nil {
		t.Fatal(err)
	}
	if code == "" {
		t.Fatal("empty reset code")
	}

	// kick the second daemon's node
	if err := d3.Kick(ownerResp.NetworkID, join.NodeID); err != nil {
		t.Fatal(err)
	}

	// remove from second daemon
	if err := d2.Remove(join.NetworkID); err != nil {
		t.Fatal(err)
	}
	if _, ok := d2.cfg.Networks[join.NetworkID]; ok {
		t.Fatal("remove left network in config")
	}

	// owner deletes its first network
	if err := d.DeleteNetwork(resp.NetworkID); err != nil {
		t.Fatal(err)
	}
	if _, ok := d.cfg.Networks[resp.NetworkID]; ok {
		t.Fatal("delete left network in config")
	}

	// status snapshot shape
	st, err := d.Status()
	if err != nil {
		t.Fatal(err)
	}
	if st["deviceId"] != d.cfg.DeviceID {
		t.Fatalf("status deviceId wrong: %v", st["deviceId"])
	}
	if _, ok := st["networks"].([]map[string]any); !ok {
		t.Fatalf("status networks type wrong: %T", st["networks"])
	}

	// persisted config reflects state
	data, err := os.ReadFile(d3path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "office-renamed") {
		t.Fatalf("config not persisted with rename: %s", data)
	}
}

// TestEmptyServerDefaultsToBound verifies create/join with an empty server
// address use the currently bound server instead of switching or failing.
func TestEmptyServerDefaultsToBound(t *testing.T) {
	tsA := httptest.NewServer(server.NewHandler(server.NewStore(), server.Options{}))
	defer tsA.Close()
	tsB := httptest.NewServer(server.NewHandler(server.NewStore(), server.Options{}))
	defer tsB.Close()
	d, _ := newTestDaemon(t)

	// not bound to anything yet → empty server is refused with a clear message
	if _, err := d.Create("", 0, "n", "", false); err == nil || !strings.Contains(err.Error(), "未连接服务器") {
		t.Fatalf("unbound create err = %v, want 未连接服务器", err)
	}
	if _, err := d.Join("", 0, "nid", "code"); err == nil || !strings.Contains(err.Error(), "未连接服务器") {
		t.Fatalf("unbound join err = %v, want 未连接服务器", err)
	}

	created, err := d.Create(tsA.URL, 0, "net-a", "", false)
	if err != nil {
		t.Fatal(err)
	}
	if d.cfg.ServerAddr != normalizeServer(tsA.URL) {
		t.Fatalf("bound server = %q", d.cfg.ServerAddr)
	}
	// creating again with an empty server must target A (owner limit fires),
	// never attempt to "switch" away
	if _, err := d.Create("", 0, "net-a2", "", false); err == nil || !strings.Contains(err.Error(), "已创建网络") {
		t.Fatalf("empty-server create err = %v, want owner limit on A", err)
	}
	// joining with an empty server must also resolve to A
	if _, err := d.Join("", 0, "missing-nid", "badcode"); err == nil {
		t.Fatal("join with empty server should hit A and fail there")
	}
	if d.cfg.ServerAddr != normalizeServer(tsA.URL) {
		t.Fatalf("server drifted off A: %q", d.cfg.ServerAddr)
	}
	// tsB was never targeted by an empty-server call
	if _, ok := d.cfg.Networks[created.NetworkID]; !ok {
		t.Fatalf("A's network lost: %+v", d.cfg.Networks)
	}
}

func TestSwitchServerClearsNetworks(t *testing.T) {
	tsA := httptest.NewServer(server.NewHandler(server.NewStore(), server.Options{}))
	defer tsA.Close()
	tsB := httptest.NewServer(server.NewHandler(server.NewStore(), server.Options{}))
	defer tsB.Close()
	d, _ := newTestDaemon(t)
	createdA, err := d.Create(tsA.URL, 51820, "net-a", "", false)
	if err != nil {
		t.Fatal(err)
	}
	if len(d.cfg.Networks) != 1 {
		t.Fatalf("expected one network on A: %+v", d.cfg.Networks)
	}
	// switching to B is allowed and clears A's networks
	resp, err := d.Create(tsB.URL, 0, "net-b", "", false)
	if err != nil {
		t.Fatalf("create on B: %v", err)
	}
	if len(d.cfg.Networks) != 1 || d.cfg.Networks[resp.NetworkID] == nil {
		t.Fatalf("expected only B's network after switch: %+v", d.cfg.Networks)
	}
	if _, ok := d.cfg.Networks[createdA.NetworkID]; ok {
		t.Fatalf("A's network still present after switch: %+v", d.cfg.Networks)
	}
	if d.cfg.ServerAddr != normalizeServer(tsB.URL) {
		t.Fatalf("server not switched: %+v", d.cfg)
	}
	// the owner limit applies per server: after switching, A's owned network
	// is gone, so creating again on B is rejected for a different reason
	if _, err := d.Create(tsB.URL, 0, "net-b2", "", false); err == nil || !strings.Contains(err.Error(), "已创建网络") {
		t.Fatalf("second create on B err = %v, want owner limit", err)
	}
}

// TestFailedSwitchRollsBack verifies a failed switch to a new server (e.g. the
// enrollment gate refusing an unbound device) leaves the current server's
// address and networks fully intact.
func TestFailedSwitchRollsBack(t *testing.T) {
	tsA := httptest.NewServer(server.NewHandler(server.NewStore(), server.Options{}))
	defer tsA.Close()
	tsB := httptest.NewServer(server.NewHandler(server.NewStore(), server.Options{RequireDeviceAuth: true}))
	defer tsB.Close()
	d, _ := newTestDaemon(t)
	created, err := d.Create(tsA.URL, 0, "net-a", "", false)
	if err != nil {
		t.Fatal(err)
	}
	// unbound create on the enforcement server B is refused; A must survive
	if _, err := d.Create(tsB.URL, 0, "x", "", false); err == nil || !strings.Contains(err.Error(), "设备未授权") {
		t.Fatalf("unbound create on B err = %v, want 设备未授权", err)
	}
	if d.cfg.ServerAddr != normalizeServer(tsA.URL) {
		t.Fatalf("server address not rolled back: %+v", d.cfg)
	}
	if len(d.cfg.Networks) != 1 || d.cfg.Networks[created.NetworkID] == nil {
		t.Fatalf("A's networks lost after failed switch: %+v", d.cfg.Networks)
	}
}

// TestBindFailureRollsBackServer verifies a failed bind to a new server rolls
// back the server address and keeps the previous server's networks.
func TestBindFailureRollsBackServer(t *testing.T) {
	tsA := httptest.NewServer(server.NewHandler(server.NewStore(), server.Options{}))
	defer tsA.Close()
	sB := server.NewStore()
	tsB := httptest.NewServer(server.NewHandler(sB, server.Options{RequireDeviceAuth: true}))
	defer tsB.Close()
	d, _ := newTestDaemon(t)
	created, err := d.Create(tsA.URL, 0, "net-a", "", false)
	if err != nil {
		t.Fatal(err)
	}
	if err := d.Bind(tsB.URL, "", "WRONG-CODE"); err == nil {
		t.Fatal("bind with wrong code should fail")
	}
	if d.cfg.ServerAddr != normalizeServer(tsA.URL) {
		t.Fatalf("server address not rolled back: %+v", d.cfg)
	}
	if len(d.cfg.Networks) != 1 || d.cfg.Networks[created.NetworkID] == nil {
		t.Fatalf("A's networks lost after failed bind: %+v", d.cfg.Networks)
	}
	if d.cfg.Bound() {
		t.Fatalf("must not be bound after failed bind: %+v", d.cfg)
	}
}

// TestVerifyBindingDetectsRevocation verifies the periodic check clears the
// local bound state once the server (enrollment gate) no longer considers the
// device bound.
func TestVerifyBindingDetectsRevocation(t *testing.T) {
	s := server.NewStore()
	ts := httptest.NewServer(server.NewHandler(s, server.Options{RequireDeviceAuth: true}))
	defer ts.Close()
	codes, _, err := s.AdminGenerateAuthCodes(1, 1)
	if err != nil {
		t.Fatal(err)
	}
	d, _ := newTestDaemon(t)
	d.SetDeviceID("revoke-dev-1")
	if err := d.Bind(ts.URL, "", codes[0]); err != nil {
		t.Fatalf("bind: %v", err)
	}
	if !d.cfg.Bound() {
		t.Fatalf("not bound after bind: %+v", d.cfg)
	}
	if !d.verifyBinding() {
		t.Fatalf("verifyBinding false while still bound")
	}
	// admin revokes the binding → next check clears the local bound state
	if err := s.AdminUnbindDevice("revoke-dev-1"); err != nil {
		t.Fatal(err)
	}
	if d.verifyBinding() {
		t.Fatalf("verifyBinding should report the revocation")
	}
	if d.cfg.Bound() {
		t.Fatalf("Bound() still true after revocation: %+v", d.cfg)
	}
}

// TestVerifyBindingNonEnforcementExplicitFalse verifies revocation detection
// works on a server without the enrollment gate (via the explicit bound
// field in the register response).
func TestVerifyBindingNonEnforcementExplicitFalse(t *testing.T) {
	s := server.NewStore()
	ts := httptest.NewServer(server.NewHandler(s, server.Options{}))
	defer ts.Close()
	codes, _, err := s.AdminGenerateAuthCodes(1, 1)
	if err != nil {
		t.Fatal(err)
	}
	d, _ := newTestDaemon(t)
	d.SetDeviceID("revoke-dev-2")
	if err := d.Bind(ts.URL, "", codes[0]); err != nil {
		t.Fatalf("bind: %v", err)
	}
	if !d.cfg.Bound() {
		t.Fatalf("not bound after bind: %+v", d.cfg)
	}
	if err := s.AdminUnbindDevice("revoke-dev-2"); err != nil {
		t.Fatal(err)
	}
	if d.verifyBinding() {
		t.Fatalf("verifyBinding should detect the non-enforcement revocation")
	}
	if d.cfg.Bound() {
		t.Fatalf("Bound() still true after revocation: %+v", d.cfg)
	}
}

// TestVerifyBindingKeepsOnTransientError verifies a temporary network failure
// does not clear a valid binding.
func TestVerifyBindingKeepsOnTransientError(t *testing.T) {
	s := server.NewStore()
	ts := httptest.NewServer(server.NewHandler(s, server.Options{RequireDeviceAuth: true}))
	codes, _, err := s.AdminGenerateAuthCodes(1, 1)
	if err != nil {
		t.Fatal(err)
	}
	d, _ := newTestDaemon(t)
	d.SetDeviceID("revoke-dev-3")
	if err := d.Bind(ts.URL, "", codes[0]); err != nil {
		t.Fatalf("bind: %v", err)
	}
	ts.Close() // server goes away
	if !d.verifyBinding() {
		t.Fatalf("transient error must keep the binding: %+v", d.cfg)
	}
	if !d.cfg.Bound() {
		t.Fatalf("binding cleared on transient error: %+v", d.cfg)
	}
}

// TestCreateAfterRevocationClearsBinding verifies an immediate gated operation
// against the device's own bound server reports the revocation and clears the
// local bound state.
func TestCreateAfterRevocationClearsBinding(t *testing.T) {
	s := server.NewStore()
	ts := httptest.NewServer(server.NewHandler(s, server.Options{RequireDeviceAuth: true}))
	defer ts.Close()
	codes, _, err := s.AdminGenerateAuthCodes(1, 1)
	if err != nil {
		t.Fatal(err)
	}
	d, _ := newTestDaemon(t)
	d.SetDeviceID("revoke-dev-4")
	if err := d.Bind(ts.URL, "", codes[0]); err != nil {
		t.Fatalf("bind: %v", err)
	}
	if err := s.AdminUnbindDevice("revoke-dev-4"); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Create(ts.URL, 0, "x", "", false); err == nil || !strings.Contains(err.Error(), "设备未授权") {
		t.Fatalf("create after revocation err = %v, want 设备未授权", err)
	}
	if d.cfg.Bound() {
		t.Fatalf("Bound() still true after gated create: %+v", d.cfg)
	}
	if d.cfg.ServerAddr != normalizeServer(ts.URL) {
		t.Fatalf("server address changed: %+v", d.cfg)
	}
}

// TestForeignServerRevokeDoesNotClearBinding verifies a 403 from a server the
// device is NOT bound to does not erase the binding on the current server.
func TestForeignServerRevokeDoesNotClearBinding(t *testing.T) {
	sA := server.NewStore()
	tsA := httptest.NewServer(server.NewHandler(sA, server.Options{RequireDeviceAuth: true}))
	defer tsA.Close()
	codesA, _, err := sA.AdminGenerateAuthCodes(1, 1)
	if err != nil {
		t.Fatal(err)
	}
	tsB := httptest.NewServer(server.NewHandler(server.NewStore(), server.Options{RequireDeviceAuth: true}))
	defer tsB.Close()
	d, _ := newTestDaemon(t)
	d.SetDeviceID("revoke-dev-5")
	if err := d.Bind(tsA.URL, "", codesA[0]); err != nil {
		t.Fatalf("bind: %v", err)
	}
	if _, err := d.Create(tsB.URL, 0, "x", "", false); err == nil || !strings.Contains(err.Error(), "设备未授权") {
		t.Fatalf("create on B err = %v, want 设备未授权", err)
	}
	if !d.cfg.Bound() {
		t.Fatalf("binding to A must survive a 403 on B: %+v", d.cfg)
	}
	if d.cfg.ServerAddr != normalizeServer(tsA.URL) {
		t.Fatalf("server address not rolled back: %+v", d.cfg)
	}
}

func TestCreateOneNetworkPerDevice(t *testing.T) {
	ts := httptest.NewServer(server.NewHandler(server.NewStore(), server.Options{}))
	defer ts.Close()
	d, _ := newTestDaemon(t)
	if _, err := d.Create(ts.URL, 51820, "home", "", false); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Create(ts.URL, 0, "second", "", false); err == nil {
		t.Fatal("daemon guard: second create should be rejected")
	}
}

func TestPendingJoinFlow(t *testing.T) {
	old := pendingPollInterval
	pendingPollInterval = 50 * time.Millisecond
	defer func() { pendingPollInterval = old }()

	ts := httptest.NewServer(server.NewHandler(server.NewStore(), server.Options{}))
	defer ts.Close()

	owner, _ := newTestDaemon(t)
	created, err := owner.Create(ts.URL, 0, "office", "10.99.0.0/24", true)
	if err != nil {
		t.Fatal(err)
	}

	joiner, _ := newTestDaemon(t)
	joined, err := joiner.Join(ts.URL, 0, created.NetworkID, created.PairingCode)
	if err != nil {
		t.Fatal(err)
	}
	if joined.Status != "pending" || joined.PendingID == "" {
		t.Fatalf("join = %+v, want pending", joined)
	}
	// not attached to any network yet
	if len(joiner.cfg.Networks) != 0 {
		t.Fatalf("pending join must not attach a network: %+v", joiner.cfg.Networks)
	}
	if len(joiner.cfg.PendingJoins) != 1 {
		t.Fatalf("pending join not persisted: %+v", joiner.cfg.PendingJoins)
	}

	// owner sees the pending request
	info, err := owner.Info(created.NetworkID)
	if err != nil {
		t.Fatal(err)
	}
	if len(info.Pending) != 1 {
		t.Fatalf("owner pending count = %d", len(info.Pending))
	}
	pendingID := info.Pending[0].ID

	// owner approves; the joiner's poll loop attaches the network
	if err := owner.ApprovePending(created.NetworkID, pendingID); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		joiner.mu.Lock()
		nc := joiner.cfg.Networks[created.NetworkID]
		joiner.mu.Unlock()
		if nc != nil && nc.Active {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("joiner never attached after approval: %+v", joiner.cfg.Networks)
		}
		time.Sleep(20 * time.Millisecond)
	}
	joiner.mu.Lock()
	nc := joiner.cfg.Networks[created.NetworkID]
	joiner.mu.Unlock()
	if !strings.HasPrefix(nc.IP, "10.99.0.") {
		t.Fatalf("approved IP out of range: %s", nc.IP)
	}
	if len(joiner.cfg.PendingJoins) != 0 {
		t.Fatalf("pending join not cleaned up: %+v", joiner.cfg.PendingJoins)
	}

	// owner can now see two members
	info, err = owner.Info(created.NetworkID)
	if err != nil {
		t.Fatal(err)
	}
	if len(info.Nodes) != 2 {
		t.Fatalf("member count after approval = %d", len(info.Nodes))
	}
}

func TestPendingJoinDenied(t *testing.T) {
	old := pendingPollInterval
	pendingPollInterval = 50 * time.Millisecond
	defer func() { pendingPollInterval = old }()

	ts := httptest.NewServer(server.NewHandler(server.NewStore(), server.Options{}))
	defer ts.Close()

	owner, _ := newTestDaemon(t)
	created, err := owner.Create(ts.URL, 0, "office", "10.99.1.0/24", true)
	if err != nil {
		t.Fatal(err)
	}
	joiner, _ := newTestDaemon(t)
	if _, err := joiner.Join(ts.URL, 0, created.NetworkID, created.PairingCode); err != nil {
		t.Fatal(err)
	}

	info, err := owner.Info(created.NetworkID)
	if err != nil {
		t.Fatal(err)
	}
	if len(info.Pending) != 1 {
		t.Fatalf("owner pending count = %d", len(info.Pending))
	}
	if err := owner.DenyPending(created.NetworkID, info.Pending[0].ID); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		joiner.mu.Lock()
		pj := joiner.cfg.PendingJoins[info.Pending[0].ID]
		status := ""
		if pj != nil {
			status = pj.Status
		}
		joiner.mu.Unlock()
		if status == "denied" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("joiner never saw denial: %+v", joiner.cfg.PendingJoins)
		}
		time.Sleep(20 * time.Millisecond)
	}
	if len(joiner.cfg.Networks) != 0 {
		t.Fatalf("denied join must not attach a network: %+v", joiner.cfg.Networks)
	}
}

func TestCancelPending(t *testing.T) {
	ts := httptest.NewServer(server.NewHandler(server.NewStore(), server.Options{}))
	defer ts.Close()
	owner, _ := newTestDaemon(t)
	created, err := owner.Create(ts.URL, 0, "office", "10.99.2.0/24", true)
	if err != nil {
		t.Fatal(err)
	}
	joiner, _ := newTestDaemon(t)
	joined, err := joiner.Join(ts.URL, 0, created.NetworkID, created.PairingCode)
	if err != nil {
		t.Fatal(err)
	}
	if err := joiner.CancelPending(joined.PendingID); err != nil {
		t.Fatal(err)
	}
	if len(joiner.cfg.PendingJoins) != 0 {
		t.Fatalf("cancel did not clear pending: %+v", joiner.cfg.PendingJoins)
	}
	if err := joiner.CancelPending(joined.PendingID); err == nil {
		t.Fatal("cancel of missing pending should fail")
	}
}

// TestSubnetChangeDetected verifies the poll loop rebuilds the tunnel when
// the owner re-allocates the network subnet (self IP change).
func TestSubnetChangeDetected(t *testing.T) {
	ts := httptest.NewServer(server.NewHandler(server.NewStore(), server.Options{}))
	defer ts.Close()
	owner, _ := newTestDaemon(t)
	created, err := owner.Create(ts.URL, 0, "home", "10.88.0.0/24", false)
	if err != nil {
		t.Fatal(err)
	}
	joiner, _ := newTestDaemon(t)
	joined, err := joiner.Join(ts.URL, 0, created.NetworkID, created.PairingCode)
	if err != nil {
		t.Fatal(err)
	}
	if joined.IP != "10.88.0.2" {
		t.Fatalf("joiner IP = %s", joined.IP)
	}
	// The poll loop only runs when the tunnel could be created; without a TUN
	// device there is nothing to rebuild, so skip.
	joiner.mu.Lock()
	probe, tErr := NewTunnel(joiner.cfg.PrivateKey, "10.0.0.2", 51830, 1420)
	joiner.mu.Unlock()
	if tErr == nil && probe != nil {
		probe.Close()
	} else {
		t.Skip("no TUN device available in this environment")
	}

	// owner changes subnet; the joiner's poll loop should detect the new self
	// IP and re-attach (bringUp). We verify the stored IP + subnet converge.
	if err := owner.UpdateSettings(created.NetworkID, "", "192.168.60.0/24", nil); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		joiner.mu.Lock()
		nc := joiner.cfg.Networks[created.NetworkID]
		joiner.mu.Unlock()
		if nc != nil && strings.HasPrefix(nc.IP, "192.168.60.") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("joiner never picked up new subnet: ip=%s subnet=%s", nc.IP, nc.Subnet)
		}
		time.Sleep(30 * time.Millisecond)
	}
	joiner.mu.Lock()
	nc := joiner.cfg.Networks[created.NetworkID]
	joiner.mu.Unlock()
	if nc.Subnet != "192.168.60.0/24" {
		t.Fatalf("subnet not updated: %s", nc.Subnet)
	}
}

// TestNetworkNameSyncedFromServer verifies the poll loop picks up a name set
// on the server (e.g. renamed by the owner) even when the joiner joined with
// an empty name, so client and server display stay consistent.
func TestNetworkNameSyncedFromServer(t *testing.T) {
	ts := httptest.NewServer(server.NewHandler(server.NewStore(), server.Options{}))
	defer ts.Close()
	owner, _ := newTestDaemon(t)
	created, err := owner.Create(ts.URL, 0, "", "10.88.0.0/24", false)
	if err != nil {
		t.Fatal(err)
	}
	joiner, _ := newTestDaemon(t)
	joined, err := joiner.Join(ts.URL, 0, created.NetworkID, created.PairingCode)
	if err != nil {
		t.Fatal(err)
	}
	if joined.IP == "" {
		t.Fatalf("joiner joined without IP")
	}
	if got := joiner.cfg.Networks[created.NetworkID].Name; got != "" {
		t.Fatalf("joiner initial name = %q, want empty", got)
	}
	// The poll loop only runs when the tunnel could be created.
	joiner.mu.Lock()
	probe, tErr := NewTunnel(joiner.cfg.PrivateKey, "10.0.0.2", 51830, 1420)
	joiner.mu.Unlock()
	if tErr == nil && probe != nil {
		probe.Close()
	} else {
		t.Skip("no TUN device available in this environment")
	}

	// owner renames the network on the server; the joiner's poll loop should
	// adopt the new name.
	if err := owner.UpdateSettings(created.NetworkID, "office-renamed", "", nil); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		joiner.mu.Lock()
		nc := joiner.cfg.Networks[created.NetworkID]
		joiner.mu.Unlock()
		if nc != nil && nc.Name == "office-renamed" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("joiner never picked up renamed network: name=%q", nc.Name)
		}
		time.Sleep(30 * time.Millisecond)
	}
}

// TestAttachSubnetConflict verifies that attach refuses to join a network
// when its subnet overlaps with an already-joined network.
func TestAttachSubnetConflict(t *testing.T) {
	d, _ := newTestDaemon(t)

	// Simulate an existing network with 10.88.0.0/24
	d.cfg.Networks = map[string]*NetworkCfg{
		"NET001": {
			Name:   "network-1",
			NodeID: "node1",
			IP:     "10.88.0.1",
			Token:  "tok1",
			Subnet: "10.88.0.0/24",
			Active: true,
		},
	}

	// Try to attach a second network with overlapping subnet
	err := d.attach("NET002", "network-2", "node2", "10.88.0.2", "tok2", "", "10.88.0.0/24", false)
	if err == nil {
		t.Fatal("attach should reject overlapping subnet")
	}
	if !strings.Contains(err.Error(), "冲突") {
		t.Fatalf("error should mention conflict: %v", err)
	}

	// Non-overlapping subnet should succeed
	err = d.attach("NET002", "network-2", "node2", "10.88.1.1", "tok2", "", "10.88.1.0/24", false)
	if err != nil {
		t.Fatalf("attach should accept non-overlapping subnet: %v", err)
	}
}

// TestAttachSubnetConflictWithInactive verifies that attach rejects overlapping
// subnet even when the existing network is inactive.
func TestAttachSubnetConflictWithInactive(t *testing.T) {
	d, _ := newTestDaemon(t)

	// Simulate an existing but inactive network
	d.cfg.Networks = map[string]*NetworkCfg{
		"NET001": {
			Name:   "network-1",
			NodeID: "node1",
			IP:     "10.88.0.1",
			Token:  "tok1",
			Subnet: "10.88.0.0/24",
			Active: false, // inactive
		},
	}

	// Should still reject overlapping subnet even if inactive
	err := d.attach("NET002", "network-2", "node2", "10.88.0.2", "tok2", "", "10.88.0.0/24", false)
	if err == nil {
		t.Fatal("attach should reject overlapping subnet even with inactive network")
	}
}

// TestUpdateSettingsSubnetConflict verifies that UpdateSettings rejects
// changing a network's subnet when it would overlap with another network.
func TestUpdateSettingsSubnetConflict(t *testing.T) {
	d, _ := newTestDaemon(t)

	d.cfg.Networks = map[string]*NetworkCfg{
		"NET001": {
			Name:   "network-1",
			NodeID: "node1",
			IP:     "10.88.0.1",
			Token:  "tok1",
			Subnet: "10.88.0.0/24",
			Active: true,
			Owner:  true,
		},
		"NET002": {
			Name:   "network-2",
			NodeID: "node2",
			IP:     "10.88.1.1",
			Token:  "tok2",
			Subnet: "10.88.1.0/24",
			Active: true,
		},
	}

	// Try to change NET001's subnet to overlap with NET002
	err := d.UpdateSettings("NET001", "", "10.88.1.0/24", nil)
	if err == nil {
		t.Fatal("UpdateSettings should reject overlapping subnet")
	}
	if !strings.Contains(err.Error(), "冲突") {
		t.Fatalf("error should mention conflict: %v", err)
	}
}

func TestIsVirtualIface(t *testing.T) {
	cases := []struct {
		name string
		want bool
	}{
		{"en0", false},
		{"eth0", false},
		{"en1", false},
		{"lo0", true},
		{"lo", true},
		{"utun0", true},
		{"utun5", true},
		{"wg0", true},
		{"wg-quick", true},
		{"tun0", true},
		{"tap0", true},
		{"docker0", true},
		{"br-abcdef", true},
		{"veth1234", true},
		{"virbr0", true},
	}
	for _, c := range cases {
		if got := isVirtualIface(c.name); got != c.want {
			t.Errorf("isVirtualIface(%q) = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestPrivateIPv4(t *testing.T) {
	cases := []struct {
		ip   string
		want bool
	}{
		{"10.0.0.1", true},
		{"10.255.255.255", true},
		{"172.16.0.1", true},
		{"172.31.255.255", true},
		{"192.168.1.1", true},
		{"192.168.0.1", true},
		{"172.15.0.1", false},
		{"172.32.0.1", false},
		{"8.8.8.8", false},
		{"1.1.1.1", false},
		{"192.169.0.1", false},
		{"127.0.0.1", false},
	}
	for _, c := range cases {
		ip := net.ParseIP(c.ip).To4()
		if ip == nil {
			t.Fatalf("bad IP: %s", c.ip)
		}
		if got := privateIPv4(ip); got != c.want {
			t.Errorf("privateIPv4(%s) = %v, want %v", c.ip, got, c.want)
		}
	}
}

func TestIpNetCIDR(t *testing.T) {
	cases := []struct {
		cidr string
		want string
	}{
		{"192.168.1.100/24", "192.168.1.0/24"},
		{"10.0.0.5/8", "10.0.0.0/8"},
		{"172.16.3.200/12", "172.16.0.0/12"},
	}
	for _, c := range cases {
		_, ipNet, err := net.ParseCIDR(c.cidr)
		if err != nil {
			t.Fatalf("ParseCIDR(%q): %v", c.cidr, err)
		}
		if got := ipNetCIDR(ipNet); got != c.want {
			t.Errorf("ipNetCIDR(%s) = %q, want %q", c.cidr, got, c.want)
		}
	}
}

func TestDetectLocalSubnets(t *testing.T) {
	d, _ := newTestDaemon(t)
	subnets := d.DetectLocalSubnets()
	// On any machine with a network interface, we should get at least one result.
	// If running in CI with no interfaces, the result may be empty.
	t.Logf("detected local subnets: %v", subnets)
	// Verify all results are valid CIDRs in private ranges
	for _, s := range subnets {
		_, _, err := net.ParseCIDR(s)
		if err != nil {
			t.Errorf("invalid CIDR %q: %v", s, err)
		}
	}
}

func TestBuildCandidates(t *testing.T) {
	// Public endpoint: observed port first, then ±1..±8 interleaved.
	got := buildCandidates("203.0.113.9:51820")
	want := []string{
		"203.0.113.9:51820",
		"203.0.113.9:51819", "203.0.113.9:51821",
		"203.0.113.9:51818", "203.0.113.9:51822",
		"203.0.113.9:51817", "203.0.113.9:51823",
		"203.0.113.9:51816", "203.0.113.9:51824",
		"203.0.113.9:51815", "203.0.113.9:51825",
		"203.0.113.9:51814", "203.0.113.9:51826",
		"203.0.113.9:51813", "203.0.113.9:51827",
		"203.0.113.9:51812", "203.0.113.9:51828",
	}
	if len(got) != len(want) {
		t.Fatalf("len = %d, want %d (%v)", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("candidate[%d] = %q, want %q", i, got[i], want[i])
		}
	}

	// Private endpoints are used as-is: no probe window.
	for _, ep := range []string{"10.7.86.111:51820", "192.168.1.5:12345", "127.0.0.1:51820", "169.254.1.1:9"} {
		cands := buildCandidates(ep)
		if len(cands) != 1 || cands[0] != ep {
			t.Errorf("buildCandidates(%q) = %v, want [%q]", ep, cands, ep)
		}
	}

	// Port bounds are respected near 1 and 65535.
	low := buildCandidates("203.0.113.9:2")
	for _, c := range low {
		if _, p, err := net.SplitHostPort(c); err != nil || p == "0" || p == "-1" {
			t.Errorf("invalid low-end candidate %q", c)
		}
	}
}

// TestRotateKeysNoNetworks rotates the identity when there are no networks: the
// key changes, the schedule anchor is set, and no error is returned.
// testWGKey returns a format-valid WireGuard public key for tests.
func testWGKey(t *testing.T, i int) string {
	t.Helper()
	_, pub, err := GenerateKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	_ = i
	return pub
}

func TestRotateKeysNoNetworks(t *testing.T) {
	d, _ := newTestDaemon(t)
	oldKey := d.cfg.PrivateKey
	if err := d.RotateKeys(); err != nil {
		t.Fatalf("RotateKeys: %v", err)
	}
	if d.cfg.PrivateKey == "" || d.cfg.PrivateKey == oldKey {
		t.Fatalf("key not rotated: %q -> %q", oldKey, d.cfg.PrivateKey)
	}
	if d.cfg.LastKeyRotatedAt == 0 {
		t.Fatal("lastKeyRotatedAt not anchored")
	}
}

// TestRotateKeysAbortsOnBadToken verifies rotation is all-or-nothing: when a
// network's server exchange fails (bad token), the local key is left unchanged
// so no live network is ever broken by a half-applied rotation.
func TestRotateKeysAbortsOnBadToken(t *testing.T) {
	srv := server.NewStore()
	ts := httptest.NewServer(server.NewHandler(srv, server.Options{}))
	defer ts.Close()

	created, err := srv.CreateNetwork(testWGKey(t, 30), "device-rot", "rot", "", false)
	if err != nil {
		t.Fatal(err)
	}

	d, _ := newTestDaemon(t)
	oldKey := d.cfg.PrivateKey
	d.cfg.ServerAddr = ts.URL
	d.cfg.Networks = map[string]*NetworkCfg{
		created.NetworkID: {NodeID: "node-does-not-exist", Token: "WRONGTOKEN", Active: true},
	}
	if err := d.RotateKeys(); err == nil {
		t.Fatal("RotateKeys succeeded with a bad token, want error")
	}
	if d.cfg.PrivateKey != oldKey {
		t.Fatal("local key changed despite failed rotation")
	}
}
