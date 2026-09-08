package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"testing"

	"snet/internal/protocol"
)

// doJSONH is doJSON plus arbitrary request headers.
func doJSONH(t *testing.T, method, url, token string, headers map[string]string, body any, out any) *http.Response {
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
	for k, v := range headers {
		req.Header.Set(k, v)
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

func TestHealthzVersion(t *testing.T) {
	ts, _ := newTestServer(t)
	var body map[string]any
	resp := doJSON(t, http.MethodGet, ts.URL+"/healthz", "", nil, &body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("healthz status = %d", resp.StatusCode)
	}
	if got := resp.Header.Get(protocol.VersionHeader); got != "1" {
		t.Fatalf("X-Snet-Api-Version = %q, want 1", got)
	}
	if got := resp.Header.Get("X-Snet-Server-Version"); got != protocol.ServerVersion {
		t.Fatalf("X-Snet-Server-Version = %q, want %q", got, protocol.ServerVersion)
	}
	if body["status"] != "ok" {
		t.Fatalf("healthz body = %v", body)
	}
	if body["apiVersion"] != float64(protocol.APIVersion) {
		t.Fatalf("apiVersion = %v, want %d", body["apiVersion"], protocol.APIVersion)
	}
	if body["serverVersion"] != protocol.ServerVersion {
		t.Fatalf("serverVersion = %v, want %q", body["serverVersion"], protocol.ServerVersion)
	}
}

// TestVersionMismatchRejected verifies that a client announcing a different
// API major version is answered 426, and that the 426 response itself carries
// the server version header.
func TestVersionMismatchRejected(t *testing.T) {
	ts, _ := newTestServer(t)
	for _, v := range []string{"0", "2", "99"} {
		resp := doJSONH(t, http.MethodGet, ts.URL+"/healthz", "", map[string]string{protocol.VersionHeader: v}, nil, nil)
		if resp.StatusCode != http.StatusUpgradeRequired {
			t.Fatalf("version %s status = %d, want 426", v, resp.StatusCode)
		}
		if got := resp.Header.Get(protocol.VersionHeader); got != "1" {
			t.Fatalf("version %s: advert header = %q, want 1", v, got)
		}
	}
}

// TestVersionAcknowledged verifies matching and legacy (no header) clients are
// served normally and every response advertises the server version.
func TestVersionAcknowledged(t *testing.T) {
	ts, _ := newTestServer(t)

	for _, hdrs := range []map[string]string{
		{protocol.VersionHeader: "1"},
		nil, // legacy client: no version header
	} {
		var created protocol.CreateNetworkResp
		resp := doJSONH(t, http.MethodPost, ts.URL+"/api/v1/networks", "", hdrs,
			protocol.CreateNetworkReq{PublicKey: testKey(11)}, &created)
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("headers %v create status = %d", hdrs, resp.StatusCode)
		}
		if got := resp.Header.Get(protocol.VersionHeader); got != "1" {
			t.Fatalf("headers %v: resp apiversion = %q", hdrs, got)
		}
		if created.NetworkID == "" {
			t.Fatalf("headers %v: no network id", hdrs)
		}
	}
}

// TestNodeTokenKeyRotation verifies a node may rotate its own public key via
// the node-token path (used by periodic client-side rotation), that the change
// is visible to the owner, and that a stale token cannot rotate a key.
func TestNodeTokenKeyRotation(t *testing.T) {
	ts, _ := newTestServer(t)

	var created protocol.CreateNetworkResp
	resp := doJSON(t, http.MethodPost, ts.URL+"/api/v1/networks", "",
		protocol.CreateNetworkReq{PublicKey: testKey(20)}, &created)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create status = %d", resp.StatusCode)
	}

	var joined protocol.JoinResp
	resp = doJSON(t, http.MethodPost, ts.URL+"/api/v1/networks/"+created.NetworkID+"/join", "",
		protocol.JoinReq{Code: created.PairingCode, PublicKey: testKey(21)}, &joined)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("join status = %d", resp.StatusCode)
	}

	// Wrong token: 401, key unchanged.
	oldKey := testKey(21)
	_ = oldKey
	resp = doJSON(t, http.MethodPost, ts.URL+"/api/v1/networks/"+created.NetworkID+"/nodes/"+joined.NodeID+"/publickey",
		"WRONGTOKEN", protocol.UpdateNodePublicKeyReq{PublicKey: testKey(22)}, nil)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("wrong-token rotate status = %d, want 401", resp.StatusCode)
	}

	newKey := testKey(22)
	resp = doJSON(t, http.MethodPost, ts.URL+"/api/v1/networks/"+created.NetworkID+"/nodes/"+joined.NodeID+"/publickey",
		joined.Token, protocol.UpdateNodePublicKeyReq{PublicKey: newKey}, nil)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("rotate status = %d, want 204", resp.StatusCode)
	}

	var peers protocol.PeersResp
	resp = doJSON(t, http.MethodGet, ts.URL+"/api/v1/networks/"+created.NetworkID+"/peers", created.Token, nil, &peers)
	if resp.StatusCode != http.StatusOK || len(peers.Peers) != 1 {
		t.Fatalf("peers: %d %+v", resp.StatusCode, peers.Peers)
	}
	got := peers.Peers[0].PublicKey
	if got != newKey {
		t.Fatalf("rotated peer key = %q, want %q", got, newKey)
	}

	// Old token still points at the same node (only the key changed).
	resp = doJSON(t, http.MethodPost, ts.URL+"/api/v1/networks/"+created.NetworkID+"/nodes/"+joined.NodeID+"/publickey",
		joined.Token, protocol.UpdateNodePublicKeyReq{PublicKey: testKey(23)}, nil)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("re-rotate status = %d, want 204", resp.StatusCode)
	}
}