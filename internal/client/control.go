package client

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"snet/internal/protocol"
)

// httpStatusErr carries the HTTP status code from the server so callers can
// match on the code instead of parsing error strings.
type httpStatusErr struct {
	code int
	msg  string
}

func (e *httpStatusErr) Error() string { return fmt.Sprintf("server %d: %s", e.code, e.msg) }

type apiClient struct {
	server string
	http   *http.Client
	ctx    context.Context

	// smu guards serverMajor, the API major version reported by the server on
	// its latest response. 0 means the server did not advertise a version
	// (legacy server).
	smu         sync.Mutex
	serverMajor int
	warned      bool
}

// newAPIClient builds an HTTP client for the coordination server. For https
// servers, caPath pins the server's TLS certificate (PEM) as the only trust
// root. Without a pinned CA the client verifies against the system trust store
// (so public certificates such as Let's Encrypt work out of the box); set
// SNET_INSECURE_SKIP_VERIFY=1 to explicitly skip verification for self-signed
// deployments that have not pinned their CA.
func newAPIClient(server, caPath string, ctx context.Context) *apiClient {
	if !strings.HasPrefix(server, "http") {
		server = "http://" + server
	}
	// Keep-alive transport: the daemon polls every couple of seconds and
	// reuses the pooled connection (and TLS session) instead of paying a full
	// handshake per request over slow/jittery links.
	transport := &http.Transport{
		MaxIdleConns:        8,
		MaxIdleConnsPerHost: 4,
		IdleConnTimeout:     90 * time.Second,
		ForceAttemptHTTP2:   true,
	}
	if strings.HasPrefix(server, "https://") {
		if caPath != "" {
			pem, err := os.ReadFile(caPath)
			if err != nil {
				log.Printf("ca: read %s: %v", caPath, err)
			} else if pool := x509.NewCertPool(); pool.AppendCertsFromPEM(pem) {
				transport.TLSClientConfig = &tls.Config{RootCAs: pool}
			} else {
				log.Printf("ca: no valid certificates in %s", caPath)
			}
		}
		if transport.TLSClientConfig == nil {
			if os.Getenv("SNET_INSECURE_SKIP_VERIFY") == "1" {
				log.Printf("warn: SNET_INSECURE_SKIP_VERIFY=1 set, skipping TLS verification for %s", server)
				transport.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
			} else {
				transport.TLSClientConfig = &tls.Config{} // system trust store
			}
		}
	}
	httpClient := &http.Client{Transport: transport, Timeout: 15 * time.Second}
	return &apiClient{server: strings.TrimRight(server, "/"), http: httpClient, ctx: ctx}
}

func (c *apiClient) do(method, path string, token string, body any, out any) error {
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(c.ctx, method, c.server+path, rdr)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	req.Header.Set(protocol.VersionHeader, fmt.Sprint(protocol.APIVersion))
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	c.noteServerVersion(resp.Header.Get(protocol.VersionHeader))
	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		var e protocol.ErrResp
		_ = json.Unmarshal(data, &e)
		return &httpStatusErr{code: resp.StatusCode, msg: e.Error}
	}
	if out != nil && len(data) > 0 {
		return json.Unmarshal(data, out)
	}
	return nil
}

// noteServerVersion records the API major version the server advertises and
// logs a one-time warning when it is incompatible with this client, so a
// mismatch surfaces instead of silently misbehaving. A missing header means a
// legacy server without version negotiation: nothing to check.
func (c *apiClient) noteServerVersion(v string) {
	if v == "" {
		return
	}
	major, err := strconv.Atoi(v)
	if err != nil {
		return
	}
	c.smu.Lock()
	defer c.smu.Unlock()
	c.serverMajor = major
	if major == protocol.APIVersion || c.warned {
		return
	}
	c.warned = true
	log.Printf("warn: server %s runs api version %d, this client speaks %d; upgrade one side", c.server, major, protocol.APIVersion)
}

// ServerAPIVersion reports the API major version advertised by the server.
// The bool is false when no version was advertised (unknown/legacy server).
func (c *apiClient) ServerAPIVersion() (int, bool) {
	c.smu.Lock()
	defer c.smu.Unlock()
	return c.serverMajor, c.serverMajor != 0
}

func (c *apiClient) CreateNetwork(publicKey, deviceID, name, subnet string, approvalRequired bool,
	description string, tags []string, visibility string) (protocol.CreateNetworkResp, error) {
	var out protocol.CreateNetworkResp
	err := c.do(http.MethodPost, "/api/v1/networks", "",
		protocol.CreateNetworkReq{PublicKey: publicKey, DeviceID: deviceID, Name: name, Subnet: subnet,
			ApprovalRequired: approvalRequired, Description: description, Tags: tags, Visibility: visibility}, &out)
	return out, err
}

func (c *apiClient) Join(nid, code, publicKey, deviceID string) (protocol.JoinResp, error) {
	var out protocol.JoinResp
	err := c.do(http.MethodPost, "/api/v1/networks/"+nid+"/join", "",
		protocol.JoinReq{Code: code, PublicKey: publicKey, DeviceID: deviceID}, &out)
	return out, err
}

// RegisterDevice records (or refreshes) this device on the server and reports
// the server's view of the device binding. A nil bound pointer means the server
// did not report binding status (old server); non-nil is authoritative.
func (c *apiClient) RegisterDevice(deviceID, publicKey, name string) (*bool, error) {
	var out protocol.RegisterDeviceResp
	err := c.do(http.MethodPost, "/api/v1/devices", "",
		protocol.RegisterDeviceReq{DeviceID: deviceID, PublicKey: publicKey, Name: name}, &out)
	if err != nil {
		return nil, err
	}
	return out.Bound, nil
}

// BindDevice presents a device authorization code to link this device to the
// server. The code is sent once and never persisted. name is the
// client-reported display name (hostname); old servers ignore it.
func (c *apiClient) BindDevice(deviceID, publicKey, code, name string) (protocol.BindDeviceResp, error) {
	var out protocol.BindDeviceResp
	err := c.do(http.MethodPost, "/api/v1/devices/bind", "",
		protocol.BindDeviceReq{Code: code, DeviceID: deviceID, PublicKey: publicKey, Name: name}, &out)
	return out, err
}

func (c *apiClient) SetNodeDevice(nid, nodeID, token, deviceID string) error {
	return c.do(http.MethodPost, "/api/v1/networks/"+nid+"/nodes/"+nodeID+"/device", token,
		protocol.SetNodeDeviceReq{DeviceID: deviceID}, nil)
}

func (c *apiClient) ClaimNetwork(nid, token, deviceID string) error {
	return c.do(http.MethodPost, "/api/v1/networks/"+nid+"/claim", token,
		protocol.ClaimReq{DeviceID: deviceID}, nil)
}

func (c *apiClient) NetworkInfo(nid, token string) (protocol.NetworkInfoResp, error) {
	var out protocol.NetworkInfoResp
	err := c.do(http.MethodGet, "/api/v1/networks/"+nid, token, nil, &out)
	return out, err
}

func (c *apiClient) NetworkExists(nid, token string) (bool, error) {
	var out struct{ Exists bool }
	err := c.do(http.MethodGet, "/api/v1/networks/"+nid+"/exists", token, nil, &out)
	return out.Exists, err
}

func (c *apiClient) UpdateNetworkSettings(nid, token, name, subnet string, approvalRequired *bool,
	description *string, tags []string, visibility *string) error {
	return c.do(http.MethodPatch, "/api/v1/networks/"+nid, token,
		protocol.NetworkSettingsReq{Name: name, Subnet: subnet, ApprovalRequired: approvalRequired,
			Description: description, Tags: tags, Visibility: visibility}, nil)
}

func (c *apiClient) SetNodeRole(nid, token, nodeID, role string) error {
	return c.do(http.MethodPut, "/api/v1/networks/"+nid+"/nodes/"+nodeID+"/role", token,
		protocol.SetNodeRoleReq{Role: role}, nil)
}

func (c *apiClient) UpdateSubnets(nid, token string, subnets []string) error {
	return c.do(http.MethodPatch, "/api/v1/networks/"+nid+"/subnets", token,
		protocol.UpdateSubnetsReq{Subnets: subnets}, nil)
}

func (c *apiClient) ApprovePending(nid, token, pendingID string) error {
	return c.do(http.MethodPost, "/api/v1/networks/"+nid+"/pending/"+pendingID+"/approve", token, nil, nil)
}

func (c *apiClient) DenyPending(nid, token, pendingID string) error {
	return c.do(http.MethodPost, "/api/v1/networks/"+nid+"/pending/"+pendingID+"/deny", token, nil, nil)
}

func (c *apiClient) DeleteNetwork(nid, token string) error {
	return c.do(http.MethodDelete, "/api/v1/networks/"+nid, token, nil, nil)
}

func (c *apiClient) KickNode(nid, token, nodeID string) error {
	return c.do(http.MethodDelete, "/api/v1/networks/"+nid+"/members/"+nodeID, token, nil, nil)
}

func (c *apiClient) ResetCode(nid, token string) (string, error) {
	var out protocol.ResetCodeResp
	err := c.do(http.MethodPost, "/api/v1/networks/"+nid+"/code", token, nil, &out)
	return out.PairingCode, err
}

func (c *apiClient) SetEndpointFor(nid, nodeID, token, endpoint, localEndpoint string) error {
	return c.do(http.MethodPut, "/api/v1/networks/"+nid+"/nodes/"+nodeID+"/endpoint",
		token, protocol.SetEndpointReq{Endpoint: endpoint, LocalEndpoint: localEndpoint}, nil)
}

func (c *apiClient) ListPeers(nid, token string) ([]protocol.Node, error) {
	var out protocol.PeersResp
	err := c.do(http.MethodGet, "/api/v1/networks/"+nid+"/peers", token, nil, &out)
	return out.Peers, err
}

// PeersState returns peers plus the caller's own node (with current IP) and
// the network subnet, so the daemon can detect subnet/IP changes.
func (c *apiClient) PeersState(nid, token string) (protocol.PeersResp, error) {
	var out protocol.PeersResp
	err := c.do(http.MethodGet, "/api/v1/networks/"+nid+"/peers", token, nil, &out)
	return out, err
}

// PendingJoinStatus polls a join request awaiting owner approval.
func (c *apiClient) PendingJoinStatus(pendingID string) (protocol.PendingStatusResp, error) {
	var out protocol.PendingStatusResp
	err := c.do(http.MethodGet, "/api/v1/pending/"+pendingID, "", nil, &out)
	return out, err
}

func (c *apiClient) RemoveNode(nid, nodeID, token string) error {
	return c.do(http.MethodDelete, "/api/v1/networks/"+nid+"/nodes/"+nodeID, token, nil, nil)
}

// DeviceNetworks returns all networks the device holds a node in, including
// per-node credentials needed to reconstruct the client config after a reinstall.
func (c *apiClient) DeviceNetworks(deviceID, deviceToken string) ([]protocol.DeviceNetworkDetail, error) {
	var out protocol.DeviceNetworksResp
	req, err := http.NewRequest(http.MethodGet, c.server+"/api/v1/devices/"+deviceID+"/networks", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-Device-Token", deviceToken)
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		var e protocol.ErrResp
		_ = json.Unmarshal(data, &e)
		return nil, &httpStatusErr{code: resp.StatusCode, msg: e.Error}
	}
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, err
	}
	return out.Networks, nil
}

// UpdateNodePublicKey updates a node's WireGuard public key. Used after a
// client reinstall when the device generates a new keypair.
func (c *apiClient) UpdateNodePublicKey(nid, nodeID, deviceID, deviceToken, publicKey string) error {
	path := fmt.Sprintf("/api/v1/networks/%s/nodes/%s/publickey", nid, nodeID)
	body := protocol.UpdateNodePublicKeyReq{PublicKey: publicKey, DeviceID: deviceID}
	b, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequest(http.MethodPost, c.server+path, bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Device-Token", deviceToken)
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		data, _ := io.ReadAll(resp.Body)
		var e protocol.ErrResp
		_ = json.Unmarshal(data, &e)
		return &httpStatusErr{code: resp.StatusCode, msg: e.Error}
	}
	return nil
}

// UpdateNodePublicKeyByToken rotates a node's WireGuard public key under the
// node's own bearer token. Used by the daemon's periodic key rotation, which
// must work without a persisted device token.
func (c *apiClient) UpdateNodePublicKeyByToken(nid, nodeID, token, publicKey string) error {
	path := fmt.Sprintf("/api/v1/networks/%s/nodes/%s/publickey", nid, nodeID)
	body := protocol.UpdateNodePublicKeyReq{PublicKey: publicKey}
	return c.do(http.MethodPost, path, token, body, nil)
}
