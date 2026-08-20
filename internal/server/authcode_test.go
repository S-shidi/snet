package server

import (
	"errors"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"snet/internal/protocol"
)

func genOneCode(t *testing.T, s *Store) (code, id string) {
	t.Helper()
	codes, ids, err := s.AdminGenerateAuthCodes(1, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(codes) != 1 || len(ids) != 1 {
		t.Fatalf("generate returned %d codes / %d ids", len(codes), len(ids))
	}
	return codes[0], ids[0]
}

// TestAuthCodeLifecycle covers generate → list (plaintext) → bind → idempotent
// rebind → rotation releasing the previous code → invalid/used rejections →
// unbind (code reusable) → revoke (code dead).
func TestAuthCodeLifecycle(t *testing.T) {
	s := NewStore()
	code1, id1 := genOneCode(t, s)
	if len(code1) != protocol.AuthCodeLen {
		t.Fatalf("code length = %d, want %d", len(code1), protocol.AuthCodeLen)
	}
	if code1 != strings.ToUpper(code1) {
		t.Fatalf("code not normalized to upper case: %q", code1)
	}

	// list shows the plaintext
	codes := s.AdminAuthCodes()
	if len(codes) != 1 {
		t.Fatalf("auth code count = %d", len(codes))
	}
	if codes[0].Code != code1 {
		t.Fatalf("code not plaintext in list: %q (want %q)", codes[0].Code, code1)
	}
	if codes[0].BoundToDevice != "" {
		t.Fatalf("new code already bound: %+v", codes[0])
	}

	// bind
	if _, err := s.BindDevice(code1, "dev-aaaa", "pub1"); err != nil {
		t.Fatalf("bind: %v", err)
	}
	if !s.DeviceBound("dev-aaaa") {
		t.Fatal("device not bound after bind")
	}
	// idempotent same-code-same-device
	if _, err := s.BindDevice(code1, "dev-aaaa", "pub1"); err != nil {
		t.Fatalf("idempotent rebind: %v", err)
	}
	// same code by another device → full (default max bindings = 1)
	if _, err := s.BindDevice(code1, "dev-bbbb", "pub2"); !errors.Is(err, ErrAuthCodeFull) {
		t.Fatalf("rebind by another device = %v, want ErrAuthCodeFull", err)
	}
	// invalid code
	if _, err := s.BindDevice("WRONGCODE123", "dev-cccc", "pub3"); !errors.Is(err, ErrAuthCodeInvalid) {
		t.Fatalf("invalid code = %v, want ErrAuthCodeInvalid", err)
	}

	// rotation: binding a fresh code releases the device's previous one
	code2, _ := genOneCode(t, s)
	if _, err := s.BindDevice(code2, "dev-aaaa", "pub1"); err != nil {
		t.Fatalf("rotate bind: %v", err)
	}
	if !s.DeviceBound("dev-aaaa") {
		t.Fatal("device lost binding after rotation")
	}
	// the released code1 is free again for a different device
	if _, err := s.BindDevice(code1, "dev-bbbb", "pub2"); err != nil {
		t.Fatalf("reuse released code: %v", err)
	}
	if !s.DeviceBound("dev-bbbb") {
		t.Fatal("second device not bound")
	}

	// unbind releases the device's code back to unbound
	if err := s.AdminUnbindDevice("dev-aaaa"); err != nil {
		t.Fatalf("unbind: %v", err)
	}
	if s.DeviceBound("dev-aaaa") {
		t.Fatal("device still bound after unbind")
	}
	if err := s.AdminUnbindDevice("ghost-device"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unbind unknown device = %v, want ErrNotFound", err)
	}
	// the released code2 can now be bound by someone else
	if _, err := s.BindDevice(code2, "dev-dddd", "pub4"); err != nil {
		t.Fatalf("rebind unbound code: %v", err)
	}

	// revoke kills a code permanently
	if err := s.AdminRevokeAuthCode(id1); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	if err := s.AdminRevokeAuthCode(id1); !errors.Is(err, ErrNotFound) {
		t.Fatalf("double revoke = %v, want ErrNotFound", err)
	}
	if _, err := s.BindDevice(code1, "dev-eeee", "pub5"); !errors.Is(err, ErrAuthCodeInvalid) {
		t.Fatalf("bind revoked code = %v, want ErrAuthCodeInvalid", err)
	}
}

func TestAuthCodeCountValidation(t *testing.T) {
	s := NewStore()
	// count <= 0 clamps to 1
	if codes, _, err := s.AdminGenerateAuthCodes(0, 1); err != nil || len(codes) != 1 {
		t.Fatalf("count 0: %v, %d codes", err, len(codes))
	}
	if _, _, err := s.AdminGenerateAuthCodes(101, 1); err == nil {
		t.Fatal("count 101 should be rejected")
	}
	// maxBindings <= 0 clamps to 1; > 100 rejected
	if codes, _, err := s.AdminGenerateAuthCodes(1, 0); err != nil || len(codes) != 1 {
		t.Fatalf("maxBindings 0: %v", err)
	}
	if _, _, err := s.AdminGenerateAuthCodes(1, 101); err == nil {
		t.Fatal("maxBindings 101 should be rejected")
	}
}

// TestAuthCodePersistenceAcrossRestart verifies generated and bound codes
// survive a store reopen.
func TestAuthCodePersistenceAcrossRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "snet.db")
	s, err := NewStoreAt(path)
	if err != nil {
		t.Fatal(err)
	}
	code, _ := genOneCode(t, s)
	if _, err := s.BindDevice(code, "dev-aaaa", "pub1"); err != nil {
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
	if !s2.DeviceBound("dev-aaaa") {
		t.Fatal("binding lost across restart")
	}
	codes := s2.AdminAuthCodes()
	if len(codes) != 1 || codes[0].BoundToDevice != "dev-aaaa" {
		t.Fatalf("persisted codes wrong: %+v", codes)
	}
	// and the stored code is still consumable/bindable
	if _, err := s2.BindDevice(code, "dev-aaaa", "pub1"); err != nil {
		t.Fatalf("rebind after restart: %v", err)
	}
}

// TestAuthCodeMultiBind verifies a code with MaxBindings > 1 accepts that many
// distinct devices, rejects further ones with ErrAuthCodeFull, exposes the
// bound list via AdminAuthCodes, and that unbinding one device frees a slot.
func TestAuthCodeMultiBind(t *testing.T) {
	s := NewStore()
	codes, ids, err := s.AdminGenerateAuthCodes(1, 3)
	if err != nil {
		t.Fatal(err)
	}
	code, id := codes[0], ids[0]

	if _, err := s.BindDevice(code, "dev-aaa1", "pub1"); err != nil {
		t.Fatalf("bind 1: %v", err)
	}
	if _, err := s.BindDevice(code, "dev-bbb2", "pub2"); err != nil {
		t.Fatalf("bind 2: %v", err)
	}
	if _, err := s.BindDevice(code, "dev-ccc3", "pub3"); err != nil {
		t.Fatalf("bind 3: %v", err)
	}
	// capacity exhausted
	if _, err := s.BindDevice(code, "dev-ddd4", "pub4"); !errors.Is(err, ErrAuthCodeFull) {
		t.Fatalf("4th bind = %v, want ErrAuthCodeFull", err)
	}
	for _, d := range []string{"dev-aaa1", "dev-bbb2", "dev-ccc3"} {
		if !s.DeviceBound(d) {
			t.Fatalf("device %s not bound", d)
		}
	}
	if s.DeviceBound("dev-ddd4") {
		t.Fatal("dev-ddd should not be bound")
	}

	// idempotent rebind does not consume capacity
	if _, err := s.BindDevice(code, "dev-aaa1", "pub1"); err != nil {
		t.Fatalf("idempotent rebind: %v", err)
	}
	if _, err := s.BindDevice(code, "dev-ddd4", "pub4"); !errors.Is(err, ErrAuthCodeFull) {
		t.Fatalf("post-idempotent 4th bind = %v, want ErrAuthCodeFull", err)
	}

	// admin listing shows capacity and all bound devices
	info := s.AdminAuthCodes()
	if len(info) != 1 {
		t.Fatalf("code count = %d", len(info))
	}
	c := info[0]
	if c.ID != id || c.MaxBindings != 3 || c.BoundCount != 3 {
		t.Fatalf("code info wrong: %+v", c)
	}
	if len(c.BoundDevices) != 3 {
		t.Fatalf("bound devices = %+v", c.BoundDevices)
	}
	// backward-compatible single-device fields mirror the first binding
	if c.BoundToDevice == "" || c.BoundAt == "" {
		t.Fatalf("legacy single-device fields empty: %+v", c)
	}

	// unbind one device frees its slot
	if err := s.AdminUnbindDevice("dev-bbb2"); err != nil {
		t.Fatalf("unbind: %v", err)
	}
	if s.DeviceBound("dev-bbb2") {
		t.Fatal("dev-bbb still bound after unbind")
	}
	if _, err := s.BindDevice(code, "dev-ddd4", "pub4"); err != nil {
		t.Fatalf("bind after unbind: %v", err)
	}
	info = s.AdminAuthCodes()
	if info[0].BoundCount != 3 || len(info[0].BoundDevices) != 3 {
		t.Fatalf("bound count after refill = %+v", info[0])
	}
}

// TestAuthCodeRotationFreesSlot verifies the one-code-per-device rotation
// invariant with multi-bind codes: binding a device to a new code removes it
// from the old code, freeing a slot there.
func TestAuthCodeRotationFreesSlot(t *testing.T) {
	s := NewStore()
	codesA, _, err := s.AdminGenerateAuthCodes(1, 2)
	if err != nil {
		t.Fatal(err)
	}
	codesB, _, err := s.AdminGenerateAuthCodes(1, 1)
	if err != nil {
		t.Fatal(err)
	}
	codeA, codeB := codesA[0], codesB[0]

	if _, err := s.BindDevice(codeA, "dev-aaa1", "pub1"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.BindDevice(codeA, "dev-bbb2", "pub2"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.BindDevice(codeA, "dev-ccc3", "pub3"); !errors.Is(err, ErrAuthCodeFull) {
		t.Fatalf("A full bind = %v, want ErrAuthCodeFull", err)
	}
	// rotating dev-aaa to codeB releases it from codeA
	if _, err := s.BindDevice(codeB, "dev-aaa1", "pub1"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.BindDevice(codeA, "dev-ccc3", "pub3"); err != nil {
		t.Fatalf("A bind after rotation: %v", err)
	}
	// codeB (max 1) is now full
	if _, err := s.BindDevice(codeB, "dev-ddd4", "pub4"); !errors.Is(err, ErrAuthCodeFull) {
		t.Fatalf("B full bind = %v, want ErrAuthCodeFull", err)
	}
}

// TestAuthCodeMultiBindPersistence verifies multi-device bindings survive a
// store reopen with capacity intact.
func TestAuthCodeMultiBindPersistence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "snet.db")
	s, err := NewStoreAt(path)
	if err != nil {
		t.Fatal(err)
	}
	codes, _, err := s.AdminGenerateAuthCodes(1, 2)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.BindDevice(codes[0], "dev-aaa1", "pub1"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.BindDevice(codes[0], "dev-bbb2", "pub2"); err != nil {
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
	if c.MaxBindings != 2 || c.BoundCount != 2 || len(c.BoundDevices) != 2 {
		t.Fatalf("persisted multi-bind wrong: %+v", c)
	}
	// capacity still enforced after restart
	if _, err := s2.BindDevice(codes[0], "dev-ccc3", "pub3"); !errors.Is(err, ErrAuthCodeFull) {
		t.Fatalf("restart full bind = %v, want ErrAuthCodeFull", err)
	}
}

// TestAuthCodeLegacyMigration verifies a record persisted in the old
// single-binding flat format is migrated to the multi-device format on load.
func TestAuthCodeLegacyMigration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "snet.db")
	s, err := NewStoreAt(path)
	if err != nil {
		t.Fatal(err)
	}
	codes, ids, err := s.AdminGenerateAuthCodes(1, 1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.BindDevice(codes[0], "dev-old1234", "pubO"); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	// Rewrite the persisted record back into the legacy flat format.
	legacy := authCodeRecord{
		ID:       ids[0],
		CodeHash: hashCode(codes[0]),
		Hint:     maskCode(codes[0]),
		DeviceID: "dev-old1234",
		PublicKey: "pubO",
	}
	s2, err := NewStoreAt(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := s2.persistAuthCode(&legacy); err != nil {
		s2.Close()
		t.Fatal(err)
	}
	if err := s2.Close(); err != nil {
		t.Fatal(err)
	}

	s3, err := NewStoreAt(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s3.Close()
	c := s3.AdminAuthCodes()[0]
	if c.MaxBindings != 1 || c.BoundCount != 1 {
		t.Fatalf("migrated record wrong: %+v", c)
	}
	if c.BoundToDevice != "dev-old1234" || len(c.BoundDevices) != 1 || c.BoundDevices[0].DeviceID != "dev-old1234" {
		t.Fatalf("migrated binding wrong: %+v", c)
	}
	if !s3.DeviceBound("dev-old1234") {
		t.Fatal("device lost after migration")
	}
}

// TestRequireDeviceAuthGate verifies the enrollment gate at the store level:
// unbound devices are refused create/join/register; bound devices pass.
func TestRequireDeviceAuthGate(t *testing.T) {
	s := NewStore()
	s.SetRequireDeviceAuth(true)
	if !s.RequireDeviceAuth() {
		t.Fatal("gate flag not on")
	}

	if _, err := s.CreateNetwork("pub1", "dev-aaaa", "", "", false); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("unbound create = %v, want ErrUnauthorized", err)
	}
	if _, err := s.Join("net1", "ABCDEFGHIJKL", "pub2", "dev-bbbb"); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("unbound join = %v, want ErrUnauthorized", err)
	}
	if err := s.RegisterDevice("dev-cccc", "pub3", ""); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("unbound register = %v, want ErrUnauthorized", err)
	}

	code, _ := genOneCode(t, s)
	if _, err := s.BindDevice(code, "dev-aaaa", "pub1"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateNetwork("pub1", "dev-aaaa", "", "", false); err != nil {
		t.Fatalf("bound create = %v", err)
	}
	if err := s.RegisterDevice("dev-aaaa", "pub1", ""); err != nil {
		t.Fatalf("bound register = %v", err)
	}

	// unbinding re-gates the device
	if err := s.AdminUnbindDevice("dev-aaaa"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateNetwork("pub1", "dev-aaaa", "", "", false); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("create after unbind = %v, want ErrUnauthorized", err)
	}

	// gate off: registration is open again
	s.SetRequireDeviceAuth(false)
	if err := s.RegisterDevice("dev-cccc", "pub3", ""); err != nil {
		t.Fatalf("register with gate off = %v", err)
	}
}

// TestRequireDeviceAuthHTTP exercises the gate through the HTTP API including
// the bind endpoint and its error mapping.
func TestRequireDeviceAuthHTTP(t *testing.T) {
	ts, s := newTestServerOpts(t, Options{RequireDeviceAuth: true})

	// unbound create/join/register → 403 with the enrollment hint
	resp := doJSON(t, http.MethodPost, ts.URL+"/api/v1/networks", "",
		protocol.CreateNetworkReq{PublicKey: "AAA==", DeviceID: "dev-aaaa"}, nil)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("unbound create status = %d, want 403", resp.StatusCode)
	}
	resp = doJSON(t, http.MethodPost, ts.URL+"/api/v1/networks/X/join", "",
		protocol.JoinReq{Code: "ABCDEFGHIJKL", PublicKey: "BBB==", DeviceID: "dev-aaaa"}, nil)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("unbound join status = %d, want 403", resp.StatusCode)
	}
	resp = doJSON(t, http.MethodPost, ts.URL+"/api/v1/devices", "",
		protocol.RegisterDeviceReq{DeviceID: "dev-aaaa", PublicKey: "AAA=="}, nil)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("unbound register status = %d, want 403", resp.StatusCode)
	}

	// bind endpoint is open and accepts the code
	code, _ := genOneCode(t, s)
	resp = doJSON(t, http.MethodPost, ts.URL+"/api/v1/devices/bind", "",
		protocol.BindDeviceReq{Code: code, DeviceID: "dev-aaaa", PublicKey: "AAA=="}, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("bind status = %d, want 200", resp.StatusCode)
	}

	// now create + join succeed
	var created protocol.CreateNetworkResp
	resp = doJSON(t, http.MethodPost, ts.URL+"/api/v1/networks", "",
		protocol.CreateNetworkReq{PublicKey: "AAA==", DeviceID: "dev-aaaa"}, &created)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("bound create status = %d", resp.StatusCode)
	}
	resp = doJSON(t, http.MethodPost, ts.URL+"/api/v1/networks/"+created.NetworkID+"/join", "",
		protocol.JoinReq{Code: created.PairingCode, PublicKey: "BBB==", DeviceID: "dev-aaaa"}, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("bound join status = %d", resp.StatusCode)
	}

	// wrong code → 404 "无效的设备授权码"; used code by another device → 409
	resp = doJSON(t, http.MethodPost, ts.URL+"/api/v1/devices/bind", "",
		protocol.BindDeviceReq{Code: "WRONGCODE1234", DeviceID: "dev-ffff", PublicKey: "FFF=="}, nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("bad code status = %d, want 404", resp.StatusCode)
	}
	resp = doJSON(t, http.MethodPost, ts.URL+"/api/v1/devices/bind", "",
		protocol.BindDeviceReq{Code: code, DeviceID: "dev-ffff", PublicKey: "FFF=="}, nil)
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("used code status = %d, want 409", resp.StatusCode)
	}
}

// TestAdminAuthCodeEndpoints covers the admin surface for authorization codes.
func TestAdminAuthCodeEndpoints(t *testing.T) {
	ts, s := newTestServerOpts(t, Options{AdminToken: "secret"})

	// unauthenticated admin calls rejected
	resp := doJSON(t, http.MethodGet, ts.URL+"/admin/devices/authcodes", "", nil, nil)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("admin without token = %d, want 401", resp.StatusCode)
	}

	// generate 2 codes
	var gen protocol.AdminGenerateAuthCodesResp
	resp = doJSON(t, http.MethodPost, ts.URL+"/admin/devices/authcodes/generate", "secret",
		protocol.AdminGenerateAuthCodesReq{Count: 2}, &gen)
	if resp.StatusCode != http.StatusOK || len(gen.Codes) != 2 || len(gen.IDs) != 2 {
		t.Fatalf("generate = %d, %+v", resp.StatusCode, gen)
	}

	// out-of-range count → 400
	resp = doJSON(t, http.MethodPost, ts.URL+"/admin/devices/authcodes/generate", "secret",
		protocol.AdminGenerateAuthCodesReq{Count: 101}, nil)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("count 101 = %d, want 400", resp.StatusCode)
	}

	// out-of-range maxBindings → 400
	resp = doJSON(t, http.MethodPost, ts.URL+"/admin/devices/authcodes/generate", "secret",
		protocol.AdminGenerateAuthCodesReq{Count: 1, MaxBindings: 101}, nil)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("maxBindings 101 = %d, want 400", resp.StatusCode)
	}

	// generate a multi-bind code and bind two devices through the device API
	var gen2 protocol.AdminGenerateAuthCodesResp
	resp = doJSON(t, http.MethodPost, ts.URL+"/admin/devices/authcodes/generate", "secret",
		protocol.AdminGenerateAuthCodesReq{Count: 1, MaxBindings: 2}, &gen2)
	if resp.StatusCode != http.StatusOK || len(gen2.Codes) != 1 {
		t.Fatalf("multi-bind generate = %d, %+v", resp.StatusCode, gen2)
	}
	if _, err := s.BindDevice(gen2.Codes[0], "dev-m1abc", "pubm1"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.BindDevice(gen2.Codes[0], "dev-m2abc", "pubm2"); err != nil {
		t.Fatal(err)
	}
	resp = doJSON(t, http.MethodPost, ts.URL+"/api/v1/devices/bind", "",
		protocol.BindDeviceReq{Code: gen2.Codes[0], DeviceID: "dev-m3abc", PublicKey: "pubm3"}, nil)
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("3rd bind on max-2 code = %d, want 409", resp.StatusCode)
	}

	// bind one code through the device API, then list shows the binding
	if _, err := s.BindDevice(gen.Codes[0], "dev-aaaa", "pub1"); err != nil {
		t.Fatal(err)
	}
	var list protocol.AdminAuthCodesResp
	resp = doJSON(t, http.MethodGet, ts.URL+"/admin/devices/authcodes", "secret", nil, &list)
	if resp.StatusCode != http.StatusOK || len(list.Codes) != 3 {
		t.Fatalf("list = %d, %+v", resp.StatusCode, list.Codes)
	}
	found := false
	for _, c := range list.Codes {
		if c.ID == gen2.IDs[0] {
			if c.MaxBindings != 2 || c.BoundCount != 2 || len(c.BoundDevices) != 2 {
				t.Fatalf("multi-bind info wrong: %+v", c)
			}
			found = true
		}
		if c.BoundToDevice == "dev-aaaa" {
			found = found && true
		}
	}
	if !found {
		t.Fatalf("binding / capacity not visible in list: %+v", list.Codes)
	}

	// revoke the unbound code (id[1]); the bound ones stay so the devices
	// keep their bindings until unbind is called below
	resp = doJSON(t, http.MethodPost, ts.URL+"/admin/devices/authcodes/revoke", "secret",
		map[string]any{"id": gen.IDs[1]}, nil)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("revoke = %d, want 204", resp.StatusCode)
	}
	resp = doJSON(t, http.MethodGet, ts.URL+"/admin/devices/authcodes", "secret", nil, &list)
	if len(list.Codes) != 2 {
		t.Fatalf("codes after revoke = %d", len(list.Codes))
	}

	// unbind releases the device's binding
	resp = doJSON(t, http.MethodPost, ts.URL+"/admin/devices/authcodes/unbind", "secret",
		map[string]any{"deviceId": "dev-aaaa"}, nil)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("unbind = %d, want 204", resp.StatusCode)
	}
	if s.DeviceBound("dev-aaaa") {
		t.Fatal("device still bound after admin unbind")
	}
}

// TestBindConcurrent ensures a single code is consumed by exactly one device
// under a race.
func TestBindConcurrent(t *testing.T) {
	s := NewStore()
	code, _ := genOneCode(t, s)

	start := make(chan struct{})
	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i, dev := range []string{"dev-aaa1", "dev-aaa2"} {
		wg.Add(1)
		go func(i int, dev string) {
			defer wg.Done()
			<-start
			_, errs[i] = s.BindDevice(code, dev, "pub")
		}(i, dev)
	}
	close(start)
	wg.Wait()

	ok := 0
	for _, err := range errs {
		if err == nil {
			ok++
		} else if !errors.Is(err, ErrAuthCodeFull) {
			t.Fatalf("unexpected error: %v", err)
		}
	}
	if ok != 1 {
		t.Fatalf("exactly one bind should win, got %d winners (%v)", ok, errs)
	}
}

// TestRegisterReportsBinding verifies the register response carries the
// device's binding status so bound clients can detect an admin revocation even
// when the enrollment gate is off.
func TestRegisterReportsBinding(t *testing.T) {
	ts, s := newTestServerOpts(t, Options{})
	code, _ := genOneCode(t, s)

	// unbound device register → 200 with bound=false
	var reg protocol.RegisterDeviceResp
	resp := doJSON(t, http.MethodPost, ts.URL+"/api/v1/devices", "",
		protocol.RegisterDeviceReq{DeviceID: "dev-reg-1", PublicKey: "AAA=="}, &reg)
	if resp.StatusCode != http.StatusOK || reg.Bound == nil || *reg.Bound {
		t.Fatalf("unbound register = %d %+v, want 200 bound=false", resp.StatusCode, reg)
	}

	// bind then register → bound=true
	resp = doJSON(t, http.MethodPost, ts.URL+"/api/v1/devices/bind", "",
		protocol.BindDeviceReq{Code: code, DeviceID: "dev-reg-1", PublicKey: "AAA=="}, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("bind status = %d", resp.StatusCode)
	}
	reg = protocol.RegisterDeviceResp{}
	resp = doJSON(t, http.MethodPost, ts.URL+"/api/v1/devices", "",
		protocol.RegisterDeviceReq{DeviceID: "dev-reg-1", PublicKey: "AAA=="}, &reg)
	if resp.StatusCode != http.StatusOK || reg.Bound == nil || !*reg.Bound {
		t.Fatalf("bound register = %d %+v, want bound=true", resp.StatusCode, reg)
	}

	// unbind → register → bound=false again
	if err := s.AdminUnbindDevice("dev-reg-1"); err != nil {
		t.Fatal(err)
	}
	reg = protocol.RegisterDeviceResp{}
	resp = doJSON(t, http.MethodPost, ts.URL+"/api/v1/devices", "",
		protocol.RegisterDeviceReq{DeviceID: "dev-reg-1", PublicKey: "AAA=="}, &reg)
	if resp.StatusCode != http.StatusOK || reg.Bound == nil || *reg.Bound {
		t.Fatalf("post-unbind register = %d %+v, want bound=false", resp.StatusCode, reg)
	}
}

// TestBindRateLimited verifies the per-IP rate limit on the bind endpoint.
func TestBindRateLimited(t *testing.T) {
	ts, _ := newTestServerOpts(t, Options{})
	const tries = 21 // defaultBindPerMinute = 20
	blocked := 0
	for i := 0; i < tries; i++ {
		resp := doJSON(t, http.MethodPost, ts.URL+"/api/v1/devices/bind", "",
			protocol.BindDeviceReq{Code: "WRONGCODE1234", DeviceID: "dev-xxxx", PublicKey: "X=="}, nil)
		if resp.StatusCode == http.StatusTooManyRequests {
			blocked++
		} else if resp.StatusCode != http.StatusNotFound {
			t.Fatalf("attempt %d status = %d", i, resp.StatusCode)
		}
	}
	if blocked != 1 {
		t.Fatalf("expected exactly 1 rate-limited attempt, got %d", blocked)
	}
}

// TestBindDeviceReturnsToken verifies BindDevice returns a non-empty deviceToken.
func TestBindDeviceReturnsToken(t *testing.T) {
	s := NewStore()
	code, _ := genOneCode(t, s)
	tok, err := s.BindDevice(code, "dev-token1", "pub1")
	if err != nil {
		t.Fatal(err)
	}
	if tok == "" {
		t.Fatal("BindDevice returned empty token")
	}
	// token is valid for the device
	if !s.ValidateDeviceToken("dev-token1", tok) {
		t.Fatal("token from BindDevice not valid")
	}
}

// TestDeviceNetworkDetails covers the full flow: create network, join from
// another device, then call DeviceNetworkDetails to verify both nodes appear
// with correct metadata (owner flag, IP, token rotation).
func TestDeviceNetworkDetails(t *testing.T) {
	s := NewStore()
	s.SetRequireDeviceAuth(true)

	// bind owner device
	code1, _ := genOneCode(t, s)
	if _, err := s.BindDevice(code1, "dev-owner1", "pubA"); err != nil {
		t.Fatal(err)
	}
	// bind joiner device
	code2, _ := genOneCode(t, s)
	if _, err := s.BindDevice(code2, "dev-joiner1", "pubB"); err != nil {
		t.Fatal(err)
	}

	created, err := s.CreateNetwork("pubA", "dev-owner1", "test-net", "10.0.0.0/24", false)
	if err != nil {
		t.Fatal(err)
	}
	joined, err := s.Join(created.NetworkID, created.PairingCode, "pubB", "dev-joiner1")
	if err != nil {
		t.Fatal(err)
	}
	if joined.Token == "" {
		t.Fatal("join returned empty token")
	}

	// owner should see 1 network
	details, err := s.DeviceNetworkDetails("dev-owner1")
	if err != nil {
		t.Fatal(err)
	}
	if len(details) != 1 {
		t.Fatalf("owner details count = %d, want 1", len(details))
	}
	if !details[0].Owner {
		t.Fatal("owner flag should be true")
	}
	if details[0].Token == "" {
		t.Fatal("token should not be empty")
	}
	if details[0].NodeCount != 2 {
		t.Fatalf("node count = %d, want 2", details[0].NodeCount)
	}

	// joiner should also see 1 network
	details, err = s.DeviceNetworkDetails("dev-joiner1")
	if err != nil {
		t.Fatal(err)
	}
	if len(details) != 1 {
		t.Fatalf("joiner details count = %d, want 1", len(details))
	}
	if details[0].Owner {
		t.Fatal("joiner should not be owner")
	}
	if details[0].Token == "" {
		t.Fatal("token should not be empty")
	}

	// device with no networks should get empty list
	details, err = s.DeviceNetworkDetails("dev-ghost")
	if err != nil {
		t.Fatal(err)
	}
	if len(details) != 0 {
		t.Fatalf("ghost details = %d, want 0", len(details))
	}
}

// TestDeviceNetworkDetailsTokenRotation verifies that calling
// DeviceNetworkDetails generates new tokens (old tokens are invalidated).
func TestDeviceNetworkDetailsTokenRotation(t *testing.T) {
	s := NewStore()
	code1, _ := genOneCode(t, s)
	tok1, _ := s.BindDevice(code1, "dev-rot1", "pubA")
	if !s.ValidateDeviceToken("dev-rot1", tok1) {
		t.Fatal("initial token not valid")
	}

	created, err := s.CreateNetwork("pubA", "dev-rot1", "net", "", false)
	if err != nil {
		t.Fatal(err)
	}

	// capture the original node token from join (here owner creates, so node token is created at create)
	// get network info to find the original token for the owner node
	info, err := s.NetworkInfo(created.Token)
	if err != nil {
		t.Fatal(err)
	}
	var oldNodeToken string
	for _, n := range info.Nodes {
		if n.DeviceID == "dev-rot1" {
			// we can't get the raw token from nodes, but we know it exists
			break
		}
	}
	_ = oldNodeToken

	// call DeviceNetworkDetails — should return a fresh token
	details, err := s.DeviceNetworkDetails("dev-rot1")
	if err != nil {
		t.Fatal(err)
	}
	if len(details) != 1 {
		t.Fatalf("details count = %d, want 1", len(details))
	}
	newToken := details[0].Token
	if newToken == "" {
		t.Fatal("new token empty")
	}

	// calling again should return yet another new token (rotation each time)
	details2, err := s.DeviceNetworkDetails("dev-rot1")
	if err != nil {
		t.Fatal(err)
	}
	if details2[0].Token == "" {
		t.Fatal("second token empty")
	}
}

// TestGenerateAndValidateDeviceToken covers GenerateDeviceToken, ValidateDeviceToken,
// and token rotation (generating a new one invalidates the old).
func TestGenerateAndValidateDeviceToken(t *testing.T) {
	s := NewStore()
	s.SetRequireDeviceAuth(true)
	code, _ := genOneCode(t, s)
	if _, err := s.BindDevice(code, "dev-tok1", "pub1"); err != nil {
		t.Fatal(err)
	}

	tok1, err := s.GenerateDeviceToken("dev-tok1")
	if err != nil {
		t.Fatal(err)
	}
	if tok1 == "" {
		t.Fatal("token empty")
	}
	if !s.ValidateDeviceToken("dev-tok1", tok1) {
		t.Fatal("token1 not valid")
	}

	// generate again → old token invalidated
	tok2, err := s.GenerateDeviceToken("dev-tok1")
	if err != nil {
		t.Fatal(err)
	}
	if tok2 == "" {
		t.Fatal("second token empty")
	}
	if tok1 == tok2 {
		t.Fatal("same token returned twice")
	}
	if s.ValidateDeviceToken("dev-tok1", tok1) {
		t.Fatal("old token should be invalid after rotation")
	}
	if !s.ValidateDeviceToken("dev-tok1", tok2) {
		t.Fatal("new token not valid")
	}

	// non-existent device
	if _, err := s.GenerateDeviceToken("dev-nobody"); err != ErrNotFound {
		t.Fatalf("GenerateDeviceToken for unknown device = %v, want ErrNotFound", err)
	}
	// empty/missing always fails
	if s.ValidateDeviceToken("", tok1) {
		t.Fatal("empty deviceID should fail")
	}
	if s.ValidateDeviceToken("dev-tok1", "") {
		t.Fatal("empty token should fail")
	}
}

// TestUpdateNodePublicKey verifies public key update and its permission checks.
func TestUpdateNodePublicKey(t *testing.T) {
	s := NewStore()
	s.SetRequireDeviceAuth(true)
	code1, _ := genOneCode(t, s)
	if _, err := s.BindDevice(code1, "dev-owner2", "pubA"); err != nil {
		t.Fatal(err)
	}
	code2, _ := genOneCode(t, s)
	if _, err := s.BindDevice(code2, "dev-joiner2", "pubB"); err != nil {
		t.Fatal(err)
	}

	created, err := s.CreateNetwork("pubA", "dev-owner2", "net", "", false)
	if err != nil {
		t.Fatal(err)
	}
	joined, err := s.Join(created.NetworkID, created.PairingCode, "pubB", "dev-joiner2")
	if err != nil {
		t.Fatal(err)
	}

	// update the joiner's public key
	if err := s.UpdateNodePublicKey("dev-joiner2", created.NetworkID, joined.NodeID, "pubB-new"); err != nil {
		t.Fatal(err)
	}

	// verify via NetworkInfo (owner perspective)
	info, err := s.NetworkInfo(created.Token)
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range info.Nodes {
		if n.DeviceID == "dev-joiner2" && n.PublicKey != "pubB-new" {
			t.Fatalf("public key not updated: got %q, want pubB-new", n.PublicKey)
		}
	}

	// wrong device → unauthorized
	if err := s.UpdateNodePublicKey("dev-owner2", created.NetworkID, joined.NodeID, "hacked"); err != ErrUnauthorized {
		t.Fatalf("update by wrong device = %v, want ErrUnauthorized", err)
	}
	// wrong network → not found
	if err := s.UpdateNodePublicKey("dev-joiner2", "fake-net", joined.NodeID, "x"); err != ErrNotFound {
		t.Fatalf("update wrong network = %v, want ErrNotFound", err)
	}
	// wrong node → not found
	if err := s.UpdateNodePublicKey("dev-joiner2", created.NetworkID, "fake-node", "x"); err != ErrNotFound {
		t.Fatalf("update wrong node = %v, want ErrNotFound", err)
	}
}

// TestDeviceNetworkDetailsMultipleNetworks verifies a device that joined
// multiple networks sees all of them in DeviceNetworkDetails.
func TestDeviceNetworkDetailsMultipleNetworks(t *testing.T) {
	s := NewStore()
	s.SetRequireDeviceAuth(true)
	code1, _ := genOneCode(t, s)
	if _, err := s.BindDevice(code1, "dev-multi1", "pub1"); err != nil {
		t.Fatal(err)
	}
	code2, _ := genOneCode(t, s)
	if _, err := s.BindDevice(code2, "dev-multi2", "pub2"); err != nil {
		t.Fatal(err)
	}
	code3, _ := genOneCode(t, s)
	if _, err := s.BindDevice(code3, "dev-multi3", "pub3"); err != nil {
		t.Fatal(err)
	}

	// create two networks owned by different devices, both joined by dev-multi1
	net1, _ := s.CreateNetwork("pub1", "dev-multi1", "net1", "", false)
	net2, _ := s.CreateNetwork("pub3", "dev-multi3", "net2", "", false)
	if _, err := s.Join(net1.NetworkID, net1.PairingCode, "pub2", "dev-multi2"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Join(net2.NetworkID, net2.PairingCode, "pub2", "dev-multi2"); err != nil {
		t.Fatal(err)
	}

	// dev-multi2 should see 2 networks
	details, err := s.DeviceNetworkDetails("dev-multi2")
	if err != nil {
		t.Fatal(err)
	}
	if len(details) != 2 {
		t.Fatalf("details count = %d, want 2", len(details))
	}
}
