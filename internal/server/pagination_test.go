package server

import (
	"fmt"
	"net/http"
	"testing"

	"snet/internal/protocol"
)

// TestAdminPagination seeds 25 networks and verifies page windows, ordering,
// search, status filtering, out-of-range clamping and the /admin/stats rollup.
func TestAdminPagination(t *testing.T) {
	ts, s := newTestServerOpts(t, Options{AdminToken: "secret"})
	const total = 25

	// one user network gets a pending join to exercise the status filter;
	// it is created first so the named admin networks sort above it
	var first protocol.AdminCreateNetworkResp
	doJSON(t, http.MethodPost, ts.URL+"/api/v1/networks", "",
		map[string]any{"publicKey": "PUB==", "approvalRequired": true}, &first)
	doJSON(t, http.MethodPost, ts.URL+"/api/v1/networks/"+first.NetworkID+"/join", "",
		map[string]any{"code": first.PairingCode, "publicKey": "JOIN=="}, nil)

	for i := 0; i < total; i++ {
		var created protocol.AdminCreateNetworkResp
		resp := doJSON(t, http.MethodPost, ts.URL+"/admin/networks", "secret",
			map[string]any{"name": fmt.Sprintf("net-%02d", i)}, &created)
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("create %d = %d", i, resp.StatusCode)
		}
	}

	type pageResp struct {
		Items    []networkSummary `json:"items"`
		Total    int              `json:"total"`
		Page     int              `json:"page"`
		PageSize int              `json:"pageSize"`
	}
	get := func(path string) pageResp {
		t.Helper()
		var pr pageResp
		resp := doJSON(t, http.MethodGet, ts.URL+path, "secret", nil, &pr)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("GET %s = %d", path, resp.StatusCode)
		}
		return pr
	}

	// default window: 20 items on page 1, total preserved
	p1 := get("/admin/networks")
	if len(p1.Items) != 20 || p1.Total != total+1 || p1.Page != 1 || p1.PageSize != 20 {
		t.Fatalf("page1: len=%d total=%d page=%d size=%d", len(p1.Items), p1.Total, p1.Page, p1.PageSize)
	}

	// page 2 carries the remainder
	p2 := get("/admin/networks?page=2")
	if len(p2.Items) != total+1-20 {
		t.Fatalf("page2: len=%d want %d", len(p2.Items), total+1-20)
	}

	// no overlap between pages
	ids := map[string]bool{}
	for _, n := range p1.Items {
		ids[n.Network.ID] = true
	}
	for _, n := range p2.Items {
		if ids[n.Network.ID] {
			t.Fatal("network appears on both pages")
		}
	}

	// newest first (createdAt desc): last created network leads page 1
	var last protocol.AdminCreateNetworkResp
	_ = last
	if p1.Items[0].Network.Name != fmt.Sprintf("net-%02d", total-1) {
		t.Fatalf("ordering wrong: first item = %s", p1.Items[0].Network.Name)
	}

	// search by name substring
	sr := get("/admin/networks?q=net-00")
	if sr.Total != 1 || len(sr.Items) != 1 || sr.Items[0].Network.Name != "net-00" {
		t.Fatalf("search: %+v", sr)
	}

	// pending filter matches only the joined network
	pr := get("/admin/networks?status=pending")
	if pr.Total != 1 || len(pr.Items) != 1 || pr.Items[0].PendingCount != 1 {
		t.Fatalf("pending filter: %+v", pr)
	}

	// unknown status value matches nothing
	nf := get("/admin/networks?status=bogus")
	if nf.Total != 0 || len(nf.Items) != 0 {
		t.Fatalf("bogus filter: %+v", nf)
	}

	// out-of-range page clamps back onto the last non-empty page
	clamped := get("/admin/networks?page=99")
	wantPage := (total + 1 + 19) / 20
	if clamped.Page != wantPage || len(clamped.Items) == 0 {
		t.Fatalf("clamp: page=%d want=%d len=%d", clamped.Page, wantPage, len(clamped.Items))
	}

	// oversized page_size is capped at 100; zero falls back to the default
	big := get("/admin/networks?page_size=500")
	if big.PageSize != 100 {
		t.Fatalf("cap: pageSize=%d", big.PageSize)
	}
	zero := get("/admin/networks?page_size=0")
	if zero.PageSize != 20 {
		t.Fatalf("default: pageSize=%d", zero.PageSize)
	}

	// stats endpoint aggregates totals
	var ov struct {
		AdminOverview
	}
	doJSON(t, http.MethodGet, ts.URL+"/admin/stats", "secret", nil, &ov)
	if ov.NetworksTotal != total+1 || ov.PendingTotal != 1 || ov.NodesTotal != 1 {
		t.Fatalf("stats: %+v", ov.AdminOverview)
	}
	if len(ov.RecentNetworks) == 0 || len(ov.RecentNetworks) > 6 {
		t.Fatalf("recent: %d entries", len(ov.RecentNetworks))
	}

	// auth codes are paginated too
	var gen protocol.AdminGenerateAuthCodesResp
	doJSON(t, http.MethodPost, ts.URL+"/admin/devices/authcodes/generate", "secret",
		protocol.AdminGenerateAuthCodesReq{Count: 23, MaxBindings: 1}, &gen)
	var codePage struct {
		Items    []protocol.AuthCodeInfo `json:"items"`
		Total    int                     `json:"total"`
		Page     int                     `json:"page"`
		PageSize int                     `json:"pageSize"`
	}
	doJSON(t, http.MethodGet, ts.URL+"/admin/devices/authcodes?page=2", "secret", nil, &codePage)
	if len(codePage.Items) != 3 || codePage.Total != 23 || codePage.Page != 2 {
		t.Fatalf("codes page2: len=%d total=%d page=%d", len(codePage.Items), codePage.Total, codePage.Page)
	}
	if s == nil {
		t.Fatal("store missing")
	}
}
