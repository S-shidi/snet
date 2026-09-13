package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"snet/internal/protocol"
)

func newTestServer(t *testing.T) (*httptest.Server, *Store) {
	t.Helper()
	return newTestServerOpts(t, Options{})
}

func newTestServerOpts(t *testing.T, opts Options) (*httptest.Server, *Store) {
	t.Helper()
	s := NewStore()
	ts := httptest.NewServer(NewHandler(s, opts))
	t.Cleanup(ts.Close)
	return ts, s
}

func doJSON(t *testing.T, method, url, token string, body any, out any) *http.Response {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		_ = json.NewEncoder(&buf).Encode(body)
	}
	req, err := http.NewRequest(method, url, &buf)
	if err != nil {
		t.Fatal(err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if out != nil {
		_ = json.NewDecoder(resp.Body).Decode(out)
	}
	return resp
}

func TestCreateAndJoinFlow(t *testing.T) {
	ts, _ := newTestServer(t)

	var created protocol.CreateNetworkResp
	resp := doJSON(t, http.MethodPost, ts.URL+"/api/v1/networks", "", protocol.CreateNetworkReq{PublicKey: "qPw1bG7fV8xY2zA3bC4dE5fG6hI7jK8lM9nO0pQ1R2s="}, &created)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create status = %d", resp.StatusCode)
	}
	if created.NetworkID == "" || created.PairingCode == "" || created.Token == "" {
		t.Fatalf("missing fields: %+v", created)
	}
	if created.IP != "10.88.0.1" {
		t.Fatalf("creator ip = %q, want 10.88.0.1", created.IP)
	}

	var joined protocol.JoinResp
	resp = doJSON(t, http.MethodPost, ts.URL+"/api/v1/networks/"+created.NetworkID+"/join", "",
		protocol.JoinReq{Code: created.PairingCode, PublicKey: "rQw2bH7fV8xY2zA3bC4dE5fG6hI7jK8lM9nO0pQ1R2t="}, &joined)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("join status = %d", resp.StatusCode)
	}
	if joined.IP != "10.88.0.2" {
		t.Fatalf("joiner ip = %q, want 10.88.0.2", joined.IP)
	}
	if len(joined.Peers) != 1 || joined.Peers[0].PublicKey != "qPw1bG7fV8xY2zA3bC4dE5fG6hI7jK8lM9nO0pQ1R2s=" {
		t.Fatalf("joiner peers wrong: %+v", joined.Peers)
	}

	// owner sees the joiner in its peer list
	var peers protocol.PeersResp
	resp = doJSON(t, http.MethodGet, ts.URL+"/api/v1/networks/"+created.NetworkID+"/peers", created.Token, nil, &peers)
	if resp.StatusCode != http.StatusOK || len(peers.Peers) != 1 || peers.Peers[0].PublicKey != "rQw2bH7fV8xY2zA3bC4dE5fG6hI7jK8lM9nO0pQ1R2t=" {
		t.Fatalf("owner peers wrong: %d %+v", resp.StatusCode, peers.Peers)
	}

	// endpoint update reflects in peer list
	resp = doJSON(t, http.MethodPut, ts.URL+"/api/v1/networks/"+created.NetworkID+"/nodes/"+joined.NodeID+"/endpoint",
		joined.Token, protocol.SetEndpointReq{Endpoint: "203.0.113.5:51820"}, nil)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("set endpoint status = %d", resp.StatusCode)
	}
	resp = doJSON(t, http.MethodGet, ts.URL+"/api/v1/networks/"+created.NetworkID+"/peers", created.Token, nil, &peers)
	if peers.Peers[0].Endpoint != "203.0.113.5:51820" {
		t.Fatalf("peer endpoint = %q", peers.Peers[0].Endpoint)
	}
}

func TestPairingCodeSecurity(t *testing.T) {
	ts, _ := newTestServer(t)

	var created protocol.CreateNetworkResp
	doJSON(t, http.MethodPost, ts.URL+"/api/v1/networks", "", protocol.CreateNetworkReq{PublicKey: "qPw1bG7fV8xY2zA3bC4dE5fG6hI7jK8lM9nO0pQ1R2s="}, &created)

	// wrong code rejected
	resp := doJSON(t, http.MethodPost, ts.URL+"/api/v1/networks/"+created.NetworkID+"/join", "",
		protocol.JoinReq{Code: "WRONGCODE123", PublicKey: "rQw2bH7fV8xY2zA3bC4dE5fG6hI7jK8lM9nO0pQ1R2t="}, nil)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("wrong code status = %d, want 401", resp.StatusCode)
	}

	// 5 wrong attempts lock the code (subsequent attempts incl. correct code get 429)
	for i := 0; i < 4; i++ {
		doJSON(t, http.MethodPost, ts.URL+"/api/v1/networks/"+created.NetworkID+"/join", "",
			protocol.JoinReq{Code: "WRONGCODE123", PublicKey: "rQw2bH7fV8xY2zA3bC4dE5fG6hI7jK8lM9nO0pQ1R2t="}, nil)
	}
	resp = doJSON(t, http.MethodPost, ts.URL+"/api/v1/networks/"+created.NetworkID+"/join", "",
		protocol.JoinReq{Code: created.PairingCode, PublicKey: "rQw2bH7fV8xY2zA3bC4dE5fG6hI7jK8lM9nO0pQ1R2t="}, nil)
	if resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("locked code should return 429, got %d", resp.StatusCode)
	}

	// fresh network: code usable exactly 5 times, then rejected
	ts2, _ := newTestServer(t)
	var created2 protocol.CreateNetworkResp
	doJSON(t, http.MethodPost, ts2.URL+"/api/v1/networks", "", protocol.CreateNetworkReq{PublicKey: "qPw1bG7fV8xY2zA3bC4dE5fG6hI7jK8lM9nO0pQ1R2s="}, &created2)
	for i := 0; i < codeMaxUse; i++ {
		resp := doJSON(t, http.MethodPost, ts2.URL+"/api/v1/networks/"+created2.NetworkID+"/join", "",
			protocol.JoinReq{Code: created2.PairingCode, PublicKey: testKey(40 + i)}, nil)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("join %d = %d, want 200", i, resp.StatusCode)
		}
	}
	resp = doJSON(t, http.MethodPost, ts2.URL+"/api/v1/networks/"+created2.NetworkID+"/join", "",
		protocol.JoinReq{Code: created2.PairingCode, PublicKey: testKey(46)}, nil)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("exhausted code = %d, want 401", resp.StatusCode)
	}
}

func TestUnauthorized(t *testing.T) {
	ts, _ := newTestServer(t)

	resp := doJSON(t, http.MethodGet, ts.URL+"/api/v1/networks/FOO/peers", "", nil, nil)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("no token status = %d, want 401", resp.StatusCode)
	}
	resp = doJSON(t, http.MethodGet, ts.URL+"/api/v1/networks/FOO/peers", "badtoken", nil, nil)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("bad token status = %d, want 401", resp.StatusCode)
	}
}

func TestExternalNodeAdmin(t *testing.T) {
	ts, _ := newTestServerOpts(t, Options{AdminToken: "sekret"})

	var create protocol.CreateNetworkResp
	createResp := doJSON(t, http.MethodPost, ts.URL+"/api/v1/networks", "",
		map[string]string{"publicKey": "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="}, &create)
	if createResp.StatusCode != http.StatusCreated {
		t.Fatalf("create status = %d", createResp.StatusCode)
	}

	var node struct {
		NodeID string `json:"nodeId"`
		IP     string `json:"ip"`
	}
	resp := doJSON(t, http.MethodPost, ts.URL+"/admin/networks/"+create.NetworkID+"/external-node",
		"sekret", map[string]string{"publicKey": "BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB="}, &node)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("external-node status = %d", resp.StatusCode)
	}
	if node.IP != "10.88.0.2" || node.NodeID == "" {
		t.Fatalf("external node = %+v, want ip 10.88.0.2", node)
	}

	var nodes []protocol.Node
	resp = doJSON(t, http.MethodGet, ts.URL+"/admin/networks/"+create.NetworkID+"/nodes", "sekret", nil, &nodes)
	if resp.StatusCode != http.StatusOK || len(nodes) != 2 {
		t.Fatalf("nodes status=%d len=%d", resp.StatusCode, len(nodes))
	}
	found := false
	for _, n := range nodes {
		if n.PublicKey == "BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB=" && n.Endpoint == "" {
			found = true
		}
	}
	if !found {
		t.Fatalf("external node missing from peers: %+v", nodes)
	}
}

func TestLinkFormat(t *testing.T) {
	nid, code := "ABC12345", "k7x9-2mqz-4rtf"
	link := protocol.BuildLink(nid, code, "", "")
	gotN, gotC, gotS, gotName, err := protocol.ParseLink(link)
	if err != nil {
		t.Fatal(err)
	}
	if gotN != nid || gotC != "K7X92MQZ4RTF" || gotS != "" || gotName != "" {
		t.Fatalf("parse = %s %s %q %q", gotN, gotC, gotS, gotName)
	}
	// a link carrying its server address round-trips the server
	withSrv := protocol.BuildLink(nid, code, "https://snet.example:8090", "")
	gotN, gotC, gotS, gotName, err = protocol.ParseLink(withSrv)
	if err != nil {
		t.Fatal(err)
	}
	if gotN != nid || gotC != "K7X92MQZ4RTF" || gotS != "https://snet.example:8090" || gotName != "" {
		t.Fatalf("parse server link = %s %s %q %q", gotN, gotC, gotS, gotName)
	}
	// a link carrying a name round-trips the name
	withName := protocol.BuildLink(nid, code, "", "家庭NAS")
	_, _, _, gotName, err = protocol.ParseLink(withName)
	if err != nil {
		t.Fatal(err)
	}
	if gotName != "家庭NAS" {
		t.Fatalf("parse name link name = %q, want 家庭NAS", gotName)
	}
	if _, _, _, _, err := protocol.ParseLink("garbage"); err == nil {
		t.Fatal("expected error for garbage")
	}
}

func TestJoinDedupeDeviceID(t *testing.T) {
	// A reinstalled client re-joins with the same deviceId but a fresh WG key.
	// The server must revive the existing membership instead of creating a
	// duplicate node, and any leftover duplicate nodes of the device must be
	// purged so peers never see two nodes for one device.
	ts, s := newTestServer(t)

	var created protocol.CreateNetworkResp
	resp := doJSON(t, http.MethodPost, ts.URL+"/api/v1/networks", "", protocol.CreateNetworkReq{
		PublicKey: "qPw1bG7fV8xY2zA3bC4dE5fG6hI7jK8lM9nO0pQ1R2s=",
	}, &created)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create status = %d", resp.StatusCode)
	}
	const dev = "phone-dev-0001"
	const k1 = "rQw2bH7fV8xY2zA3bC4dE5fG6hI7jK8lM9nO0pQ1R2t="
	const k2 = "sQw2bH7fV8xY2zA3bC4dE5fG6hI7jK8lM9nO0pQ1R2u="
	const k3 = "tQw2bH7fV8xY2zA3bC4dE5fG6hI7jK8lM9nO0pQ1R2v="

	var joined protocol.JoinResp
	resp = doJSON(t, http.MethodPost, ts.URL+"/api/v1/networks/"+created.NetworkID+"/join", "",
		protocol.JoinReq{Code: created.PairingCode, PublicKey: k1, DeviceID: dev}, &joined)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("join status = %d", resp.StatusCode)
	}
	firstID, firstIP, firstTok := joined.NodeID, joined.IP, joined.Token

	// Re-join with the same device, new public key: same node, key refreshed.
	resp = doJSON(t, http.MethodPost, ts.URL+"/api/v1/networks/"+created.NetworkID+"/join", "",
		protocol.JoinReq{Code: created.PairingCode, PublicKey: k2, DeviceID: dev}, &joined)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("rejoin status = %d", resp.StatusCode)
	}
	if joined.NodeID != firstID || joined.IP != firstIP {
		t.Fatalf("rejoin changed identity: got %s/%s want %s/%s", joined.NodeID, joined.IP, firstID, firstIP)
	}
	if joined.Status != "rejoined" {
		t.Fatalf("rejoin status field = %q, want %q", joined.Status, "rejoined")
	}
	if joined.Token == "" || joined.Token == firstTok {
		t.Fatalf("rejoin token not refreshed: got %q", joined.Token)
	}

	// Owner must see exactly one node for the device, with the new key.
	var peers protocol.PeersResp
	resp = doJSON(t, http.MethodGet, ts.URL+"/api/v1/networks/"+created.NetworkID+"/peers", created.Token, nil, &peers)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("peers status = %d", resp.StatusCode)
	}
	count := 0
	for _, p := range peers.Peers {
		if p.DeviceID == dev {
			count++
			if p.PublicKey != k2 {
				t.Fatalf("revived node pubkey = %q, want %q", p.PublicKey, k2)
			}
		}
	}
	if count != 1 {
		t.Fatalf("peers for device %s = %d, want 1", dev, count)
	}

	// Damage state: simulate the pre-fix duplicate by injecting a second node
	// for the same device, then re-join and expect the duplicate purged. The
	// injected node is made clearly older so the "revive the latest" logic is
	// deterministic regardless of second-boundary ties.
	s.mu.Lock()
	ns := s.networks[created.NetworkID]
	ns.nodes["DUPXXXX"] = &protocol.Node{
		ID:        "DUPXXXX",
		NetworkID: created.NetworkID,
		IP:        "10.88.0.3",
		PublicKey: k1,
		DeviceID:  dev,
		LastSeen:  time.Now().Unix() - 3600,
	}
	s.mu.Unlock()

	resp = doJSON(t, http.MethodPost, ts.URL+"/api/v1/networks/"+created.NetworkID+"/join", "",
		protocol.JoinReq{Code: created.PairingCode, PublicKey: k3, DeviceID: dev}, &joined)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("rejoin after dup status = %d", resp.StatusCode)
	}
	s.mu.Lock()
	var survivors []string
	for id, n := range s.networks[created.NetworkID].nodes {
		if n.DeviceID == dev {
			survivors = append(survivors, id)
		}
	}
	s.mu.Unlock()
	if len(survivors) != 1 || survivors[0] != firstID {
		t.Fatalf("dup cleanup survivors = %v, want [%s]", survivors, firstID)
	}
}
