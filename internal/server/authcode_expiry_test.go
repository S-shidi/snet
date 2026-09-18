package server

import (
	"errors"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"snet/internal/protocol"
)

func pastTime() *time.Time {
	p := time.Now().UTC().Add(-time.Hour)
	return &p
}

// expireCodeLocked rewrites a code's ExpiresAt in place to simulate a code
// that was generated earlier and has since lapsed (white-box test helper).
func expireCode(t *testing.T, s *Store, id string) {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	ac := s.authCodes[id]
	if ac == nil {
		t.Fatalf("code %s not found", id)
	}
	ac.ExpiresAt = pastTime()
}

// TestAuthCodeExpiredBindRejected verifies BindDevice rejects both a fresh
// binding and an idempotent re-bind once the code has expired.
func TestAuthCodeExpiredBindRejected(t *testing.T) {
	s := NewStore()
	s.SetRequireDeviceAuth(true)
	future := time.Now().Add(time.Hour)
	codes, err := s.AdminGenerateAuthCodes(1, 2, &future)
	if err != nil {
		t.Fatal(err)
	}
	code, id := codes[0].Code, codes[0].ID
	if _, err := s.BindDevice(code, "dev-aaa1", testKey(1), ""); err != nil {
		t.Fatalf("bind: %v", err)
	}
	expireCode(t, s, id)

	// fresh device → rejected
	if _, err := s.BindDevice(code, "dev-bbb2", testKey(2), ""); !errors.Is(err, ErrAuthCodeExpired) {
		t.Fatalf("bind after expiry = %v, want ErrAuthCodeExpired", err)
	}
	// idempotent re-bind of the already-bound device → also rejected
	if _, err := s.BindDevice(code, "dev-aaa1", testKey(1), ""); !errors.Is(err, ErrAuthCodeExpired) {
		t.Fatalf("idempotent rebind after expiry = %v, want ErrAuthCodeExpired", err)
	}
	// device is still considered bound (expiry is separate from unbinding)
	if !s.DeviceBound("dev-aaa1") {
		t.Fatal("bound device lost binding on expiry")
	}
	if err := s.CheckDeviceAuth("dev-aaa1"); !errors.Is(err, ErrAuthCodeExpired) {
		t.Fatalf("CheckDeviceAuth after expiry = %v, want ErrAuthCodeExpired", err)
	}
}

// TestGetDeviceAuthStatus covers the bound/unbound/expired states returned to
// clients polling the auth-status endpoint.
func TestGetDeviceAuthStatus(t *testing.T) {
	s := NewStore()
	if st := s.GetDeviceAuthStatus("dev-ghost"); st.Bound || st.Expired {
		t.Fatalf("unknown device status = %+v", st)
	}

	future := time.Now().Add(time.Hour)
	codes, err := s.AdminGenerateAuthCodes(1, 2, &future)
	if err != nil {
		t.Fatal(err)
	}
	code, id := codes[0].Code, codes[0].ID
	if _, err := s.BindDevice(code, "dev-aaa1", testKey(1), ""); err != nil {
		t.Fatal(err)
	}
	st := s.GetDeviceAuthStatus("dev-aaa1")
	if !st.Bound || st.Expired || st.AuthCodeID != id || st.ExpiresAt == nil {
		t.Fatalf("active status = %+v", st)
	}

	expireCode(t, s, id)
	st = s.GetDeviceAuthStatus("dev-aaa1")
	if !st.Bound || !st.Expired || st.Message == "" {
		t.Fatalf("expired status = %+v", st)
	}

	// an unbound-but-registered device
	if err := s.RegisterDevice("dev-nobind", testKey(3), ""); err != nil {
		t.Fatal(err)
	}
	if st := s.GetDeviceAuthStatus("dev-nobind"); st.Bound {
		t.Fatalf("unbound registered device status = %+v", st)
	}
}

// TestAdminRenewAuthCode verifies renewing extends an active code, can make it
// permanent, rejects past expirations and unknown IDs, and unblocks a device
// whose code had lapsed.
func TestAdminRenewAuthCode(t *testing.T) {
	s := NewStore()
	future := time.Now().Add(time.Hour)
	codes, err := s.AdminGenerateAuthCodes(1, 2, &future)
	if err != nil {
		t.Fatal(err)
	}
	code, id := codes[0].Code, codes[0].ID
	if _, err := s.BindDevice(code, "dev-aaa1", testKey(1), ""); err != nil {
		t.Fatal(err)
	}
	expireCode(t, s, id)
	if _, err := s.BindDevice(code, "dev-bbb2", testKey(2), ""); !errors.Is(err, ErrAuthCodeExpired) {
		t.Fatalf("bind while expired = %v", err)
	}

	// unknown ID
	if err := s.AdminRenewAuthCode("nonexistent", &future); !errors.Is(err, ErrNotFound) {
		t.Fatalf("renew unknown = %v, want ErrNotFound", err)
	}
	// past expiration rejected
	if err := s.AdminRenewAuthCode(id, pastTime()); err == nil {
		t.Fatal("renew to the past should be rejected")
	}

	// extend into the future → device unblocked again
	next := time.Now().Add(48 * time.Hour)
	if err := s.AdminRenewAuthCode(id, &next); err != nil {
		t.Fatalf("renew: %v", err)
	}
	if _, err := s.BindDevice(code, "dev-bbb2", testKey(2), ""); err != nil {
		t.Fatalf("bind after renew = %v", err)
	}
	if st := s.GetDeviceAuthStatus("dev-aaa1"); st.Expired {
		t.Fatalf("expired after renew = %+v", st)
	}

	// make permanent (nil expiration)
	if err := s.AdminRenewAuthCode(id, nil); err != nil {
		t.Fatalf("renew permanent: %v", err)
	}
	if st := s.GetDeviceAuthStatus("dev-aaa1"); st.Expired || st.ExpiresAt != nil {
		t.Fatalf("permanent status = %+v", st)
	}
	for _, c := range s.AdminAuthCodes() {
		if c.ID == id && c.Status != "permanent" {
			t.Fatalf("status after permanent renew = %q", c.Status)
		}
	}
}

// TestAuthCodeExpiryPersistence verifies expiresAt survives a store reopen and
// is enforced afterwards.
func TestAuthCodeExpiryPersistence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "snet.db")
	s, err := NewStoreAt(path)
	if err != nil {
		t.Fatal(err)
	}
	future := time.Now().Add(24 * time.Hour)
	codes, err := s.AdminGenerateAuthCodes(1, 1, &future)
	if err != nil {
		t.Fatal(err)
	}
	code, id := codes[0].Code, codes[0].ID
	if _, err := s.BindDevice(code, "dev-aaa1", testKey(1), ""); err != nil {
		t.Fatal(err)
	}
	expireCode(t, s, id)
	// persist the mutation before closing so reopen sees it
	if err := s.persistAuthCode(s.authCodes[id]); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	s2, err := NewStoreAt(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	c := s2.AdminAuthCodes()[0]
	if c.ID != id || c.Status != "expired" || c.ExpiresAt == "" {
		t.Fatalf("persisted expiry wrong: %+v", c)
	}
	if !s2.GetDeviceAuthStatus("dev-aaa1").Expired {
		t.Fatal("expired state lost across restart")
	}
	if _, err := s2.BindDevice(code, "dev-aaa1", testKey(1), ""); !errors.Is(err, ErrAuthCodeExpired) {
		t.Fatalf("rebind after persisted expiry = %v, want ErrAuthCodeExpired", err)
	}
}

// TestAuthCodeExpiredGateHTTP verifies an expired bound device receives 403
// (not 500) on create/join/register after a real (short) expiration window.
func TestAuthCodeExpiredGateHTTP(t *testing.T) {
	ts, s := newTestServerOpts(t, Options{RequireDeviceAuth: true})
	soon := time.Now().Add(300 * time.Millisecond)
	codes, err := s.AdminGenerateAuthCodes(1, 1, &soon)
	if err != nil {
		t.Fatal(err)
	}
	code := codes[0].Code
	var bindResp protocol.BindDeviceResp
	resp := doJSON(t, http.MethodPost, ts.URL+"/api/v1/devices/bind", "",
		protocol.BindDeviceReq{Code: code, DeviceID: "dev-aaaa", PublicKey: testKey(1)}, &bindResp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("bind status = %d", resp.StatusCode)
	}
	if bindResp.DeviceToken == "" {
		t.Fatal("bind returned no device token")
	}

	time.Sleep(400 * time.Millisecond)

	// create → 403 (the bug was a 500 via handleStoreErr default)
	resp = doJSON(t, http.MethodPost, ts.URL+"/api/v1/networks", "",
		protocol.CreateNetworkReq{PublicKey: testKey(1), DeviceID: "dev-aaaa"}, nil)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("expired create status = %d, want 403", resp.StatusCode)
	}
	// register → 403
	resp = doJSON(t, http.MethodPost, ts.URL+"/api/v1/devices", "",
		protocol.RegisterDeviceReq{DeviceID: "dev-aaaa", PublicKey: testKey(1)}, nil)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("expired register status = %d, want 403", resp.StatusCode)
	}
	// join → 403
	resp = doJSON(t, http.MethodPost, ts.URL+"/api/v1/networks/NET1234/join", "",
		protocol.JoinReq{Code: "ABCDEFGHIJKL", PublicKey: testKey(2), DeviceID: "dev-aaaa"}, nil)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("expired join status = %d, want 403", resp.StatusCode)
	}
}

// TestAuthStatusEndpointTokenRequirement verifies the auth-status endpoint
// only answers for devices with a valid device token once bound.
func TestAuthStatusEndpointTokenRequirement(t *testing.T) {
	ts, s := newTestServerOpts(t, Options{})

	// unbound device needs no token and gets Bound:false
	var st protocol.DeviceAuthStatusResp
	resp := doJSON(t, http.MethodGet, ts.URL+"/api/v1/devices/auth-status?deviceId=dev-aaaa", "", nil, &st)
	if resp.StatusCode != http.StatusOK || st.Bound {
		t.Fatalf("unbound auth-status = %d %+v", resp.StatusCode, st)
	}

	code, _ := genOneCode(t, s)
	var bindResp protocol.BindDeviceResp
	resp = doJSON(t, http.MethodPost, ts.URL+"/api/v1/devices/bind", "",
		protocol.BindDeviceReq{Code: code, DeviceID: "dev-aaaa", PublicKey: testKey(1)}, &bindResp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("bind status = %d", resp.StatusCode)
	}

	// missing token on a bound device → 401, no leakage
	resp = doJSON(t, http.MethodGet, ts.URL+"/api/v1/devices/auth-status?deviceId=dev-aaaa", "", nil, nil)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("bound without token status = %d, want 401", resp.StatusCode)
	}
	// wrong token → 401
	resp = doJSONH(t, http.MethodGet, ts.URL+"/api/v1/devices/auth-status?deviceId=dev-aaaa", "",
		map[string]string{"X-Device-Token": "wrong-token"}, nil, nil)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("bound with wrong token status = %d, want 401", resp.StatusCode)
	}
	// correct token → 200 Bound:true
	resp = doJSONH(t, http.MethodGet, ts.URL+"/api/v1/devices/auth-status?deviceId=dev-aaaa", "",
		map[string]string{"X-Device-Token": bindResp.DeviceToken}, nil, &st)
	if resp.StatusCode != http.StatusOK || !st.Bound || st.Expired {
		t.Fatalf("bound with token = %d %+v", resp.StatusCode, st)
	}
}

// TestAdminOverviewCodesExpired verifies the overview counts expired codes.
func TestAdminOverviewCodesExpired(t *testing.T) {
	s := NewStore()
	if _, err := s.AdminGenerateAuthCodes(1, 1, nil); err != nil { // permanent
		t.Fatal(err)
	}
	future := time.Now().Add(time.Hour)
	codes, err := s.AdminGenerateAuthCodes(2, 1, &future)
	if err != nil {
		t.Fatal(err)
	}
	// bind one of the dated codes so a single record is both bound and dated
	if _, err := s.BindDevice(codes[0].Code, "dev-aaa1", testKey(1), ""); err != nil {
		t.Fatal(err)
	}
	// let both dated codes lapse (one bound, one free)
	expireCode(t, s, codes[0].ID)
	expireCode(t, s, codes[1].ID)

	ov := s.AdminOverview(0)
	if ov.CodesTotal != 3 {
		t.Fatalf("codesTotal = %d, want 3", ov.CodesTotal)
	}
	if ov.CodesBound != 1 || ov.CodesFree != 2 {
		t.Fatalf("bound/free = %d/%d", ov.CodesBound, ov.CodesFree)
	}
	if ov.CodesExpired != 2 { // both dated codes are expired
		t.Fatalf("codesExpired = %d, want 2", ov.CodesExpired)
	}
}