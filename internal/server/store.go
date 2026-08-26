package server

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"go.etcd.io/bbolt"
	"golang.org/x/crypto/bcrypt"
	"snet/internal/protocol"
)

var (
	ErrNotFound       = errors.New("not found")
	ErrCodeInvalid    = errors.New("invalid or expired pairing code")
	ErrCodeLocked     = errors.New("pairing code temporarily locked")
	ErrNetworkFull    = errors.New("network is full")
	ErrUnauthorized   = errors.New("unauthorized")
	ErrManagedNetwork = errors.New("服务端管理的网络不可认领")
	// ErrAuthCodeInvalid is returned when a presented device authorization
	// code does not match any generated code. Unlike pairing codes there is no
	// retry lockout: codes carry ≈80 bits of entropy and the bind endpoint is
	// per-IP rate limited.
	ErrAuthCodeInvalid = errors.New("invalid device authorization code")
	ErrAuthCodeUsed    = errors.New("device authorization code already used")
	ErrAuthCodeFull    = errors.New("device authorization code reached max bindings")
	// ErrAdminExists is returned by BootstrapAdmin when an admin account has
	// already been initialized.
	ErrAdminExists = errors.New("管理员账号已初始化")
	// ErrInternal is the client-facing stand-in for unexpected server errors;
	// the real cause is logged server-side.
	ErrInternal = errors.New("服务器内部错误，请稍后重试")
	// ErrBadJSON replaces raw JSON decoder errors on 400 responses so parser
	// internals never leak to clients.
	ErrBadJSON = errors.New("请求格式错误")
)

const (
	codeTTL         = 72 * time.Hour
	codeMaxUse      = 5
	codeUnlimited   = -1 // pairing.remaining sentinel for server-managed networks
	maxFails        = 5
	lockWindow      = 15 * time.Minute
	lastSeenTTL     = 60 * time.Second // a node is "online" if seen within this window
	lastSeenThrot   = 30 * time.Second // throttle lastSeen DB writes
	lastActThrot    = 60 * time.Second // throttle network activity DB writes
	netAliveTTL     = 90 * time.Second // a network is "online" if active within this window
	idAlphabet      = "ABCDEFGHJKMNPQRSTUVWXYZ23456789"
	tokenBytes      = 32
	networkIDLen    = 8
	codeLen         = 12
	authCodeIDLen   = 8
	defaultSubnet   = "10.88.0.0/24"
	maxSubnetPrefix = 24
)

var (
	bktNetworks  = []byte("networks")
	bktNodes     = []byte("nodes")
	bktTokens    = []byte("tokens")
	bktDevices   = []byte("devices")
	bktPending   = []byte("pending")
	bktAdmin     = []byte("admin")
	bktAuthCodes = []byte("authcodes")
)

const pendingTTL = 24 * time.Hour

type pairing struct {
	codeHash    string
	remaining   int
	expiresAt   time.Time
	failCount   int
	lockedUntil time.Time
}

// pendingNode is a join request awaiting the owner's approval. Once approved
// it carries the granted node/credentials so the waiting client can fetch
// them; it is deleted only when the client consumes them or it expires.
type pendingNode struct {
	ID        string    `json:"id"`
	PublicKey string    `json:"publicKey"`
	DeviceID  string    `json:"deviceId,omitempty"`
	NetworkID string    `json:"networkId"`
	CreatedAt time.Time `json:"createdAt"`
	ExpiresAt time.Time `json:"expiresAt"`

	Status    string `json:"status,omitempty"` // pending | approved
	NodeID    string `json:"nodeId,omitempty"`
	IP        string `json:"ip,omitempty"`
	Token     string `json:"token,omitempty"`
	Subnet    string `json:"subnet,omitempty"`
	RelayPort int    `json:"relayPort,omitempty"`
}

type networkState struct {
	n              protocol.Network
	seq            uint64 // creation sequence, monotonic per store
	pairing        *pairing
	nodes          map[string]*protocol.Node
	pending        map[string]*pendingNode
	ipam           int
	lastSeenWrite  int64
	lastActWrite   int64
	lastActivityAt int64
	relayPort      int
	subnetBase     net.IP
}

type tokenEntry struct {
	NetworkID string `json:"networkId"`
	NodeID    string `json:"nodeId"`
}

type networkRecord struct {
	ID               string    `json:"id"`
	OwnerNodeID      string    `json:"ownerNodeId"`
	CreatedAt        string    `json:"createdAt"`
	CodeHash         string    `json:"codeHash"`
	Remaining        int       `json:"remaining"`
	ExpiresAt        time.Time `json:"expiresAt"`
	FailCount        int       `json:"failCount"`
	LockedUntil      time.Time `json:"lockedUntil"`
	IPAM             int       `json:"ipam"`
	RelayPort        int       `json:"relayPort"`
	Name             string    `json:"name,omitempty"`
	Subnet           string    `json:"subnet,omitempty"`
	OwnerDeviceID    string    `json:"ownerDeviceId,omitempty"`
	LastActivityAt   int64     `json:"lastActivityAt,omitempty"`
	ApprovalRequired bool      `json:"approvalRequired,omitempty"`
	Managed          bool      `json:"managed,omitempty"`
}

type deviceRecord struct {
	ID          string `json:"id"`
	PublicKey   string `json:"publicKey"`
	CreatedAt   string `json:"createdAt"`
	LastSeen    int64  `json:"lastSeen,omitempty"`
	Name        string `json:"name,omitempty"`
	DeviceToken string `json:"deviceToken,omitempty"`
}

// authCodeBinding records one device bound to a shared authorization code.
type authCodeBinding struct {
	DeviceID  string    `json:"deviceId"`
	PublicKey string    `json:"publicKey,omitempty"`
	BoundAt   time.Time `json:"boundAt,omitempty"`
}

// authCodeRecord is a device authorization code. The database key is the
// non-secret public ID (used for admin revocation); CodePlain holds the
// plaintext so the admin console can always display it, and CodeHash is kept
// for bind verification. A single code may bind up to MaxBindings devices;
// MaxBindings is fixed at generation time. The legacy flat
// DeviceID/PublicKey/BoundAt fields exist only to migrate records persisted
// before multi-device codes were introduced.
type authCodeRecord struct {
	ID          string            `json:"id"`
	CodePlain   string            `json:"codePlain,omitempty"`
	CodeHash    string            `json:"codeHash"`
	Hint        string            `json:"hint"`
	CreatedAt   time.Time         `json:"createdAt"`
	MaxBindings int               `json:"maxBindings,omitempty"`
	Bindings    []authCodeBinding `json:"bindings,omitempty"`

	// Legacy single-binding fields, kept for migration only.
	DeviceID  string    `json:"deviceId,omitempty"`
	PublicKey string    `json:"publicKey,omitempty"`
	BoundAt   time.Time `json:"boundAt,omitempty"`
}

type Store struct {
	mu        sync.Mutex
	db        *bbolt.DB
	networks  map[string]*networkState
	byToken   map[string]tokenEntry // key = SHA-256 hex of the raw token
	devices   map[string]*deviceRecord
	authCodes map[string]*authCodeRecord // key = public auth-code ID
	netSeq    uint64                     // monotonic creation counter for networks

	// requireDeviceAuth gates CreateNetwork/Join/RegisterDevice: when on, only
	// devices that successfully bound a generated authorization code may
	// participate. The bind endpoint itself is always open.
	requireDeviceAuth bool

	// relayHost is the public relay endpoint advertised to peers; when empty,
	// relay mode is disabled and peers keep their self-advertised endpoints.
	relayHost  string
	relayBase  int
	relayCount int
}

// NewStore returns a purely in-memory store (no persistence). Used by tests
// and by callers that do not want a database file.
func NewStore() *Store {
	s, _ := NewStoreAt("")
	return s
}

// NewStoreAt opens (or creates) a bbolt database at path and loads all state.
// An empty path keeps the store purely in-memory.
func NewStoreAt(path string) (*Store, error) {
	s := &Store{
		networks:  make(map[string]*networkState),
		byToken:   make(map[string]tokenEntry),
		devices:   make(map[string]*deviceRecord),
		authCodes: make(map[string]*authCodeRecord),
	}
	if path == "" {
		return s, nil
	}
	db, err := bbolt.Open(path, 0o600, &bbolt.Options{Timeout: 2 * time.Second})
	if err != nil {
		return nil, err
	}
	s.db = db
	if err := s.load(); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) Close() error {
	if s.db != nil {
		return s.db.Close()
	}
	return nil
}

func (s *Store) load() error {
	var migrated []string
	if err := s.db.View(func(tx *bbolt.Tx) error {
		if nb := tx.Bucket(bktNetworks); nb != nil {
			c := nb.Cursor()
			for k, v := c.First(); k != nil; k, v = c.Next() {
				var r networkRecord
				if err := json.Unmarshal(v, &r); err != nil {
					return err
				}
				subnet := r.Subnet
				if subnet == "" {
					subnet = defaultSubnet
				}
				base, err := subnetBase(subnet)
				if err != nil {
					return fmt.Errorf("network %s: bad subnet %q: %w", r.ID, subnet, err)
				}
				lastAct := r.LastActivityAt
				if lastAct == 0 {
					lastAct = time.Now().Unix()
				}
				s.netSeq++
				ns := &networkState{
					seq: s.netSeq,
					n: protocol.Network{
						ID:               r.ID,
						OwnerNodeID:      r.OwnerNodeID,
						CreatedAt:        r.CreatedAt,
						PairingCode:      "",
						Name:             r.Name,
						Subnet:           subnet,
						OwnerDeviceID:    r.OwnerDeviceID,
						LastActivityAt:   lastAct,
						ApprovalRequired: r.ApprovalRequired,
						Managed:          r.Managed,
					},
					pairing: &pairing{
						codeHash:    r.CodeHash,
						remaining:   r.Remaining,
						expiresAt:   r.ExpiresAt,
						failCount:   r.FailCount,
						lockedUntil: r.LockedUntil,
					},
					nodes:          make(map[string]*protocol.Node),
					pending:        make(map[string]*pendingNode),
					ipam:           r.IPAM,
					relayPort:      r.RelayPort,
					lastActivityAt: lastAct,
					subnetBase:     base,
				}
				s.networks[r.ID] = ns
			}
		}
		if ndb := tx.Bucket(bktNodes); ndb != nil {
			c := ndb.Cursor()
			for k, v := c.First(); k != nil; k, v = c.Next() {
				var n protocol.Node
				if err := json.Unmarshal(v, &n); err != nil {
					return err
				}
				netID := string(k[:strings.IndexByte(string(k), '/')])
				if ns := s.networks[netID]; ns != nil {
					ns.nodes[n.ID] = &n
				}
			}
		}
		if tkb := tx.Bucket(bktTokens); tkb != nil {
			c := tkb.Cursor()
			for k, v := c.First(); k != nil; k, v = c.Next() {
				var te tokenEntry
				if err := json.Unmarshal(v, &te); err != nil {
					return err
				}
				s.byToken[string(k)] = te
			}
		}
		if ddb := tx.Bucket(bktDevices); ddb != nil {
			c := ddb.Cursor()
			for k, v := c.First(); k != nil; k, v = c.Next() {
				var d deviceRecord
				if err := json.Unmarshal(v, &d); err != nil {
					return err
				}
				s.devices[string(k)] = &d
			}
		}
		if ab := tx.Bucket(bktAuthCodes); ab != nil {
			c := ab.Cursor()
			for k, v := c.First(); k != nil; k, v = c.Next() {
				var ac authCodeRecord
				if err := json.Unmarshal(v, &ac); err != nil {
					return err
				}
				if ac.migrateLegacy() {
					migrated = append(migrated, ac.ID)
				}
				s.authCodes[string(k)] = &ac
			}
		}
		if pb := tx.Bucket(bktPending); pb != nil {
			c := pb.Cursor()
			for k, v := c.First(); k != nil; k, v = c.Next() {
				var p pendingNode
				if err := json.Unmarshal(v, &p); err != nil {
					return err
				}
				if ns := s.networks[p.NetworkID]; ns != nil {
					ns.pending[p.ID] = &p
				}
			}
		}
		return nil
	}); err != nil {
		return err
	}
	// Persist any legacy auth-code records that were migrated in memory so the
	// new multi-device format survives a restart. Errors here are non-fatal:
	// the migrated in-memory copy is already correct.
	for _, id := range migrated {
		if ac := s.authCodes[id]; ac != nil {
			_ = s.persistAuthCode(ac)
		}
	}
	return nil
}

func randomString(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	out := make([]byte, n)
	for i, v := range b {
		out[i] = idAlphabet[int(v)%len(idAlphabet)]
	}
	return string(out), nil
}

func randomToken() (string, error) {
	b := make([]byte, tokenBytes)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func hashCode(code string) string {
	h := sha256.Sum256([]byte(protocol.NormalizeCode(code)))
	return hex.EncodeToString(h[:])
}

func hashToken(tok string) string {
	h := sha256.Sum256([]byte(tok))
	return hex.EncodeToString(h[:])
}

// ---- persistence helpers (no-op when db == nil) ----

func (s *Store) ensureBuckets(tx *bbolt.Tx) error {
	for _, b := range [][]byte{bktNetworks, bktNodes, bktTokens, bktDevices, bktPending, bktAdmin, bktAuthCodes} {
		if _, err := tx.CreateBucketIfNotExists(b); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) persistNetwork(ns *networkState) error {
	if s.db == nil {
		return nil
	}
	r := networkRecord{
		ID:               ns.n.ID,
		OwnerNodeID:      ns.n.OwnerNodeID,
		CreatedAt:        ns.n.CreatedAt,
		CodeHash:         ns.pairing.codeHash,
		Remaining:        ns.pairing.remaining,
		ExpiresAt:        ns.pairing.expiresAt,
		FailCount:        ns.pairing.failCount,
		LockedUntil:      ns.pairing.lockedUntil,
		IPAM:             ns.ipam,
		RelayPort:        ns.relayPort,
		Name:             ns.n.Name,
		Subnet:           ns.n.Subnet,
		OwnerDeviceID:    ns.n.OwnerDeviceID,
		LastActivityAt:   ns.lastActivityAt,
		ApprovalRequired: ns.n.ApprovalRequired,
		Managed:          ns.n.Managed,
	}
	b, err := json.Marshal(r)
	if err != nil {
		return err
	}
	return s.db.Update(func(tx *bbolt.Tx) error {
		if err := s.ensureBuckets(tx); err != nil {
			return err
		}
		return tx.Bucket(bktNetworks).Put([]byte(ns.n.ID), b)
	})
}

func (s *Store) persistNode(netID string, n *protocol.Node) error {
	if s.db == nil {
		return nil
	}
	b, err := json.Marshal(n)
	if err != nil {
		return err
	}
	return s.db.Update(func(tx *bbolt.Tx) error {
		if err := s.ensureBuckets(tx); err != nil {
			return err
		}
		return tx.Bucket(bktNodes).Put([]byte(netID+"/"+n.ID), b)
	})
}

func (s *Store) deleteNode(netID, nodeID string) error {
	if s.db == nil {
		return nil
	}
	return s.db.Update(func(tx *bbolt.Tx) error {
		if err := s.ensureBuckets(tx); err != nil {
			return err
		}
		return tx.Bucket(bktNodes).Delete([]byte(netID + "/" + nodeID))
	})
}

func (s *Store) persistToken(tok string, te tokenEntry) error {
	if s.db == nil {
		return nil
	}
	b, err := json.Marshal(te)
	if err != nil {
		return err
	}
	return s.db.Update(func(tx *bbolt.Tx) error {
		if err := s.ensureBuckets(tx); err != nil {
			return err
		}
		return tx.Bucket(bktTokens).Put([]byte(hashToken(tok)), b)
	})
}

func (s *Store) persistDevice(d *deviceRecord) error {
	if s.db == nil {
		return nil
	}
	b, err := json.Marshal(d)
	if err != nil {
		return err
	}
	return s.db.Update(func(tx *bbolt.Tx) error {
		if err := s.ensureBuckets(tx); err != nil {
			return err
		}
		return tx.Bucket(bktDevices).Put([]byte(d.ID), b)
	})
}

func (s *Store) deleteTokenByHash(h string) error {
	if s.db == nil {
		return nil
	}
	return s.db.Update(func(tx *bbolt.Tx) error {
		if err := s.ensureBuckets(tx); err != nil {
			return err
		}
		return tx.Bucket(bktTokens).Delete([]byte(h))
	})
}

func (s *Store) persistAuthCode(ac *authCodeRecord) error {
	if s.db == nil {
		return nil
	}
	b, err := json.Marshal(ac)
	if err != nil {
		return err
	}
	return s.db.Update(func(tx *bbolt.Tx) error {
		if err := s.ensureBuckets(tx); err != nil {
			return err
		}
		return tx.Bucket(bktAuthCodes).Put([]byte(ac.ID), b)
	})
}

func (s *Store) deleteAuthCode(id string) error {
	if s.db == nil {
		return nil
	}
	return s.db.Update(func(tx *bbolt.Tx) error {
		if err := s.ensureBuckets(tx); err != nil {
			return err
		}
		return tx.Bucket(bktAuthCodes).Delete([]byte(id))
	})
}

func (s *Store) persistPending(p *pendingNode) error {
	if s.db == nil {
		return nil
	}
	b, err := json.Marshal(p)
	if err != nil {
		return err
	}
	return s.db.Update(func(tx *bbolt.Tx) error {
		if err := s.ensureBuckets(tx); err != nil {
			return err
		}
		return tx.Bucket(bktPending).Put([]byte(p.ID), b)
	})
}

func (s *Store) deletePending(pendingID string) error {
	if s.db == nil {
		return nil
	}
	return s.db.Update(func(tx *bbolt.Tx) error {
		if err := s.ensureBuckets(tx); err != nil {
			return err
		}
		return tx.Bucket(bktPending).Delete([]byte(pendingID))
	})
}

func (s *Store) deleteNetworkRows(netID string) error {
	if s.db == nil {
		return nil
	}
	return s.db.Update(func(tx *bbolt.Tx) error {
		if err := s.ensureBuckets(tx); err != nil {
			return err
		}
		if err := tx.Bucket(bktNetworks).Delete([]byte(netID)); err != nil {
			return err
		}
		ndb := tx.Bucket(bktNodes)
		if ndb != nil {
			prefix := []byte(netID + "/")
			c := ndb.Cursor()
			for k, _ := c.Seek(prefix); k != nil && bytesHasPrefix(k, prefix); k, _ = c.Next() {
				if err := ndb.Delete(k); err != nil {
					return err
				}
			}
		}
		return nil
	})
}

func bytesHasPrefix(b, prefix []byte) bool {
	return len(b) >= len(prefix) && string(b[:len(prefix)]) == string(prefix)
}

// ---- subnet handling ----

// subnetBase returns the network address of a CIDR subnet.
func subnetBase(cidr string) (net.IP, error) {
	ip, ipnet, err := net.ParseCIDR(cidr)
	if err != nil {
		return nil, err
	}
	return ip.Mask(ipnet.Mask), nil
}

// validateSubnet checks that cidr is a private IPv4 network with a prefix of
// /24 or larger and returns its normalized network address form.
func validateSubnet(cidr string) (string, error) {
	_, ipnet, err := net.ParseCIDR(cidr)
	if err != nil {
		return "", err
	}
	if ipnet.IP.To4() == nil {
		return "", errors.New("subnet must be IPv4")
	}
	ones, _ := ipnet.Mask.Size()
	if ones > maxSubnetPrefix {
		return "", fmt.Errorf("subnet prefix must be /%d or larger", maxSubnetPrefix)
	}
	if !ipnet.IP.IsPrivate() {
		return "", errors.New("subnet must be a private range (10/8, 172.16/12, 192.168/16)")
	}
	// ParseCIDR zeroes host bits, so re-derive the host part to verify the
	// caller supplied a proper network address (not e.g. 192.168.1.99/24).
	if hostStr, _, ok := strings.Cut(cidr, "/"); ok {
		if host := net.ParseIP(hostStr); host == nil || !host.Equal(host.Mask(ipnet.Mask)) {
			return "", errors.New("subnet must be a network address")
		}
	}
	return ipnet.String(), nil
}

// subnetsOverlap reports whether two CIDR subnets share any address.
func subnetsOverlap(a, b string) bool {
	_, an, err := net.ParseCIDR(a)
	if err != nil {
		return false
	}
	_, bn, err := net.ParseCIDR(b)
	if err != nil {
		return false
	}
	return an.Contains(bn.IP) || bn.Contains(an.IP)
}

// autoSubnetLocked picks a free 10.88.N.0/24 subnet not exactly used by any
// existing network. Networks are independent on the server; overlap is only
// checked at exact CIDR match to avoid giving the same subnet to two networks
// automatically. Users may still explicitly choose overlapping subnets.
func (s *Store) autoSubnetLocked() string {
	for n := 0; n < 256; n++ {
		cidr := fmt.Sprintf("10.88.%d.0/24", n)
		taken := false
		for _, other := range s.networks {
			if other.n.Subnet == cidr {
				taken = true
				break
			}
		}
		if !taken {
			return cidr
		}
	}
	return "10.89.0.0/24"
}

func addIPOffset(ip net.IP, n int) net.IP {
	v := ip.To4()
	x := binary.BigEndian.Uint32(v)
	x += uint32(n)
	out := make(net.IP, 4)
	binary.BigEndian.PutUint32(out, x)
	return out
}

// ---- node address allocation ----

// allocIP assigns the next available address within the network's subnet.
func (ns *networkState) allocIP() (string, error) {
	if ns.ipam >= protocol.MaxNodes {
		return "", ErrNetworkFull
	}
	ns.ipam++
	return addIPOffset(ns.subnetBase, ns.ipam).String(), nil
}

// ---- relay mode ----

// SetRelay configures relay mode: relayHost is the public relay address
// advertised to peers and relayBase/relayCount define the assignable UDP port
// range. Callers must hold no locks.
func (s *Store) SetRelay(relayHost string, relayBase, relayCount int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.relayHost = relayHost
	s.relayBase = relayBase
	s.relayCount = relayCount
}

// relayEnabled reports whether relay mode is on.
func (s *Store) relayEnabled() bool {
	return s.relayHost != "" && s.relayCount > 0
}

// ensureRelayPort assigns a free relay port to the network if it does not
// already have one. Callers must hold s.mu.
func (s *Store) ensureRelayPort(ns *networkState) error {
	if ns.relayPort != 0 {
		return nil
	}
	for i := 0; i < s.relayCount; i++ {
		candidate := s.relayBase + i
		used := false
		for _, other := range s.networks {
			if other.relayPort == candidate {
				used = true
				break
			}
		}
		if !used {
			ns.relayPort = candidate
			return s.persistNetwork(ns)
		}
	}
	return errors.New("no free relay ports")
}

// ---- public API ----

func (s *Store) CreateNetwork(publicKey, deviceID, name, subnet string, approvalRequired bool) (protocol.CreateNetworkResp, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.requireDeviceAuth && !s.deviceBoundLocked(deviceID) {
		return protocol.CreateNetworkResp{}, ErrUnauthorized
	}
	if err := validateDeviceID(deviceID); err != nil {
		return protocol.CreateNetworkResp{}, err
	}
	if err := validatePublicKey(publicKey); err != nil {
		return protocol.CreateNetworkResp{}, err
	}
	if deviceID != "" {
		for _, other := range s.networks {
			if other.n.OwnerDeviceID == deviceID {
				return protocol.CreateNetworkResp{}, errors.New("该设备已创建网络（每设备仅能创建一个）")
			}
		}
	}
	if name != "" && (len(name) > 64 || strings.TrimSpace(name) == "") {
		return protocol.CreateNetworkResp{}, errors.New("invalid network name")
	}
	sub := subnet
	if sub == "" {
		sub = s.autoSubnetLocked()
	} else {
		norm, err := validateSubnet(sub)
		if err != nil {
			return protocol.CreateNetworkResp{}, err
		}
		sub = norm
	}
	base, err := subnetBase(sub)
	if err != nil {
		return protocol.CreateNetworkResp{}, err
	}

	nid, err := randomString(networkIDLen)
	if err != nil {
		return protocol.CreateNetworkResp{}, err
	}
	for s.networks[nid] != nil {
		nid, _ = randomString(networkIDLen)
	}
	code, err := randomString(codeLen)
	if err != nil {
		return protocol.CreateNetworkResp{}, err
	}
	tok, err := randomToken()
	if err != nil {
		return protocol.CreateNetworkResp{}, err
	}
	nodeID, err := randomString(8)
	if err != nil {
		return protocol.CreateNetworkResp{}, err
	}

	now := time.Now()
	ns := &networkState{
		n: protocol.Network{
			ID:               nid,
			PairingCode:      protocol.NormalizeCode(code),
			OwnerNodeID:      nodeID,
			CreatedAt:        now.UTC().Format(time.RFC3339),
			Name:             name,
			Subnet:           sub,
			OwnerDeviceID:    deviceID,
			LastActivityAt:   now.Unix(),
			ApprovalRequired: approvalRequired,
		},
		pairing: &pairing{
			codeHash:  hashCode(code),
			remaining: codeMaxUse,
			expiresAt: now.Add(codeTTL),
		},
		nodes:          make(map[string]*protocol.Node),
		pending:        make(map[string]*pendingNode),
		lastActivityAt: now.Unix(),
		subnetBase:     base,
	}
	s.netSeq++
	ns.seq = s.netSeq
	ip, err := ns.allocIP()
	if err != nil {
		return protocol.CreateNetworkResp{}, err
	}
	ns.nodes[nodeID] = &protocol.Node{
		ID:        nodeID,
		NetworkID: nid,
		IP:        ip,
		PublicKey: publicKey,
		DeviceID:  deviceID,
		LastSeen:  now.Unix(),
	}
	s.networks[nid] = ns
	s.byToken[hashToken(tok)] = tokenEntry{NetworkID: nid, NodeID: nodeID}
	if err := s.upsertDeviceLocked(deviceID, publicKey, ""); err != nil {
		return protocol.CreateNetworkResp{}, err
	}

	if s.relayEnabled() {
		if err := s.ensureRelayPort(ns); err != nil {
			return protocol.CreateNetworkResp{}, err
		}
	}
	if err := s.persistNetwork(ns); err != nil {
		return protocol.CreateNetworkResp{}, err
	}
	if err := s.persistNode(nid, ns.nodes[nodeID]); err != nil {
		return protocol.CreateNetworkResp{}, err
	}
	if err := s.persistToken(tok, tokenEntry{NetworkID: nid, NodeID: nodeID}); err != nil {
		return protocol.CreateNetworkResp{}, err
	}

	return protocol.CreateNetworkResp{
		NetworkID:        nid,
		PairingCode:      ns.n.PairingCode,
		NodeID:           nodeID,
		IP:               ip,
		Token:            tok,
		Subnet:           sub,
		RelayPort:        ns.relayPort,
		ApprovalRequired: ns.n.ApprovalRequired,
	}, nil
}

// upsertDeviceLocked records or refreshes a device identity. Callers must
// hold s.mu.  The name parameter seeds the display name on new devices; an
// empty string leaves any existing name untouched.
func (s *Store) upsertDeviceLocked(deviceID, publicKey, name string) error {
	if deviceID == "" {
		return nil
	}
	name = normalizeName(name)
	now := time.Now().Unix()
	d := s.devices[deviceID]
	if d == nil {
		d = &deviceRecord{
			ID:        deviceID,
			CreatedAt: time.Now().UTC().Format(time.RFC3339),
			Name:      name,
		}
		s.devices[deviceID] = d
	} else if name != "" && d.Name == "" {
		d.Name = name
	}
	d.PublicKey = publicKey
	d.LastSeen = now
	return s.persistDevice(d)
}

// fillDeviceNameLocked applies a client-reported name without clobbering an
// existing one; admin-set names always win. Callers hold s.mu.
func (s *Store) fillDeviceNameLocked(d *deviceRecord, name string) {
	if n := normalizeName(name); n != "" && d.Name == "" {
		d.Name = n
	}
}

// touchLocked refreshes the network activity timestamp and persists at most
// every lastActThrot. Callers must hold s.mu.
func (s *Store) touchLocked(ns *networkState, now time.Time) {
	ts := now.Unix()
	if ts <= ns.lastActivityAt {
		return
	}
	ns.lastActivityAt = ts
	ns.n.LastActivityAt = ts
	if now.Unix()-ns.lastActWrite >= int64(lastActThrot/time.Second) {
		ns.lastActWrite = now.Unix()
		_ = s.persistNetwork(ns)
	}
}

func (s *Store) Join(nid, rawCode, publicKey, deviceID string) (protocol.JoinResp, error) {
	code := protocol.NormalizeCode(rawCode)
	now := time.Now()

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.requireDeviceAuth && !s.deviceBoundLocked(deviceID) {
		return protocol.JoinResp{}, ErrUnauthorized
	}
	if err := validatePublicKey(publicKey); err != nil {
		return protocol.JoinResp{}, err
	}

	ns := s.networks[nid]
	if ns == nil {
		return protocol.JoinResp{}, ErrNotFound
	}
	p := ns.pairing
	if now.Before(p.lockedUntil) {
		return protocol.JoinResp{}, ErrCodeLocked
	}
	if p.codeHash != hashCode(code) {
		p.failCount++
		if p.failCount >= maxFails {
			p.lockedUntil = now.Add(lockWindow)
			p.failCount = 0
		}
		_ = s.persistNetwork(ns)
		return protocol.JoinResp{}, ErrCodeInvalid
	}
	if !p.expiresAt.IsZero() && now.After(p.expiresAt) {
		return protocol.JoinResp{}, ErrCodeInvalid
	}
	if p.remaining == 0 {
		return protocol.JoinResp{}, ErrCodeInvalid
	}
	if len(ns.nodes) >= protocol.MaxNodes {
		return protocol.JoinResp{}, ErrNetworkFull
	}
	if p.remaining > 0 {
		p.remaining--
	}
	p.failCount = 0

	if ns.n.ApprovalRequired {
		pendingID, err := randomString(12)
		if err != nil {
			return protocol.JoinResp{}, err
		}
		pnd := &pendingNode{
			ID:        pendingID,
			PublicKey: publicKey,
			DeviceID:  deviceID,
			NetworkID: nid,
			CreatedAt: now,
			ExpiresAt: now.Add(pendingTTL),
			Status:    "pending",
		}
		ns.pending[pendingID] = pnd
		if err := s.persistNetwork(ns); err != nil {
			return protocol.JoinResp{}, err
		}
		if err := s.persistPending(pnd); err != nil {
			return protocol.JoinResp{}, err
		}
		return protocol.JoinResp{
			NetworkID: nid,
			Name:      ns.n.Name,
			Status:    "pending",
			PendingID: pendingID,
		}, nil
	}

	ip, err := ns.allocIP()
	if err != nil {
		return protocol.JoinResp{}, err
	}
	tok, err := randomToken()
	if err != nil {
		return protocol.JoinResp{}, err
	}
	nodeID, err := randomString(8)
	if err != nil {
		return protocol.JoinResp{}, err
	}

	node := &protocol.Node{
		ID:        nodeID,
		NetworkID: nid,
		IP:        ip,
		PublicKey: publicKey,
		DeviceID:  deviceID,
	}
	ns.nodes[nodeID] = node
	s.byToken[hashToken(tok)] = tokenEntry{NetworkID: nid, NodeID: nodeID}
	if err := s.upsertDeviceLocked(deviceID, publicKey, ""); err != nil {
		return protocol.JoinResp{}, err
	}
	s.touchLocked(ns, now)

	if s.relayEnabled() {
		if err := s.ensureRelayPort(ns); err != nil {
			return protocol.JoinResp{}, err
		}
	}
	if err := s.persistNetwork(ns); err != nil {
		return protocol.JoinResp{}, err
	}
	if err := s.persistNode(nid, node); err != nil {
		return protocol.JoinResp{}, err
	}
	if err := s.persistToken(tok, tokenEntry{NetworkID: nid, NodeID: nodeID}); err != nil {
		return protocol.JoinResp{}, err
	}

	peers := make([]protocol.Node, 0, len(ns.nodes)-1)
	for id, n := range ns.nodes {
		if id != nodeID {
			peers = append(peers, *n)
		}
	}

	return protocol.JoinResp{
		NetworkID: nid,
		Name:      ns.n.Name,
		NodeID:    nodeID,
		IP:        ip,
		Token:     tok,
		Subnet:    ns.n.Subnet,
		RelayPort: ns.relayPort,
		Peers:     peers,
	}, nil
}

func (s *Store) SetEndpoint(token, endpoint string) error {
	if err := validateEndpoint(endpoint); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	te, ok := s.byToken[hashToken(token)]
	if !ok {
		return ErrUnauthorized
	}
	ns := s.networks[te.NetworkID]
	if ns == nil {
		return ErrNotFound
	}
	n := ns.nodes[te.NodeID]
	if n == nil {
		return ErrNotFound
	}
	n.Endpoint = endpoint
	n.LastSeen = time.Now().Unix()
	s.touchLocked(ns, time.Now())
	return s.persistNode(te.NetworkID, n)
}

func (s *Store) ListPeers(token string) (protocol.PeersResp, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	te, ok := s.byToken[hashToken(token)]
	if !ok {
		return protocol.PeersResp{}, ErrUnauthorized
	}
	ns := s.networks[te.NetworkID]
	if ns == nil {
		return protocol.PeersResp{}, ErrNotFound
	}
	if me := ns.nodes[te.NodeID]; me != nil {
		now := time.Now().Unix()
		me.LastSeen = now
		if now-ns.lastSeenWrite >= int64(lastSeenThrot/time.Second) {
			_ = s.persistNode(te.NetworkID, me)
			ns.lastSeenWrite = now
		}
	}
	s.touchLocked(ns, time.Now())
	if s.relayEnabled() {
		if err := s.ensureRelayPort(ns); err != nil {
			return protocol.PeersResp{}, err
		}
	}
	relayEP := ""
	if s.relayEnabled() {
		relayEP = net.JoinHostPort(s.relayHost, fmt.Sprint(ns.relayPort))
	}
	peers := make([]protocol.Node, 0, len(ns.nodes)-1)
	for id, n := range ns.nodes {
		if id != te.NodeID {
			n2 := *n
			if relayEP != "" {
				n2.Endpoint = relayEP
			}
			peers = append(peers, n2)
		}
	}
	var self *protocol.Node
	if me := ns.nodes[te.NodeID]; me != nil {
		m2 := *me
		if relayEP != "" {
			m2.Endpoint = relayEP
		}
		self = &m2
	}
	return protocol.PeersResp{
		Peers:            peers,
		Name:             ns.n.Name,
		Self:             self,
		Subnet:           ns.n.Subnet,
		ApprovalRequired: ns.n.ApprovalRequired,
	}, nil
}

func (s *Store) RemoveNode(token string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	te, ok := s.byToken[hashToken(token)]
	if !ok {
		return ErrUnauthorized
	}
	ns := s.networks[te.NetworkID]
	if ns == nil {
		return ErrNotFound
	}
	delete(ns.nodes, te.NodeID)
	delete(s.byToken, hashToken(token))
	if err := s.deleteNode(te.NetworkID, te.NodeID); err != nil {
		return err
	}
	return s.deleteTokenByHash(hashToken(token))
}

// ---- device identity ----

var deviceIDRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{7,63}$`)

// validateDeviceID checks the format of a client-supplied device identifier.
func validateDeviceID(id string) error {
	if id == "" {
		return nil
	}
	if !deviceIDRe.MatchString(id) {
		return errors.New("invalid deviceId (8-64 chars, alphanumeric, '-' or '_')")
	}
	return nil
}

// validatePublicKey checks that a client-supplied WireGuard public key is a
// base64-encoded 32-byte key. An empty value is allowed (the field is
// optional on some endpoints); anything non-empty must be well-formed so
// junk cannot be persisted and fanned out to peers.
func validatePublicKey(pk string) error {
	if pk == "" {
		return nil
	}
	raw, err := base64.StdEncoding.DecodeString(pk)
	if err != nil {
		// Tolerate unpadded input; real keys are padded but be liberal.
		raw, err = base64.RawStdEncoding.DecodeString(strings.TrimRight(pk, "="))
		if err != nil {
			return errors.New("公钥格式无效（需 base64 编码的 WireGuard 公钥）")
		}
	}
	if len(raw) != 32 {
		return errors.New("公钥长度无效（需 32 字节 WireGuard 公钥）")
	}
	return nil
}

// maxNameLen caps client-supplied display names (network names are checked
// separately with the same limit).
const maxNameLen = 64

// normalizeName trims and caps a display name.
func normalizeName(name string) string {
	name = strings.TrimSpace(name)
	r := []rune(name)
	if len(r) > maxNameLen {
		return string(r[:maxNameLen])
	}
	return name
}

// validateEndpoint checks the host:port shape of a peer-advertised endpoint.
// An empty value is allowed (clears the endpoint).
func validateEndpoint(ep string) error {
	if ep == "" {
		return nil
	}
	host, portStr, err := net.SplitHostPort(ep)
	if err != nil || host == "" || portStr == "" {
		return errors.New("endpoint 需为 host:port 形式")
	}
	port, err := strconv.Atoi(portStr)
	if err != nil || port < 1 || port > 65535 {
		return errors.New("endpoint 端口无效")
	}
	return nil
}

// RegisterDevice records (or refreshes) a device identity and binds the
// device's WireGuard public key to it.  The name parameter seeds the display
// name on new devices; an empty string leaves any existing name untouched.
func (s *Store) RegisterDevice(deviceID, publicKey, name string) error {
	if err := validateDeviceID(deviceID); err != nil {
		return err
	}
	if err := validatePublicKey(publicKey); err != nil {
		return err
	}
	if deviceID == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.requireDeviceAuth && !s.deviceBoundLocked(deviceID) {
		return ErrUnauthorized
	}
	return s.upsertDeviceLocked(deviceID, publicKey, name)
}

// SetNodeDevice binds a device identity to one of the caller's own nodes.
// Only the node's own token may change its binding.
func (s *Store) SetNodeDevice(token, nodeID, deviceID string) error {
	if err := validateDeviceID(deviceID); err != nil {
		return err
	}
	if deviceID == "" {
		return errors.New("missing deviceId")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	ns, meID, err := s.nodeFromTokenLocked(token)
	if err != nil {
		return err
	}
	if meID != nodeID {
		return ErrUnauthorized
	}
	ns.nodes[nodeID].DeviceID = deviceID
	if err := s.persistNode(ns.n.ID, ns.nodes[nodeID]); err != nil {
		return err
	}
	return s.upsertDeviceLocked(deviceID, ns.nodes[nodeID].PublicKey, "")
}

// ClaimNetwork assigns ownership of a legacy (ownerless) network to the
// device that already owns a node inside it.
func (s *Store) ClaimNetwork(nid, token, deviceID string) error {
	if err := validateDeviceID(deviceID); err != nil {
		return err
	}
	if deviceID == "" {
		return errors.New("missing deviceId")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	ns, meID, err := s.nodeFromTokenLocked(token)
	if err != nil {
		return err
	}
	if ns.n.ID != nid {
		return ErrUnauthorized
	}
	if ns.n.OwnerDeviceID != "" {
		return errors.New("network already has an owner")
	}
	if ns.n.Managed {
		return ErrManagedNetwork
	}
	if ns.nodes[meID].DeviceID != deviceID {
		return ErrUnauthorized
	}
	ns.n.OwnerDeviceID = deviceID
	return s.persistNetwork(ns)
}

// ---- owner API ----

// nodeFromTokenLocked resolves a coordination token to its network and node.
// Callers must hold s.mu.
func (s *Store) nodeFromTokenLocked(token string) (*networkState, string, error) {
	te, ok := s.byToken[hashToken(token)]
	if !ok {
		return nil, "", ErrUnauthorized
	}
	ns := s.networks[te.NetworkID]
	if ns == nil {
		return nil, "", ErrNotFound
	}
	return ns, te.NodeID, nil
}

// requireOwnerLocked verifies the calling node belongs to the network's owner
// device. Legacy unclaimed networks reject owner operations until claimed.
// Callers must hold s.mu.
func (s *Store) requireOwnerLocked(ns *networkState, nodeID string) error {
	if ns.n.OwnerDeviceID == "" {
		return ErrUnauthorized
	}
	me := ns.nodes[nodeID]
	if me == nil {
		return ErrNotFound
	}
	if me.DeviceID != ns.n.OwnerDeviceID {
		return ErrUnauthorized
	}
	return nil
}

// UpdateNetworkSettings updates the network name, subnet and/or the
// join-approval requirement. Only the owner device may change settings.
// Changing the subnet re-allocates every node's IP on the new range and
// resets the IPAM cursor; clients detect the change via the self IP in
// ListPeers and reconfigure themselves.
func (s *Store) UpdateNetworkSettings(token, name, subnet string, approvalRequired *bool) error {
	name = strings.TrimSpace(name)
	if name != "" && len(name) > 64 {
		return errors.New("invalid network name (max 64 chars)")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	ns, meID, err := s.nodeFromTokenLocked(token)
	if err != nil {
		return err
	}
	if err := s.requireOwnerLocked(ns, meID); err != nil {
		return err
	}
	return s.updateNetworkLocked(ns, name, subnet, approvalRequired)
}

// updateNetworkLocked applies name/subnet/approval changes without owner
// checks. Callers must hold s.mu.
func (s *Store) updateNetworkLocked(ns *networkState, name, subnet string, approvalRequired *bool) error {
	changed := false
	if name != "" && name != ns.n.Name {
		ns.n.Name = name
		changed = true
	}
	if subnet != "" && subnet != ns.n.Subnet {
		if err := s.reassignIPsLocked(ns, subnet); err != nil {
			return err
		}
		changed = true
	}
	if approvalRequired != nil && *approvalRequired != ns.n.ApprovalRequired {
		ns.n.ApprovalRequired = *approvalRequired
		changed = true
	}
	if !changed {
		return nil
	}
	return s.persistNetwork(ns)
}

// AdminUpdateNetwork updates any network's name/subnet/approval setting,
// bypassing the owner requirement.
func (s *Store) AdminUpdateNetwork(nid, name, subnet string, approvalRequired *bool) error {
	name = strings.TrimSpace(name)
	if name != "" && len(name) > 64 {
		return errors.New("invalid network name (max 64 chars)")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	ns := s.networks[nid]
	if ns == nil {
		return ErrNotFound
	}
	return s.updateNetworkLocked(ns, name, subnet, approvalRequired)
}

// reassignIPsLocked validates a new subnet and re-allocates all node IPs.
// Callers must hold s.mu. Node IDs, tokens and peer identity are preserved;
// only IPs change, which the daemon picks up via its self IP.
func (s *Store) reassignIPsLocked(ns *networkState, subnet string) error {
	norm, err := validateSubnet(subnet)
	if err != nil {
		return err
	}
	base, err := subnetBase(norm)
	if err != nil {
		return err
	}
	ipam := 1
	for _, n := range ns.nodes {
		n.IP = addIPOffset(base, ipam).String()
		ipam++
		if err := s.persistNode(ns.n.ID, n); err != nil {
			return err
		}
	}
	ns.n.Subnet = norm
	ns.subnetBase = base
	// allocIP increments ns.ipam before use, so the cursor is the node count.
	ns.ipam = len(ns.nodes)
	return nil
}

// ---- pending joins ----

// PendingStatus reports the state of a pending join request. Only the
// requesting client (identified by its pending ID) may read it. Status is
// "pending", "approved" or "denied"; approved responses include the full
// join payload.
func (s *Store) PendingStatus(pendingID string) (protocol.PendingStatusResp, error) {
	if pendingID == "" {
		return protocol.PendingStatusResp{}, ErrUnauthorized
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	pruneExpiredPendingLocked(s, time.Now())
	for _, ns := range s.networks {
		p := ns.pending[pendingID]
		if p == nil {
			continue
		}
		switch p.Status {
		case "approved":
			peers := make([]protocol.Node, 0, len(ns.nodes)-1)
			for id, n := range ns.nodes {
				if id != p.NodeID {
					peers = append(peers, *n)
				}
			}
			return protocol.PendingStatusResp{
				Status:    "approved",
				NetworkID: ns.n.ID,
				NodeID:    p.NodeID,
				IP:        p.IP,
				Token:     p.Token,
				Subnet:    p.Subnet,
				RelayPort: p.RelayPort,
				Peers:     peers,
			}, nil
		case "denied":
			return protocol.PendingStatusResp{Status: "denied", NetworkID: ns.n.ID}, nil
		default:
			return protocol.PendingStatusResp{Status: "pending", NetworkID: ns.n.ID}, nil
		}
	}
	return protocol.PendingStatusResp{Status: "gone"}, nil
}

// ConsumePending deletes an approved pending request once the client has
// picked up its credentials.
func (s *Store) ConsumePending(pendingID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, ns := range s.networks {
		if ns.pending[pendingID] != nil {
			delete(ns.pending, pendingID)
			_ = s.deletePending(pendingID)
			return
		}
	}
}

// OwnerApprove approves a pending join, allocating the node's address and
// credentials. Only the owner device may approve. Returns the granted join
// info so the owner can verify.
func (s *Store) OwnerApprove(token, pendingID string) (protocol.PendingStatusResp, error) {
	if pendingID == "" {
		return protocol.PendingStatusResp{}, ErrNotFound
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	pruneExpiredPendingLocked(s, time.Now())
	ns, meID, err := s.nodeFromTokenLocked(token)
	if err != nil {
		return protocol.PendingStatusResp{}, err
	}
	if err := s.requireOwnerLocked(ns, meID); err != nil {
		return protocol.PendingStatusResp{}, err
	}
	p := ns.pending[pendingID]
	if p == nil {
		return protocol.PendingStatusResp{}, ErrNotFound
	}
	return s.approvePendingLocked(ns, p)
}

// AdminApprove approves a pending join on any network, bypassing the owner
// requirement.
func (s *Store) AdminApprove(nid, pendingID string) (protocol.PendingStatusResp, error) {
	if pendingID == "" {
		return protocol.PendingStatusResp{}, ErrNotFound
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	pruneExpiredPendingLocked(s, time.Now())
	ns := s.networks[nid]
	if ns == nil {
		return protocol.PendingStatusResp{}, ErrNotFound
	}
	p := ns.pending[pendingID]
	if p == nil {
		return protocol.PendingStatusResp{}, ErrNotFound
	}
	return s.approvePendingLocked(ns, p)
}

// approvePendingLocked allocates node credentials for an approved join request
// and marks it approved. Callers must hold s.mu.
func (s *Store) approvePendingLocked(ns *networkState, p *pendingNode) (protocol.PendingStatusResp, error) {
	if p.Status == "approved" {
		return s.pendingStatusLocked(ns, p), nil
	}
	if len(ns.nodes) >= protocol.MaxNodes {
		return protocol.PendingStatusResp{}, ErrNetworkFull
	}
	ip, err := ns.allocIP()
	if err != nil {
		return protocol.PendingStatusResp{}, err
	}
	tok, err := randomToken()
	if err != nil {
		return protocol.PendingStatusResp{}, err
	}
	nodeID, err := randomString(8)
	if err != nil {
		return protocol.PendingStatusResp{}, err
	}
	node := &protocol.Node{
		ID:        nodeID,
		NetworkID: ns.n.ID,
		IP:        ip,
		PublicKey: p.PublicKey,
		DeviceID:  p.DeviceID,
	}
	ns.nodes[nodeID] = node
	s.byToken[hashToken(tok)] = tokenEntry{NetworkID: ns.n.ID, NodeID: nodeID}
	if err := s.upsertDeviceLocked(p.DeviceID, p.PublicKey, ""); err != nil {
		return protocol.PendingStatusResp{}, err
	}
	if s.relayEnabled() {
		if err := s.ensureRelayPort(ns); err != nil {
			return protocol.PendingStatusResp{}, err
		}
	}
	p.Status = "approved"
	p.NodeID = nodeID
	p.IP = ip
	p.Token = tok
	p.Subnet = ns.n.Subnet
	p.RelayPort = ns.relayPort
	s.touchLocked(ns, time.Now())
	if err := s.persistNetwork(ns); err != nil {
		return protocol.PendingStatusResp{}, err
	}
	if err := s.persistNode(ns.n.ID, node); err != nil {
		return protocol.PendingStatusResp{}, err
	}
	if err := s.persistToken(tok, tokenEntry{NetworkID: ns.n.ID, NodeID: nodeID}); err != nil {
		return protocol.PendingStatusResp{}, err
	}
	if err := s.persistPending(p); err != nil {
		return protocol.PendingStatusResp{}, err
	}
	return s.pendingStatusLocked(ns, p), nil
}

// OwnerDeny rejects a pending join. Only the owner device may deny.
func (s *Store) OwnerDeny(token, pendingID string) error {
	if pendingID == "" {
		return ErrNotFound
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	ns, meID, err := s.nodeFromTokenLocked(token)
	if err != nil {
		return err
	}
	if err := s.requireOwnerLocked(ns, meID); err != nil {
		return err
	}
	p := ns.pending[pendingID]
	if p == nil {
		return ErrNotFound
	}
	return s.denyPendingLocked(ns, p)
}

// AdminDeny rejects a pending join on any network, bypassing the owner
// requirement.
func (s *Store) AdminDeny(nid, pendingID string) error {
	if pendingID == "" {
		return ErrNotFound
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	ns := s.networks[nid]
	if ns == nil {
		return ErrNotFound
	}
	p := ns.pending[pendingID]
	if p == nil {
		return ErrNotFound
	}
	return s.denyPendingLocked(ns, p)
}

// denyPendingLocked marks a pending request denied. Callers must hold s.mu.
func (s *Store) denyPendingLocked(ns *networkState, p *pendingNode) error {
	if p.Status == "approved" {
		return errors.New("请求已批准，无法拒绝")
	}
	p.Status = "denied"
	return s.persistPending(p)
}

// pendingStatusLocked builds the status response for an approved request.
// Callers must hold s.mu.
func (s *Store) pendingStatusLocked(ns *networkState, p *pendingNode) protocol.PendingStatusResp {
	peers := make([]protocol.Node, 0, len(ns.nodes)-1)
	for id, n := range ns.nodes {
		if id != p.NodeID {
			peers = append(peers, *n)
		}
	}
	return protocol.PendingStatusResp{
		Status:    "approved",
		NetworkID: ns.n.ID,
		NodeID:    p.NodeID,
		IP:        p.IP,
		Token:     p.Token,
		Subnet:    p.Subnet,
		RelayPort: p.RelayPort,
		Peers:     peers,
	}
}

// pruneExpiredPendingLocked removes pending requests older than pendingTTL.
// Callers must hold s.mu.
func pruneExpiredPendingLocked(s *Store, now time.Time) {
	for _, ns := range s.networks {
		for id, p := range ns.pending {
			if now.After(p.ExpiresAt) {
				delete(ns.pending, id)
				_ = s.deletePending(id)
			}
		}
	}
}

// NetworkInfo returns full network details plus all member nodes for the
// network owner.
func (s *Store) NetworkInfo(token string) (protocol.NetworkInfoResp, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ns, meID, err := s.nodeFromTokenLocked(token)
	if err != nil {
		return protocol.NetworkInfoResp{}, err
	}
	if err := s.requireOwnerLocked(ns, meID); err != nil {
		return protocol.NetworkInfoResp{}, err
	}
	return s.networkInfoLocked(ns), nil
}

// networkInfoLocked builds the full network detail response. Callers must
// hold s.mu.
func (s *Store) networkInfoLocked(ns *networkState) protocol.NetworkInfoResp {
	now := time.Now().Unix()
	n := ns.n
	n.RelayPort = ns.relayPort
	n.NodeCount = len(ns.nodes)
	n.Online = now-ns.lastActivityAt < int64(netAliveTTL/time.Second)
	n.LastActivityAt = ns.lastActivityAt
	n.PendingCount = len(ns.pending)
	nodes := make([]protocol.Node, 0, len(ns.nodes))
	for _, nd := range ns.nodes {
		c := *nd
		c.Online = now-c.LastSeen < int64(lastSeenTTL/time.Second)
		nodes = append(nodes, c)
	}
	pending := make([]protocol.PendingNode, 0, len(ns.pending))
	for _, p := range ns.pending {
		pending = append(pending, protocol.PendingNode{
			ID:        p.ID,
			PublicKey: p.PublicKey,
			DeviceID:  p.DeviceID,
			CreatedAt: p.CreatedAt.UTC().Format(time.RFC3339),
		})
	}
	return protocol.NetworkInfoResp{Network: n, Nodes: nodes, Pending: pending}
}

// AdminNetworkInfo returns full network details for any network, bypassing
// the owner requirement.
func (s *Store) AdminNetworkInfo(nid string) (protocol.NetworkInfoResp, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	pruneExpiredPendingLocked(s, time.Now())
	ns := s.networks[nid]
	if ns == nil {
		return protocol.NetworkInfoResp{}, ErrNotFound
	}
	return s.networkInfoLocked(ns), nil
}

// AdminPending lists the pending join requests of any network.
func (s *Store) AdminPending(nid string) ([]protocol.PendingNode, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ns := s.networks[nid]
	if ns == nil {
		return nil, ErrNotFound
	}
	pruneExpiredPendingLocked(s, time.Now())
	// Only surface requests still awaiting a decision: approved/denied records
	// are kept for client polling (PendingStatus) but are noise for admins.
	out := make([]protocol.PendingNode, 0, len(ns.pending))
	for _, p := range ns.pending {
		if p.Status != "pending" {
			continue
		}
		pn := protocol.PendingNode{
			ID:        p.ID,
			PublicKey: p.PublicKey,
			DeviceID:  p.DeviceID,
			CreatedAt: p.CreatedAt.UTC().Format(time.RFC3339),
		}
		if d := s.devices[p.DeviceID]; d != nil {
			pn.DeviceName = d.Name
		}
		out = append(out, pn)
	}
	return out, nil
}

// newPairing builds a pairing entry for a fresh code. Managed (server-created)
// networks get unlimited, non-expiring codes; client networks keep the
// 72h / 5-use budget. Callers must hold s.mu.
func newPairing(ns *networkState, code string) *pairing {
	if ns.n.Managed {
		return &pairing{codeHash: hashCode(code), remaining: codeUnlimited}
	}
	return &pairing{
		codeHash:  hashCode(code),
		remaining: codeMaxUse,
		expiresAt: time.Now().Add(codeTTL),
	}
}

// ResetCode generates a fresh pairing code, invalidating the previous one.
func (s *Store) ResetCode(token string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ns, meID, err := s.nodeFromTokenLocked(token)
	if err != nil {
		return "", err
	}
	if err := s.requireOwnerLocked(ns, meID); err != nil {
		return "", err
	}
	code, err := randomString(codeLen)
	if err != nil {
		return "", err
	}
	ns.n.PairingCode = protocol.NormalizeCode(code)
	ns.pairing = newPairing(ns, code)
	if err := s.persistNetwork(ns); err != nil {
		return "", err
	}
	return ns.n.PairingCode, nil
}

// KickNode removes a member node from the network. The owner node itself
// cannot be kicked.
func (s *Store) KickNode(token, nodeID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	ns, meID, err := s.nodeFromTokenLocked(token)
	if err != nil {
		return err
	}
	if err := s.requireOwnerLocked(ns, meID); err != nil {
		return err
	}
	if ns.nodes[nodeID] == nil {
		return ErrNotFound
	}
	if nodeID == ns.n.OwnerNodeID {
		return errors.New("cannot remove the owner node")
	}
	delete(ns.nodes, nodeID)
	for h, te := range s.byToken {
		if te.NetworkID == ns.n.ID && te.NodeID == nodeID {
			delete(s.byToken, h)
			if err := s.deleteTokenByHash(h); err != nil {
				return err
			}
		}
	}
	return s.deleteNode(ns.n.ID, nodeID)
}

// DeleteNetwork removes the network and all its state.
func (s *Store) DeleteNetwork(token string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	ns, meID, err := s.nodeFromTokenLocked(token)
	if err != nil {
		return err
	}
	if err := s.requireOwnerLocked(ns, meID); err != nil {
		return err
	}
	return s.deleteNetworkLocked(ns.n.ID)
}

// deleteNetworkLocked removes a network and all its rows. Callers must hold
// s.mu.
func (s *Store) deleteNetworkLocked(nid string) error {
	ns := s.networks[nid]
	if ns != nil {
		for _, p := range ns.pending {
			if err := s.deletePending(p.ID); err != nil {
				return err
			}
		}
	}
	for h, te := range s.byToken {
		if te.NetworkID == nid {
			delete(s.byToken, h)
			if err := s.deleteTokenByHash(h); err != nil {
				return err
			}
		}
	}
	delete(s.networks, nid)
	return s.deleteNetworkRows(nid)
}

// ---- relay activity ----

// MarkRelayActivity refreshes the activity of the network whose relay port
// received traffic. It is called from the relay hot path, so it only touches
// memory; persistence is throttled.
func (s *Store) MarkRelayActivity(port int) {
	if port <= 0 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, ns := range s.networks {
		if ns.relayPort == port {
			s.touchLocked(ns, time.Now())
			return
		}
	}
}

// ---- zombie reaping ----

// SweepZombies deletes networks whose last activity is older than ttl and
// returns the removed network IDs. A ttl <= 0 disables reaping.
func (s *Store) SweepZombies(ttl time.Duration) ([]string, error) {
	if ttl <= 0 {
		return nil, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	pruneExpiredPendingLocked(s, time.Now())
	now := time.Now()
	var victims []string
	for nid, ns := range s.networks {
		if ns.n.Managed {
			continue // server-managed networks are never reaped
		}
		last := ns.lastActivityAt
		if last == 0 {
			if t, err := time.Parse(time.RFC3339, ns.n.CreatedAt); err == nil {
				last = t.Unix()
			}
		}
		if last > 0 && now.Sub(time.Unix(last, 0)) >= ttl {
			victims = append(victims, nid)
		}
	}
	for _, nid := range victims {
		if err := s.deleteNetworkLocked(nid); err != nil {
			return victims, err
		}
	}
	return victims, nil
}

// ---- admin API (guarded by admin token in the handler layer) ----

// AdminCreateNetwork creates a server-managed network with no owner node.
// Managed networks get unlimited, non-expiring pairing codes and are exempt
// from zombie reaping. Returns the network ID and code for distribution.
func (s *Store) AdminCreateNetwork(name, subnet string, approvalRequired bool) (protocol.AdminCreateNetworkResp, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	name = strings.TrimSpace(name)
	if name != "" && len(name) > 64 {
		return protocol.AdminCreateNetworkResp{}, errors.New("invalid network name (max 64 chars)")
	}
	sub := subnet
	if sub == "" {
		sub = s.autoSubnetLocked()
	} else {
		norm, err := validateSubnet(sub)
		if err != nil {
			return protocol.AdminCreateNetworkResp{}, err
		}
		sub = norm
	}
	base, err := subnetBase(sub)
	if err != nil {
		return protocol.AdminCreateNetworkResp{}, err
	}

	nid, err := randomString(networkIDLen)
	if err != nil {
		return protocol.AdminCreateNetworkResp{}, err
	}
	for s.networks[nid] != nil {
		nid, _ = randomString(networkIDLen)
	}
	code, err := randomString(codeLen)
	if err != nil {
		return protocol.AdminCreateNetworkResp{}, err
	}

	now := time.Now()
	ns := &networkState{
		n: protocol.Network{
			ID:               nid,
			PairingCode:      protocol.NormalizeCode(code),
			CreatedAt:        now.UTC().Format(time.RFC3339),
			Name:             name,
			Subnet:           sub,
			LastActivityAt:   now.Unix(),
			ApprovalRequired: approvalRequired,
			Managed:          true,
		},
		pairing: &pairing{
			codeHash:  hashCode(code),
			remaining: codeUnlimited,
		},
		nodes:          make(map[string]*protocol.Node),
		pending:        make(map[string]*pendingNode),
		lastActivityAt: now.Unix(),
		subnetBase:     base,
	}
	s.netSeq++
	ns.seq = s.netSeq
	s.networks[nid] = ns
	if s.relayEnabled() {
		if err := s.ensureRelayPort(ns); err != nil {
			return protocol.AdminCreateNetworkResp{}, err
		}
	}
	if err := s.persistNetwork(ns); err != nil {
		return protocol.AdminCreateNetworkResp{}, err
	}
	return protocol.AdminCreateNetworkResp{
		NetworkID:   nid,
		PairingCode: ns.n.PairingCode,
		Subnet:      sub,
		RelayPort:   ns.relayPort,
	}, nil
}

type networkSummary struct {
	Network        protocol.Network `json:"network"`
	Seq            uint64           `json:"-"`
	NodeCount      int              `json:"nodeCount"`
	RelayPort      int              `json:"relayPort"`
	Online         bool             `json:"online"`
	Zombie         bool             `json:"zombie"`
	LastActivityAt int64            `json:"lastActivityAt"`
	PendingCount   int              `json:"pendingCount"`
}

// ---- admin paged listings ----

const (
	defaultAdminPageSize = 20
	maxAdminPageSize     = 100
)

// clampPage normalizes pagination params: page >= 1, 1 <= pageSize <= max.
func clampPage(page, pageSize int) (int, int) {
	if page < 1 {
		page = 1
	}
	if pageSize < 1 {
		pageSize = defaultAdminPageSize
	}
	if pageSize > maxAdminPageSize {
		pageSize = maxAdminPageSize
	}
	return page, pageSize
}

// adminTimeOf parses an RFC3339 timestamp, returning the zero time when
// missing or malformed so such records sort last in descending order.
func adminTimeOf(s string) time.Time {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}
	}
	return t
}

// pageSlice windows a fully sorted slice. total is the pre-window length so
// callers can report it even when the requested page is out of range.
func pageSlice[T any](items []T, page, pageSize int) ([]T, int) {
	total := len(items)
	start := (page - 1) * pageSize
	if start >= total || start < 0 {
		return []T{}, total
	}
	end := start + pageSize
	if end > total {
		end = total
	}
	return items[start:end], total
}

// clampToLastPage resolves an out-of-range request onto the last non-empty
// page (a no-op while the collection is empty).
func clampToLastPage(page, pageSize, total int) int {
	if total > 0 {
		if pages := (total + pageSize - 1) / pageSize; page > pages {
			return pages
		}
	}
	return page
}

func (s *Store) AdminNetworksPage(zombieTTL time.Duration, q, status string, page, pageSize int) ([]networkSummary, int, int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	page, pageSize = clampPage(page, pageSize)
	needle := strings.ToLower(strings.TrimSpace(q))
	out := make([]networkSummary, 0, len(s.networks))
	for _, ns := range s.networks {
		zombie := false
		if zombieTTL > 0 && ns.lastActivityAt > 0 && now.Sub(time.Unix(ns.lastActivityAt, 0)) >= zombieTTL {
			zombie = true
		}
		sum := networkSummary{
			Network:        ns.n,
			Seq:            ns.seq,
			NodeCount:      len(ns.nodes),
			RelayPort:      ns.relayPort,
			Online:         now.Unix()-ns.lastActivityAt < int64(netAliveTTL/time.Second),
			Zombie:         zombie,
			LastActivityAt: ns.lastActivityAt,
			PendingCount:   len(ns.pending),
		}
		switch status {
		case "", "all":
		case "online":
			if !sum.Online {
				continue
			}
		case "offline":
			if sum.Online {
				continue
			}
		case "managed":
			if !ns.n.Managed {
				continue
			}
		case "zombie":
			if !zombie {
				continue
			}
		case "pending":
			if sum.PendingCount == 0 {
				continue
			}
		default:
			continue // unknown status matches nothing
		}
		if needle != "" {
			hay := strings.ToLower(ns.n.Name + " " + ns.n.ID + " " + ns.n.Subnet)
			if !strings.Contains(hay, needle) {
				continue
			}
		}
		out = append(out, sum)
	}
	sort.Slice(out, func(i, j int) bool {
		ti, tj := adminTimeOf(out[i].Network.CreatedAt), adminTimeOf(out[j].Network.CreatedAt)
		if !ti.Equal(tj) {
			return ti.After(tj)
		}
		return out[i].Seq > out[j].Seq
	})
	total := len(out)
	page = clampToLastPage(page, pageSize, total)
	items, _ := pageSlice(out, page, pageSize)
	return items, total, page
}

func (s *Store) AdminNetworks(zombieTTL time.Duration) []networkSummary {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	out := make([]networkSummary, 0, len(s.networks))
	for _, ns := range s.networks {
		zombie := false
		if zombieTTL > 0 && ns.lastActivityAt > 0 && now.Sub(time.Unix(ns.lastActivityAt, 0)) >= zombieTTL {
			zombie = true
		}
		out = append(out, networkSummary{
			Network:        ns.n,
			NodeCount:      len(ns.nodes),
			RelayPort:      ns.relayPort,
			Online:         now.Unix()-ns.lastActivityAt < int64(netAliveTTL/time.Second),
			Zombie:         zombie,
			LastActivityAt: ns.lastActivityAt,
		})
	}
	return out
}

// NodeNetwork returns the network ID that a coordination token belongs to.
func (s *Store) NodeNetwork(token string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	te, ok := s.byToken[hashToken(token)]
	if !ok {
		return "", ErrUnauthorized
	}
	return te.NetworkID, nil
}

// AdminDevices lists all registered device identities.
func (s *Store) AdminDevices() []protocol.Device {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]protocol.Device, 0, len(s.devices))
	for _, d := range s.devices {
		out = append(out, protocol.Device{
			ID:        d.ID,
			PublicKey: d.PublicKey,
			CreatedAt: d.CreatedAt,
			LastSeen:  d.LastSeen,
			Name:      d.Name,
		})
	}
	return out
}

// AdminDevicesPage returns one page of registered devices sorted by creation
// time (newest first), optionally filtered by q across id/name/public key.
func (s *Store) AdminDevicesPage(q string, page, pageSize int) ([]protocol.Device, int, int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	page, pageSize = clampPage(page, pageSize)
	needle := strings.ToLower(strings.TrimSpace(q))
	out := make([]protocol.Device, 0, len(s.devices))
	for _, d := range s.devices {
		dev := protocol.Device{
			ID:        d.ID,
			PublicKey: d.PublicKey,
			CreatedAt: d.CreatedAt,
			LastSeen:  d.LastSeen,
			Name:      d.Name,
		}
		if needle != "" {
			hay := strings.ToLower(dev.ID + " " + dev.Name + " " + dev.PublicKey)
			if !strings.Contains(hay, needle) {
				continue
			}
		}
		out = append(out, dev)
	}
	sort.Slice(out, func(i, j int) bool {
		ti, tj := adminTimeOf(out[i].CreatedAt), adminTimeOf(out[j].CreatedAt)
		if !ti.Equal(tj) {
			return ti.After(tj)
		}
		return out[i].ID < out[j].ID
	})
	total := len(out)
	page = clampToLastPage(page, pageSize, total)
	items, _ := pageSlice(out, page, pageSize)
	return items, total, page
}

// DeviceNetworks returns the networks a device holds a node in.
func (s *Store) DeviceNetworks(deviceID string) []protocol.Network {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []protocol.Network
	for _, ns := range s.networks {
		for _, n := range ns.nodes {
			if n.DeviceID == deviceID {
				c := ns.n
				c.RelayPort = ns.relayPort
				c.NodeCount = len(ns.nodes)
				c.Online = time.Now().Unix()-ns.lastActivityAt < int64(netAliveTTL/time.Second)
				c.LastActivityAt = ns.lastActivityAt
				out = append(out, c)
				break
			}
		}
	}
	return out
}

// DeviceNetworkDetails returns the networks a device holds a node in, including
// per-node credentials (nodeID, IP, token, publicKey) needed to reconstruct
// the client config after a reinstall. New tokens are generated for each node
// because the original tokens are not recoverable from their SHA-256 hashes.
func (s *Store) DeviceNetworkDetails(deviceID string) ([]protocol.DeviceNetworkDetail, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []protocol.DeviceNetworkDetail
	for _, ns := range s.networks {
		for _, n := range ns.nodes {
			if n.DeviceID == deviceID {
				// Generate a fresh token for this node. The old token is
				// lost (client reinstall) and cannot be recovered from its
				// hash, so we rotate it.
				tok, err := randomToken()
				if err != nil {
					return nil, err
				}
				// Remove old token mapping and register the new one.
				s.rotateNodeTokenLocked(ns.n.ID, n.ID, tok)
				c := protocol.DeviceNetworkDetail{
					Network:   ns.n,
					NodeID:    n.ID,
					IP:        n.IP,
					Token:     tok,
					Owner:     ns.n.OwnerDeviceID == deviceID,
					PublicKey: n.PublicKey,
				}
				c.RelayPort = ns.relayPort
				c.NodeCount = len(ns.nodes)
				c.Online = time.Now().Unix()-ns.lastActivityAt < int64(netAliveTTL/time.Second)
				c.LastActivityAt = ns.lastActivityAt
				out = append(out, c)
				break
			}
		}
	}
	return out, nil
}

// rotateNodeTokenLocked removes the old token mapping for a node and registers
// a new one. Callers must hold s.mu.
func (s *Store) rotateNodeTokenLocked(netID, nodeID, newToken string) {
	for h, te := range s.byToken {
		if te.NetworkID == netID && te.NodeID == nodeID {
			delete(s.byToken, h)
			_ = s.deleteTokenByHash(h)
			break
		}
	}
	s.byToken[hashToken(newToken)] = tokenEntry{NetworkID: netID, NodeID: nodeID}
	_ = s.persistToken(newToken, tokenEntry{NetworkID: netID, NodeID: nodeID})
}

// GenerateDeviceToken creates (or rotates) a device-level bearer token that
// authenticates device-scoped API calls (e.g. fetching network details after
// a reinstall). The token is persisted in the device record.
func (s *Store) GenerateDeviceToken(deviceID string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	d := s.devices[deviceID]
	if d == nil {
		return "", ErrNotFound
	}
	tok, err := randomToken()
	if err != nil {
		return "", err
	}
	d.DeviceToken = tok
	if err := s.persistDevice(d); err != nil {
		return "", err
	}
	return tok, nil
}

// ValidateDeviceToken checks whether the presented token matches the stored
// device token for deviceID. Comparison is constant-time.
func (s *Store) ValidateDeviceToken(deviceID, token string) bool {
	if deviceID == "" || token == "" {
		return false
	}
	s.mu.Lock()
	d := s.devices[deviceID]
	if d == nil || d.DeviceToken == "" {
		s.mu.Unlock()
		return false
	}
	stored := d.DeviceToken
	s.mu.Unlock()
	return subtle.ConstantTimeCompare([]byte(stored), []byte(token)) == 1
}

// UpdateNodePublicKey updates the WireGuard public key of a node identified by
// its device binding. This is used after a client reinstall when the device
// generates a new keypair.
func (s *Store) UpdateNodePublicKey(deviceID, networkID, nodeID, publicKey string) error {
	if err := validatePublicKey(publicKey); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	ns := s.networks[networkID]
	if ns == nil {
		return ErrNotFound
	}
	n := ns.nodes[nodeID]
	if n == nil {
		return ErrNotFound
	}
	if n.DeviceID != deviceID {
		return ErrUnauthorized
	}
	n.PublicKey = publicKey
	return s.persistNode(networkID, n)
}

func (s *Store) AdminNodes(nid string) ([]protocol.Node, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ns := s.networks[nid]
	if ns == nil {
		return nil, ErrNotFound
	}
	nodes := make([]protocol.Node, 0, len(ns.nodes))
	for _, n := range ns.nodes {
		node := *n
		if d := s.devices[n.DeviceID]; d != nil {
			node.DeviceName = d.Name
		}
		nodes = append(nodes, node)
	}
	return nodes, nil
}

func (s *Store) AdminRemoveNode(nid, nodeID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	ns := s.networks[nid]
	if ns == nil {
		return ErrNotFound
	}
	if ns.nodes[nodeID] == nil {
		return ErrNotFound
	}
	delete(ns.nodes, nodeID)
	for h, te := range s.byToken {
		if te.NetworkID == nid && te.NodeID == nodeID {
			delete(s.byToken, h)
			if err := s.deleteTokenByHash(h); err != nil {
				return err
			}
		}
	}
	return s.deleteNode(nid, nodeID)
}

// AdminAddExternalNode registers a WireGuard-only node (e.g. a phone using
// the stock WireGuard app) on the given network. It has no coordination token
// of its own beyond a placeholder: peers reach it once it initiates, so its
// endpoint stays empty until the server observes it. Returns node ID and IP.
func (s *Store) AdminAddExternalNode(nid, publicKey string) (string, string, error) {
	if err := validatePublicKey(publicKey); err != nil {
		return "", "", err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	ns := s.networks[nid]
	if ns == nil {
		return "", "", ErrNotFound
	}
	if len(ns.nodes) >= protocol.MaxNodes {
		return "", "", ErrNetworkFull
	}
	ip, err := ns.allocIP()
	if err != nil {
		return "", "", err
	}
	tok, err := randomToken()
	if err != nil {
		return "", "", err
	}
	nodeID, err := randomString(8)
	if err != nil {
		return "", "", err
	}
	node := &protocol.Node{
		ID:        nodeID,
		NetworkID: nid,
		IP:        ip,
		PublicKey: publicKey,
	}
	ns.nodes[nodeID] = node
	s.byToken[hashToken(tok)] = tokenEntry{NetworkID: nid, NodeID: nodeID}

	if s.relayEnabled() {
		if err := s.ensureRelayPort(ns); err != nil {
			return "", "", err
		}
	}
	if err := s.persistNetwork(ns); err != nil {
		return "", "", err
	}
	if err := s.persistNode(nid, node); err != nil {
		return "", "", err
	}
	if err := s.persistToken(tok, tokenEntry{NetworkID: nid, NodeID: nodeID}); err != nil {
		return "", "", err
	}
	return nodeID, ip, nil
}

// AdminResetCode generates a fresh pairing code, invalidating the previous one.
func (s *Store) AdminResetCode(nid string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ns := s.networks[nid]
	if ns == nil {
		return "", ErrNotFound
	}
	code, err := randomString(codeLen)
	if err != nil {
		return "", err
	}
	ns.n.PairingCode = protocol.NormalizeCode(code)
	ns.pairing = newPairing(ns, code)
	if err := s.persistNetwork(ns); err != nil {
		return "", err
	}
	return ns.n.PairingCode, nil
}

func (s *Store) AdminDeleteNetwork(nid string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.networks[nid] == nil {
		return ErrNotFound
	}
	return s.deleteNetworkLocked(nid)
}

// ---- device authorization codes ----

// SetRequireDeviceAuth toggles the enrollment gate. When on, CreateNetwork /
// Join / RegisterDevice are denied for devices that have not bound a generated
// authorization code. Callers hold no locks.
func (s *Store) SetRequireDeviceAuth(on bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.requireDeviceAuth = on
}

// RequireDeviceAuth reports whether the enrollment gate is on.
func (s *Store) RequireDeviceAuth() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.requireDeviceAuth
}

// DeviceBound reports whether deviceID has successfully bound an
// authorization code.
func (s *Store) DeviceBound(deviceID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.deviceBoundLocked(deviceID)
}

// deviceBoundLocked reports whether deviceID holds a valid binding. Callers
// must hold s.mu.
func (s *Store) deviceBoundLocked(deviceID string) bool {
	if deviceID == "" {
		return false
	}
	for _, ac := range s.authCodes {
		if ac.bindingIndex(deviceID) >= 0 {
			return true
		}
	}
	return false
}

// migrateLegacy upgrades a record written before multi-device codes existed:
// the flat DeviceID/PublicKey/BoundAt fields become the first binding. It
// returns true when the record was changed and should be re-persisted. It is
// idempotent: a fresh multi-device record (empty legacy fields) is untouched.
func (ac *authCodeRecord) migrateLegacy() bool {
	if ac.DeviceID == "" || len(ac.Bindings) > 0 {
		return false
	}
	ac.Bindings = []authCodeBinding{{
		DeviceID:  ac.DeviceID,
		PublicKey: ac.PublicKey,
		BoundAt:   ac.BoundAt,
	}}
	if ac.MaxBindings < 1 {
		ac.MaxBindings = 1
	}
	ac.DeviceID = ""
	ac.PublicKey = ""
	ac.BoundAt = time.Time{}
	return true
}

// bindingIndex returns the index of deviceID in Bindings, or -1. Callers must
// hold s.mu.
func (ac *authCodeRecord) bindingIndex(deviceID string) int {
	for i, b := range ac.Bindings {
		if b.DeviceID == deviceID {
			return i
		}
	}
	return -1
}

// removeBinding deletes the binding for deviceID, if present. Callers must
// hold s.mu.
func (ac *authCodeRecord) removeBinding(deviceID string) {
	if i := ac.bindingIndex(deviceID); i >= 0 {
		ac.Bindings = append(ac.Bindings[:i], ac.Bindings[i+1:]...)
	}
}

// maskCode renders a human-readable tail of a code for admin display. The
// full plaintext is never revealed after generation.
func maskCode(code string) string {
	c := protocol.NormalizeCode(code)
	if len(c) <= 4 {
		return "****"
	}
	return "****" + c[len(c)-4:]
}

// AdminGenerateAuthCodes creates count fresh authorization codes, each able to
// bind up to maxBindings devices, and returns their plaintext plus the public
// IDs used for later revocation. Plaintext is also persisted so the admin
// console can display codes at any time.
func (s *Store) AdminGenerateAuthCodes(count, maxBindings int) ([]string, []string, error) {
	if count < 1 {
		count = 1
	}
	if count > 100 {
		return nil, nil, errors.New("count must be between 1 and 100")
	}
	if maxBindings < 1 {
		maxBindings = 1
	}
	if maxBindings > 100 {
		return nil, nil, errors.New("maxBindings must be between 1 and 100")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	codes := make([]string, 0, count)
	ids := make([]string, 0, count)
	for i := 0; i < count; i++ {
		code, err := randomString(protocol.AuthCodeLen)
		if err != nil {
			return nil, nil, err
		}
		id, err := randomString(authCodeIDLen)
		if err != nil {
			return nil, nil, err
		}
		for s.authCodes[id] != nil {
			id, _ = randomString(authCodeIDLen)
		}
		ac := &authCodeRecord{
			ID:          id,
			CodePlain:   protocol.NormalizeCode(code),
			CodeHash:    hashCode(code),
			Hint:        maskCode(code),
			CreatedAt:   time.Now().UTC(),
			MaxBindings: maxBindings,
		}
		s.authCodes[id] = ac
		if err := s.persistAuthCode(ac); err != nil {
			return nil, nil, err
		}
		codes = append(codes, protocol.NormalizeCode(code))
		ids = append(ids, id)
	}
	return codes, ids, nil
}

// AdminAuthCodes lists all authorization codes with their plaintext (masked
// hint only for legacy records whose plaintext was never stored), binding
// capacity and bound devices, newest first.
func (s *Store) AdminAuthCodes() []protocol.AuthCodeInfo {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]protocol.AuthCodeInfo, 0, len(s.authCodes))
	for _, ac := range s.authCodes {
		code := ac.CodePlain
		if code == "" {
			code = ac.Hint // legacy record: plaintext was never stored
		}
		info := protocol.AuthCodeInfo{
			ID:          ac.ID,
			Code:        code,
			CreatedAt:   ac.CreatedAt.UTC().Format(time.RFC3339),
			MaxBindings: ac.MaxBindings,
			BoundCount:  len(ac.Bindings),
		}
		for _, b := range ac.Bindings {
			bi := protocol.AuthCodeBindingInfo{
				DeviceID: b.DeviceID,
				BoundAt:  b.BoundAt.UTC().Format(time.RFC3339),
			}
			if d := s.devices[b.DeviceID]; d != nil {
				bi.DeviceName = d.Name
			}
			info.BoundDevices = append(info.BoundDevices, bi)
		}
		if len(ac.Bindings) > 0 {
			// Backward-compatible single-device fields mirror the first binding.
			info.BoundToDevice = ac.Bindings[0].DeviceID
			info.BoundAt = ac.Bindings[0].BoundAt.UTC().Format(time.RFC3339)
		}
		out = append(out, info)
	}
	return out
}

// AdminAuthCodesPage returns one page of auth codes sorted by creation time
// (newest first).
func (s *Store) AdminAuthCodesPage(page, pageSize int) ([]protocol.AuthCodeInfo, int, int) {
	all := s.AdminAuthCodes()
	sort.Slice(all, func(i, j int) bool {
		ti, tj := adminTimeOf(all[i].CreatedAt), adminTimeOf(all[j].CreatedAt)
		if !ti.Equal(tj) {
			return ti.After(tj)
		}
		return all[i].ID < all[j].ID
	})
	page, pageSize = clampPage(page, pageSize)
	total := len(all)
	page = clampToLastPage(page, pageSize, total)
	items, _ := pageSlice(all, page, pageSize)
	return items, total, page
}

// AdminOverview carries the aggregate numbers shown on the admin overview
// page plus the most recently active networks. It exists so the console can
// render totals without pulling every paged list.
type AdminOverview struct {
	NetworksTotal  int              `json:"networksTotal"`
	NetworksOnline int              `json:"networksOnline"`
	NodesTotal     int              `json:"nodesTotal"`
	PendingTotal   int              `json:"pendingTotal"`
	DevicesTotal   int              `json:"devicesTotal"`
	CodesTotal     int              `json:"codesTotal"`
	CodesBound     int              `json:"codesBound"`
	CodesFree      int              `json:"codesFree"`
	RecentNetworks []networkSummary `json:"recentNetworks"`
}

// AdminOverview computes the aggregate stats for the admin console.
func (s *Store) AdminOverview(zombieTTL time.Duration) AdminOverview {
	ov := AdminOverview{}
	s.mu.Lock()
	now := time.Now()
	recent := make([]networkSummary, 0, len(s.networks))
	for _, ns := range s.networks {
		online := now.Unix()-ns.lastActivityAt < int64(netAliveTTL/time.Second)
		ov.NetworksTotal++
		if online {
			ov.NetworksOnline++
		}
		ov.NodesTotal += len(ns.nodes)
		ov.PendingTotal += len(ns.pending)
		zombie := false
		if zombieTTL > 0 && ns.lastActivityAt > 0 && now.Sub(time.Unix(ns.lastActivityAt, 0)) >= zombieTTL {
			zombie = true
		}
		recent = append(recent, networkSummary{
			Network:        ns.n,
			NodeCount:      len(ns.nodes),
			RelayPort:      ns.relayPort,
			Online:         online,
			Zombie:         zombie,
			LastActivityAt: ns.lastActivityAt,
			PendingCount:   len(ns.pending),
		})
	}
	ov.DevicesTotal = len(s.devices)
	for _, ac := range s.authCodes {
		ov.CodesTotal++
		if len(ac.Bindings) > 0 {
			ov.CodesBound++
		} else {
			ov.CodesFree++
		}
	}
	sort.Slice(recent, func(i, j int) bool { return recent[i].LastActivityAt > recent[j].LastActivityAt })
	s.mu.Unlock()
	if len(recent) > 6 {
		recent = recent[:6]
	}
	ov.RecentNetworks = recent
	return ov
}

// AdminRevokeAuthCode permanently deletes a code by its public ID. A bound
// code is revoked too; the device keeps access to networks it already joined
// until its nodes are removed.
func (s *Store) AdminRevokeAuthCode(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.authCodes[id] == nil {
		return ErrNotFound
	}
	delete(s.authCodes, id)
	return s.deleteAuthCode(id)
}

// AdminUnbindDevice releases deviceID from every authorization code, freeing
// its binding slot for reuse. The device can no longer create/join networks
// while the gate is on; existing memberships remain until the admin removes
// its nodes.
func (s *Store) AdminUnbindDevice(deviceID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	found := false
	for _, ac := range s.authCodes {
		if ac.bindingIndex(deviceID) >= 0 {
			ac.removeBinding(deviceID)
			if err := s.persistAuthCode(ac); err != nil {
				return err
			}
			found = true
		}
	}
	if !found {
		return ErrNotFound
	}
	return nil
}

// AdminDeleteDevice removes a device from the store after unbinding it from
// all authorization codes and removing its nodes from every network.
func (s *Store) AdminDeleteDevice(deviceID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.devices[deviceID] == nil {
		return ErrNotFound
	}
	// 1. Unbind from all authorization codes.
	for _, ac := range s.authCodes {
		if ac.bindingIndex(deviceID) >= 0 {
			ac.removeBinding(deviceID)
			if err := s.persistAuthCode(ac); err != nil {
				return err
			}
		}
	}
	// 2. Remove from all networks.
	for nid, ns := range s.networks {
		for nodeID, n := range ns.nodes {
			if n.DeviceID == deviceID {
				delete(ns.nodes, nodeID)
				for h, te := range s.byToken {
					if te.NetworkID == nid && te.NodeID == nodeID {
						delete(s.byToken, h)
						if err := s.deleteTokenByHash(h); err != nil {
							return err
						}
					}
				}
				if err := s.deleteNode(nid, nodeID); err != nil {
					return err
				}
			}
		}
	}
	// 3. Delete the device record.
	delete(s.devices, deviceID)
	return s.deleteDevice(deviceID)
}

// AdminRenameDevice sets a device's display name.
func (s *Store) AdminRenameDevice(deviceID, name string) error {
	name = normalizeName(name)
	s.mu.Lock()
	defer s.mu.Unlock()
	d := s.devices[deviceID]
	if d == nil {
		return ErrNotFound
	}
	d.Name = name
	return s.persistDevice(d)
}

func (s *Store) deleteDevice(deviceID string) error {
	if s.db == nil {
		return nil
	}
	return s.db.Update(func(tx *bbolt.Tx) error {
		if err := s.ensureBuckets(tx); err != nil {
			return err
		}
		return tx.Bucket(bktDevices).Delete([]byte(deviceID))
	})
}

// BindDevice consumes a generated authorization code for deviceID. Binding is
// idempotent for the same device and same code; a code already at its
// MaxBindings rejects further devices with ErrAuthCodeFull; binding a new code
// automatically releases the device's previous code so an operator can rotate
// codes by handing out fresh ones. A device is bound to at most one code.
// Returns the device token for device-scoped API calls (e.g. network sync
// after reinstall).
func (s *Store) BindDevice(code, deviceID, publicKey, name string) (string, error) {
	if err := validateDeviceID(deviceID); err != nil {
		return "", err
	}
	if err := validatePublicKey(publicKey); err != nil {
		return "", err
	}
	if deviceID == "" {
		return "", errors.New("missing deviceId")
	}
	h := hashCode(code)
	s.mu.Lock()
	defer s.mu.Unlock()

	var found *authCodeRecord
	for _, ac := range s.authCodes {
		if ac.CodeHash == h {
			found = ac
			break
		}
	}
	if found == nil {
		return "", ErrAuthCodeInvalid
	}
	if found.bindingIndex(deviceID) >= 0 {
		// Idempotent: same device, same code. Still return the device token.
		d := s.devices[deviceID]
		if d != nil && d.DeviceToken != "" {
			s.fillDeviceNameLocked(d, name)
			return d.DeviceToken, nil
		}
		// First time binding with this device: generate a token.
		tok, err := randomToken()
		if err != nil {
			return "", err
		}
		if d == nil {
			d = &deviceRecord{
				ID:        deviceID,
				CreatedAt: time.Now().UTC().Format(time.RFC3339),
			}
			s.devices[deviceID] = d
		}
		d.DeviceToken = tok
		d.PublicKey = publicKey
		s.fillDeviceNameLocked(d, name)
		d.LastSeen = time.Now().Unix()
		if err := s.persistDevice(d); err != nil {
			return "", err
		}
		return tok, nil
	}
	if len(found.Bindings) >= found.MaxBindings {
		return "", ErrAuthCodeFull
	}
	// Rotation: release the device from any other code first so it holds
	// exactly one binding.
	for _, ac := range s.authCodes {
		if ac.bindingIndex(deviceID) >= 0 {
			ac.removeBinding(deviceID)
			if err := s.persistAuthCode(ac); err != nil {
				return "", err
			}
		}
	}
	found.Bindings = append(found.Bindings, authCodeBinding{
		DeviceID:  deviceID,
		PublicKey: publicKey,
		BoundAt:   time.Now().UTC(),
	})
	if err := s.persistAuthCode(found); err != nil {
		return "", err
	}
	// Registering the device here keeps the device list consistent even when
	// the client never calls /api/v1/devices.
	if err := s.upsertDeviceLocked(deviceID, publicKey, name); err != nil {
		return "", err
	}
	// Generate a device token for device-scoped API calls.
	tok, err := randomToken()
	if err != nil {
		return "", err
	}
	d := s.devices[deviceID]
	if d != nil {
		d.DeviceToken = tok
		if err := s.persistDevice(d); err != nil {
			return "", err
		}
	}
	return tok, nil
}

// ---- admin credentials ----

// EnsureAdminPassword seeds the bcrypt hash of pass for user if none is
// recorded yet and returns the active hash. A persisted hash always wins, so
// the flag/env password only acts as the initial bootstrap password. Callers
// hold no locks.
func (s *Store) EnsureAdminPassword(user, pass string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if h := s.adminPasswordHashLocked(user); h != "" {
		return h, nil
	}
	if pass == "" {
		return "", nil
	}
	h, err := bcrypt.GenerateFromPassword([]byte(pass), bcrypt.DefaultCost)
	if err != nil {
		return "", err
	}
	if err := s.putAdminPasswordLocked(user, string(h)); err != nil {
		return "", err
	}
	return string(h), nil
}

// SetAdminPassword replaces the stored bcrypt hash for user and returns the
// new hash so callers can keep an in-memory copy. Callers hold no locks.
func (s *Store) SetAdminPassword(user, newPassword string) (string, error) {
	h, err := bcrypt.GenerateFromPassword([]byte(newPassword), bcrypt.DefaultCost)
	if err != nil {
		return "", err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return string(h), s.putAdminPasswordLocked(user, string(h))
}

// adminPasswordHashLocked reads the stored bcrypt hash for user, if any.
// Callers must hold s.mu.
func (s *Store) adminPasswordHashLocked(user string) string {
	if s.db == nil {
		return ""
	}
	var out string
	_ = s.db.View(func(tx *bbolt.Tx) error {
		b := tx.Bucket(bktAdmin)
		if b == nil {
			return nil
		}
		out = string(b.Get([]byte(user)))
		return nil
	})
	return out
}

// adminPasswordHash reads the stored bcrypt hash for user, if any.
func (s *Store) adminPasswordHash(user string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.adminPasswordHashLocked(user)
}

// putAdminPasswordLocked writes the bcrypt hash for user. Callers must hold
// s.mu.
func (s *Store) putAdminPasswordLocked(user, hash string) error {
	if s.db == nil {
		return nil
	}
	return s.db.Update(func(tx *bbolt.Tx) error {
		if err := s.ensureBuckets(tx); err != nil {
			return err
		}
		return tx.Bucket(bktAdmin).Put([]byte(user), []byte(hash))
	})
}

// AdminUsernames lists the admin usernames recorded in the store, sorted.
func (s *Store) AdminUsernames() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db == nil {
		return nil
	}
	var out []string
	_ = s.db.View(func(tx *bbolt.Tx) error {
		b := tx.Bucket(bktAdmin)
		if b == nil {
			return nil
		}
		return b.ForEach(func(k, _ []byte) error {
			if len(k) > 0 {
				out = append(out, string(k))
			}
			return nil
		})
	})
	sort.Strings(out)
	return out
}

// BootstrapAdmin creates the very first admin account and returns its bcrypt
// hash. It refuses when any admin account already exists so the web setup
// wizard cannot be replayed after initialization.
func (s *Store) BootstrapAdmin(user, pass string) (string, error) {
	h, err := bcrypt.GenerateFromPassword([]byte(pass), bcrypt.DefaultCost)
	if err != nil {
		return "", err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db != nil {
		exists := false
		_ = s.db.View(func(tx *bbolt.Tx) error {
			if b := tx.Bucket(bktAdmin); b != nil {
				k, _ := b.Cursor().First()
				exists = k != nil
			}
			return nil
		})
		if exists {
			return "", ErrAdminExists
		}
	}
	return string(h), s.putAdminPasswordLocked(user, string(h))
}

// UpdateAllowedSubnets replaces the CIDR subnets a node advertises for
// routing. Each subnet is validated, must not overlap with the network's own
// subnet, and must not conflict with another node's advertised subnets.
func (s *Store) UpdateAllowedSubnets(token string, subnets []string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	te, ok := s.byToken[hashToken(token)]
	if !ok {
		return ErrUnauthorized
	}
	ns := s.networks[te.NetworkID]
	if ns == nil {
		return ErrNotFound
	}
	me := ns.nodes[te.NodeID]
	if me == nil {
		return ErrNotFound
	}
	// Validate each subnet.
	seen := map[string]struct{}{}
	for _, raw := range subnets {
		cidr, err := validateSubnet(raw)
		if err != nil {
			return fmt.Errorf("子网 %q 无效: %w", raw, err)
		}
		if _, dup := seen[cidr]; dup {
			return fmt.Errorf("重复子网 %s", cidr)
		}
		seen[cidr] = struct{}{}
		// Must not overlap with the network's own VPN subnet.
		if ns.n.Subnet != "" && subnetsOverlap(cidr, ns.n.Subnet) {
			return fmt.Errorf("子网 %s 与网络 VPN 网段 %s 重叠", cidr, ns.n.Subnet)
		}
	}
	// Must not conflict with other nodes' advertised subnets.
	for id, n := range ns.nodes {
		if id == te.NodeID {
			continue
		}
		for _, other := range n.AllowedSubnets {
			_, oc, _ := net.ParseCIDR(other)
			if oc == nil {
				continue
			}
			for _, raw := range subnets {
				cidr, _ := validateSubnet(raw)
				_, ac, _ := net.ParseCIDR(cidr)
				if ac == nil {
					continue
				}
				if oc.Contains(ac.IP) || ac.Contains(oc.IP) {
					return fmt.Errorf("子网 %s 与节点 %s 宣告的 %s 冲突", cidr, n.ID, other)
				}
			}
		}
	}
	me.AllowedSubnets = subnets
	return s.persistNode(te.NetworkID, me)
}
