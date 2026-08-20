package client

import (
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"snet/internal/protocol"
)

type apiClient struct {
	server string
	http   *http.Client
}

// newAPIClient builds an HTTP client for the coordination server. For https
// servers, caPath pins the server's TLS certificate (PEM) as the only trust
// root. Without a pinned CA the client verifies against the system trust store
// (so public certificates such as Let's Encrypt work out of the box); set
// SNET_INSECURE_SKIP_VERIFY=1 to explicitly skip verification for self-signed
// deployments that have not pinned their CA.
func newAPIClient(server, caPath string) *apiClient {
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
	return &apiClient{server: strings.TrimRight(server, "/"), http: httpClient}
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
	req, err := http.NewRequest(method, c.server+path, rdr)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		var e protocol.ErrResp
		_ = json.Unmarshal(data, &e)
		return fmt.Errorf("server %d: %s", resp.StatusCode, e.Error)
	}
	if out != nil && len(data) > 0 {
		return json.Unmarshal(data, out)
	}
	return nil
}

func (c *apiClient) CreateNetwork(publicKey, deviceID, name, subnet string, approvalRequired bool) (protocol.CreateNetworkResp, error) {
	var out protocol.CreateNetworkResp
	err := c.do(http.MethodPost, "/api/v1/networks", "",
		protocol.CreateNetworkReq{PublicKey: publicKey, DeviceID: deviceID, Name: name, Subnet: subnet, ApprovalRequired: approvalRequired}, &out)
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
// server. The code is sent once and never persisted.
func (c *apiClient) BindDevice(deviceID, publicKey, code string) (protocol.BindDeviceResp, error) {
	var out protocol.BindDeviceResp
	err := c.do(http.MethodPost, "/api/v1/devices/bind", "",
		protocol.BindDeviceReq{Code: code, DeviceID: deviceID, PublicKey: publicKey}, &out)
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

func (c *apiClient) UpdateNetworkSettings(nid, token, name, subnet string, approvalRequired *bool) error {
	return c.do(http.MethodPatch, "/api/v1/networks/"+nid, token,
		protocol.NetworkSettingsReq{Name: name, Subnet: subnet, ApprovalRequired: approvalRequired}, nil)
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

func (c *apiClient) SetEndpointFor(nid, nodeID, token, endpoint string) error {
	return c.do(http.MethodPut, "/api/v1/networks/"+nid+"/nodes/"+nodeID+"/endpoint",
		token, protocol.SetEndpointReq{Endpoint: endpoint}, nil)
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
		return nil, fmt.Errorf("server %d: %s", resp.StatusCode, e.Error)
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
		return fmt.Errorf("server %d: %s", resp.StatusCode, e.Error)
	}
	return nil
}
