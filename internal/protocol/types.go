package protocol

const (
	DefaultMTU           = 1420
	DefaultWGPort        = 51820
	DefaultCtlAddr       = "http://127.0.0.1:19432"
	NetworkPrefix        = "10.88.0."
	MaxNodes             = 100
	KeepaliveInterval    = 10
	PollIntervalSeconds  = 2
	ProbeIntervalSeconds = 15
	ProbePort            = 8091
)

type Network struct {
	ID               string `json:"id"`
	PairingCode      string `json:"pairingCode"`
	OwnerNodeID      string `json:"ownerNodeId"`
	CreatedAt        string `json:"createdAt"`
	Name             string `json:"name,omitempty"`
	Subnet           string `json:"subnet,omitempty"`
	OwnerDeviceID    string `json:"ownerDeviceId,omitempty"`
	LastActivityAt   int64  `json:"lastActivityAt,omitempty"`
	RelayPort        int    `json:"relayPort,omitempty"`
	NodeCount        int    `json:"nodeCount,omitempty"`
	Online           bool   `json:"online,omitempty"`
	ApprovalRequired bool   `json:"approvalRequired,omitempty"`
	PendingCount     int    `json:"pendingCount,omitempty"`
	// Managed 标记服务端直接创建的网络:无网主、不受僵尸清理、客户端不可认领。
	Managed bool `json:"managed,omitempty"`
}

type Node struct {
	ID        string `json:"id"`
	NetworkID string `json:"networkId"`
	IP        string `json:"ip"`
	PublicKey string `json:"publicKey"`
	Endpoint  string `json:"endpoint"`
	LastSeen  int64  `json:"lastSeen,omitempty"`
	DeviceID  string `json:"deviceId,omitempty"`
	Online    bool   `json:"online,omitempty"`
}

type CreateNetworkReq struct {
	PublicKey        string `json:"publicKey"`
	DeviceID         string `json:"deviceId,omitempty"`
	Name             string `json:"name,omitempty"`
	Subnet           string `json:"subnet,omitempty"`
	ApprovalRequired bool   `json:"approvalRequired,omitempty"`
}

type CreateNetworkResp struct {
	NetworkID        string `json:"networkId"`
	PairingCode      string `json:"pairingCode"`
	NodeID           string `json:"nodeId"`
	IP               string `json:"ip"`
	Token            string `json:"token"`
	Subnet           string `json:"subnet,omitempty"`
	RelayPort        int    `json:"relayPort,omitempty"`
	ApprovalRequired bool   `json:"approvalRequired,omitempty"`
}

type JoinReq struct {
	Code      string `json:"code"`
	PublicKey string `json:"publicKey"`
	DeviceID  string `json:"deviceId,omitempty"`
}

type JoinResp struct {
	NetworkID string `json:"networkId"`
	Name      string `json:"name,omitempty"`
	NodeID    string `json:"nodeId"`
	IP        string `json:"ip"`
	Token     string `json:"token"`
	Subnet    string `json:"subnet,omitempty"`
	RelayPort int    `json:"relayPort,omitempty"`
	Peers     []Node `json:"peers"`
	// Status 为 "pending" 时表示加入需网络创建者批准：不返回 IP/Token，
	// 客户端应轮询 PendingID 对应的待批准状态。空/“ok” 为正常加入。
	Status    string `json:"status,omitempty"`
	PendingID string `json:"pendingId,omitempty"`
}

type SetEndpointReq struct {
	Endpoint string `json:"endpoint"`
}

type PeersResp struct {
	Peers []Node `json:"peers"`
	Name  string `json:"name,omitempty"`
	// Self 是调用者自身的节点（含最新 IP，用于网段变更后自动重配）。
	Self             *Node  `json:"self,omitempty"`
	Subnet           string `json:"subnet,omitempty"`
	ApprovalRequired bool   `json:"approvalRequired,omitempty"`
}

// ClaimReq claims ownership of a legacy network. Requires the calling device
// to already own a node inside the network.
type ClaimReq struct {
	DeviceID string `json:"deviceId"`
}

type SetNodeDeviceReq struct {
	DeviceID string `json:"deviceId"`
}

// NetworkSettingsReq updates a network's name, subnet and/or join-approval
// setting. Empty name/subnet keep the current value; a nil approval keeps it.
type NetworkSettingsReq struct {
	Name             string `json:"name"`
	Subnet           string `json:"subnet"`
	ApprovalRequired *bool  `json:"approvalRequired"`
}

type NetworkInfoResp struct {
	Network
	Nodes   []Node        `json:"nodes"`
	Pending []PendingNode `json:"pending,omitempty"`
}

type PendingNode struct {
	ID        string `json:"id"`
	PublicKey string `json:"publicKey"`
	DeviceID  string `json:"deviceId,omitempty"`
	CreatedAt string `json:"createdAt,omitempty"`
}

// PendingStatusResp reports a pending join's state. Status 为 "pending" /
// "approved" / "denied"；approved 时附带完整的加入信息（IP/Token 等）。
type PendingStatusResp struct {
	Status    string `json:"status"`
	NetworkID string `json:"networkId,omitempty"`
	NodeID    string `json:"nodeId,omitempty"`
	IP        string `json:"ip,omitempty"`
	Token     string `json:"token,omitempty"`
	Subnet    string `json:"subnet,omitempty"`
	RelayPort int    `json:"relayPort,omitempty"`
	Peers     []Node `json:"peers,omitempty"`
}

type NetworksResp struct {
	Networks []Network `json:"networks"`
}

type ResetCodeResp struct {
	PairingCode string `json:"pairingCode"`
}

type Device struct {
	ID        string `json:"id"`
	PublicKey string `json:"publicKey"`
	CreatedAt string `json:"createdAt"`
	LastSeen  int64  `json:"lastSeen,omitempty"`
	Name      string `json:"name,omitempty"`
}

type RegisterDeviceReq struct {
	DeviceID  string `json:"deviceId"`
	PublicKey string `json:"publicKey"`
}

type RegisterDeviceResp struct {
	DeviceID string `json:"deviceId"`
	// Bound reports whether the server currently considers this device bound
	// to an authorization code. It is a pointer so an old server (field
	// absent) can be told apart from an explicit false; clients only treat an
	// explicit non-nil value as authoritative.
	Bound *bool `json:"bound,omitempty"`
}

type AdminNetworksResp struct {
	Networks []Network `json:"networks"`
}

// AdminCreateNetworkReq creates a server-managed network. Managed networks
// have no client owner, are exempt from zombie reaping and cannot be claimed.
type AdminCreateNetworkReq struct {
	Name             string `json:"name,omitempty"`
	Subnet           string `json:"subnet,omitempty"`
	ApprovalRequired bool   `json:"approvalRequired,omitempty"`
}

type AdminCreateNetworkResp struct {
	NetworkID   string `json:"networkId"`
	PairingCode string `json:"pairingCode"`
	Subnet      string `json:"subnet,omitempty"`
	RelayPort   int    `json:"relayPort,omitempty"`
}

// AdminPasswordReq changes the admin login password. OldPassword is verified
// against the currently stored hash; on success all admin sessions are
// invalidated.
type AdminPasswordReq struct {
	OldPassword string `json:"oldPassword"`
	NewPassword string `json:"newPassword"`
}

type AdminNodesResp struct {
	Nodes []Node `json:"nodes"`
}

type AdminDevicesResp struct {
	Devices []Device `json:"devices"`
}

// AdminLoginReq authenticates with username+password and returns a session
// token usable as an Authorization: Bearer <token> header.
type AdminLoginReq struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type AdminLoginResp struct {
	Token   string `json:"token"`
	Expires int64  `json:"expires"`
}

type ErrResp struct {
	Error string `json:"error"`
}

// AuthCodeLen is the length of a device authorization code. Codes use the
// 32-symbol idAlphabet (≈5 bits/symbol), so a 16-symbol code carries ≈80 bits
// of entropy. Only a hash of the code is ever stored.
const AuthCodeLen = 16

// BindDeviceReq binds a device to this server using a one-time authorization
// code generated by the administrator. The code is sent once over the control
// channel and never persisted by the client.
type BindDeviceReq struct {
	Code      string `json:"code"`
	DeviceID  string `json:"deviceId"`
	PublicKey string `json:"publicKey"`
}

type BindDeviceResp struct {
	DeviceID string `json:"deviceId"`
}

// AuthCodeInfo is the admin-facing view of an authorization code. The full
// plaintext code is only shown at generation time; this record carries a
// non-secret public ID (used for revocation) and a masked tail. A single code
// may bind up to MaxBindings devices; BoundCount reports how many are bound.
type AuthCodeInfo struct {
	ID            string `json:"id"`
	Code          string `json:"code"` // masked, e.g. "****3H7K"
	MaxBindings   int    `json:"maxBindings"`
	BoundCount    int    `json:"boundCount"`
	BoundToDevice string `json:"boundToDevice,omitempty"` // first bound device (backward compatible)
	BoundAt       string `json:"boundAt,omitempty"`
	BoundDevices  []AuthCodeBindingInfo `json:"boundDevices,omitempty"`
	CreatedAt     string `json:"createdAt"`
}

// AuthCodeBindingInfo describes one device bound to a shared authorization
// code.
type AuthCodeBindingInfo struct {
	DeviceID string `json:"deviceId"`
	BoundAt  string `json:"boundAt,omitempty"`
}

type AdminGenerateAuthCodesReq struct {
	Count       int `json:"count"`
	MaxBindings int `json:"maxBindings"` // devices a single code may bind; 0 defaults to 1
}

type AdminGenerateAuthCodesResp struct {
	Codes []string `json:"codes"`
	IDs   []string `json:"ids"`
}

type AdminAuthCodesResp struct {
	Codes []AuthCodeInfo `json:"codes"`
}
