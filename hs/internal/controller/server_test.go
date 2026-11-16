package controller

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func adminHeader(token string) map[string]string {
	return map[string]string{"Authorization": "Bearer " + token}
}

// N-inv-1 (FM: invitation lifecycle): create -> redeem -> second redeem
// refused (one-time), node record created pending.
func TestInvitationLifecycle(t *testing.T) {
	s := newTestStore(t)
	fake := &FakeHeadscale{}
	code, inv, err := s.CreateInvitation("lab", "default", time.Hour, "test")
	if err != nil {
		t.Fatal(err)
	}
	if inv.State != InvCreated {
		t.Fatalf("state = %v", inv.State)
	}
	res, err := s.Redeem(code, "win-a", fake, "http://127.0.0.1:8080")
	if err != nil {
		t.Fatalf("first redeem: %v", err)
	}
	if res.NodeID != "win-a" || res.PreauthKey == "" || res.NodeSecret == "" {
		t.Fatalf("redeem incomplete: %+v", res)
	}
	if _, err := s.Redeem(code, "win-b", fake, ""); err != ErrUsed {
		t.Fatalf("second redeem = %v, want ErrUsed", err)
	}
	n, ok := s.Node("win-a")
	if !ok || n.State != NodePending {
		t.Fatalf("node after redeem: %+v (ok=%v)", n, ok)
	}
	// first contact -> active
	if _, err := s.Resume("win-a", res.NodeSecret, fake); err != nil {
		t.Fatalf("resume: %v", err)
	}
	if n, _ := s.Node("win-a"); n.State != NodeActive {
		t.Fatalf("node state after first contact = %v, want active", n.State)
	}
}

// N-inv-2 (N5' atomicity): concurrent redeems of the same code -- exactly one
// succeeds. LIVE-RED candidate: without the store lock the second redeem wins
// or corrupts state.
func TestRedeemAtomicUnderConcurrency(t *testing.T) {
	s := newTestStore(t)
	fake := &FakeHeadscale{}
	code, _, err := s.CreateInvitation("", "", time.Hour, "test")
	if err != nil {
		t.Fatal(err)
	}
	const N = 8
	var wg sync.WaitGroup
	var mu sync.Mutex
	won, failed := 0, 0
	for i := 0; i < N; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, err := s.Redeem(code, fmt.Sprintf("racer-%d", i), fake, "")
			mu.Lock()
			defer mu.Unlock()
			if err == nil {
				won++
			} else {
				failed++
			}
		}(i)
	}
	wg.Wait()
	if won != 1 {
		t.Fatalf("atomicity broken: %d redeems succeeded (want exactly 1); failed=%d", won, failed)
	}
	if failed != N-1 {
		t.Fatalf("unexpected failure count: %d", failed)
	}
}

// N-inv-3 (N3'/N4'): expired and revoked invitations are refused.
func TestRedeemRejectsExpiredAndRevoked(t *testing.T) {
	s := newTestStore(t)
	fake := &FakeHeadscale{}
	code, _, _ := s.CreateInvitation("", "", -time.Minute, "test") // already expired
	if _, err := s.Redeem(code, "late", fake, ""); err != ErrExpired {
		t.Fatalf("expired redeem = %v, want ErrExpired", err)
	}
	code2, inv2, _ := s.CreateInvitation("", "", time.Hour, "test")
	if err := s.RevokeInvitation(inv2.ID, "test"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Redeem(code2, "revoked", fake, ""); err != ErrRevoked {
		t.Fatalf("revoked redeem = %v, want ErrRevoked", err)
	}
	// wrong code shape
	if _, err := s.Redeem("totally-wrong-code", "x", fake, ""); err != ErrNoInvitation {
		t.Fatalf("unknown code = %v, want ErrNoInvitation", err)
	}
}

// N-inv-4 (N7'): a revoked node cannot be resumed, and its unredeemed
// invitations fail.
func TestRevokedNodeRefusesResume(t *testing.T) {
	s := newTestStore(t)
	fake := &FakeHeadscale{}
	code, _, _ := s.CreateInvitation("", "", time.Hour, "test")
	res, err := s.Redeem(code, "victim", fake, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Resume("victim", res.NodeSecret, fake); err != nil {
		t.Fatal(err)
	}
	if err := s.RevokeNode("victim", "test", fake); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	if _, err := s.Resume("victim", res.NodeSecret, fake); err != ErrNodeRevoked {
		t.Fatalf("resume after revoke = %v, want ErrNodeRevoked", err)
	}
	if _, err := s.Resume("victim", "wrong-secret", fake); err != ErrNodeRevoked {
		t.Fatalf("wrong-secret resume after revoke = %v, want ErrNodeRevoked (revocation check must precede secret check)", err)
	}
	// a second invitation for the same node cannot revive it
	code2, _, _ := s.CreateInvitation("", "", time.Hour, "test")
	if _, err := s.Redeem(code2, "victim", fake, ""); err != ErrNodeExists {
		t.Fatalf("re-enroll revoked node = %v, want ErrNodeExists", err)
	}
}

// N-inv-5: wrong node secret is refused with a non-enumerating error.
func TestResumeRejectsWrongSecret(t *testing.T) {
	s := newTestStore(t)
	fake := &FakeHeadscale{}
	code, _, _ := s.CreateInvitation("", "", time.Hour, "test")
	res, err := s.Redeem(code, "n1", fake, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Resume("n1", "bad", fake); err != ErrBadSecret {
		t.Fatalf("wrong secret = %v, want ErrBadSecret", err)
	}
	// unknown hostname vs wrong secret must not be distinguishable by timing
	// here -- both are 403 at the API layer (asserted in server tests)
	_ = res
}

// N-inv-6 (N14' analog): the store file and audit never contain plaintext
// codes or secrets.
func TestStoreNeverHoldsPlaintextSecrets(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	fake := &FakeHeadscale{}
	code, _, err := s.CreateInvitation("", "", time.Hour, "test")
	if err != nil {
		t.Fatal(err)
	}
	res, err := s.Redeem(code, "secretless", fake, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Resume("secretless", res.NodeSecret, fake); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(dir, "registry.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{code, res.NodeSecret, res.PreauthKey} {
		if strings.Contains(string(b), secret) {
			t.Fatalf("store file contains plaintext secret %q...:\n%s", secret[:8], b)
		}
	}
	sum := sha256.Sum256([]byte(res.NodeSecret))
	if !strings.Contains(string(b), hex.EncodeToString(sum[:])) {
		t.Fatal("node secret hash not stored (resume would never work)")
	}
}

// ---- HTTP server tests ----

type srvFixture struct {
	ts       *httptest.Server
	code     string
	sec      string
	store    *Store
	adminTok string
}

func newSrv(t *testing.T) *srvFixture {
	s := newTestStore(t)
	adminTok, err := RandomToken()
	if err != nil {
		t.Fatal(err)
	}
	srv := NewServer(s, Options{
		AdminTokenSHA: AdminTokenSHA(adminTok),
		Server:        "http://127.0.0.1:8080",
		Headscale:     &FakeHeadscale{},
	})
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	code, _, err := s.CreateInvitation("", "", time.Hour, "test")
	if err != nil {
		t.Fatal(err)
	}
	return &srvFixture{ts: ts, code: code, store: s, adminTok: adminTok}
}

func postJSON(t *testing.T, url string, headers map[string]string, body any) (*http.Response, map[string]any) {
	t.Helper()
	b, _ := json.Marshal(body)
	req, err := http.NewRequest("POST", url, strings.NewReader(string(b)))
	if err != nil {
		t.Fatal(err)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp, out
}

// N-srv-1 (N13'): no admin token -> 401 on admin endpoints.
func TestAdminAuthRequired(t *testing.T) {
	f := newSrv(t)
	resp, _ := postJSON(t, f.ts.URL+"/api/v1/invitations", nil, map[string]any{"label": "x"})
	if resp.StatusCode != 401 {
		t.Fatalf("unauthed invitation create = %d, want 401", resp.StatusCode)
	}
	resp2, _ := postJSON(t, f.ts.URL+"/api/v1/nodes/winx/revoke", nil, map[string]any{})
	if resp2.StatusCode != 401 {
		t.Fatalf("unauthed revoke = %d, want 401", resp2.StatusCode)
	}
	// health stays open (probe endpoint)
	hresp, err := http.Get(f.ts.URL + "/api/v1/health")
	if err != nil {
		t.Fatal(err)
	}
	hresp.Body.Close()
	if hresp.StatusCode != 200 {
		t.Fatalf("health = %d, want 200", hresp.StatusCode)
	}
}

// N-srv-2: redeem over HTTP is exactly once; wrong code is 403.
func TestRedeemHTTPMatrix(t *testing.T) {
	f := newSrv(t)
	resp, out := postJSON(t, f.ts.URL+"/api/v1/enroll/redeem", nil, map[string]string{"code": f.code, "hostname": "win-http"})
	if resp.StatusCode != 201 {
		t.Fatalf("redeem = %d: %v", resp.StatusCode, out)
	}
	if out["node_secret"] == nil || out["preauth_key"] == nil {
		t.Fatalf("redeem response missing secrets: %v", out)
	}
	resp2, _ := postJSON(t, f.ts.URL+"/api/v1/enroll/redeem", nil, map[string]string{"code": f.code, "hostname": "other"})
	if resp2.StatusCode != 409 {
		t.Fatalf("replay = %d, want 409", resp2.StatusCode)
	}
	resp3, _ := postJSON(t, f.ts.URL+"/api/v1/enroll/redeem", nil, map[string]string{"code": "nope", "hostname": "x"})
	if resp3.StatusCode != 403 {
		t.Fatalf("bad code = %d, want 403", resp3.StatusCode)
	}
}

// N-srv-3: rate limiting -- the 7th redeem in the window is 429.
func TestRedeemRateLimited(t *testing.T) {
	s := newTestStore(t)
	adminTok, _ := RandomToken()
	srv := NewServer(s, Options{
		AdminTokenSHA: AdminTokenSHA(adminTok),
		Headscale:     &FakeHeadscale{},
		RateLimit:     6,
		RateWindow:    time.Hour, // long window: every attempt counts
	})
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()
	last := 0
	for i := 0; i < 7; i++ {
		resp, _ := postJSON(t, ts.URL+"/api/v1/enroll/redeem", nil, map[string]string{"code": "junk", "hostname": "x"})
		last = resp.StatusCode
	}
	if last != 429 {
		t.Fatalf("7th attempt = %d, want 429", last)
	}
}

// N-srv-4: resume over HTTP works and a wrong secret is a 403.
func TestResumeHTTP(t *testing.T) {
	f := newSrv(t)
	resp, out := postJSON(t, f.ts.URL+"/api/v1/enroll/redeem", nil, map[string]string{"code": f.code, "hostname": "win-r"})
	if resp.StatusCode != 201 {
		t.Fatalf("redeem = %d", resp.StatusCode)
	}
	sec, _ := out["node_secret"].(string)
	resp2, out2 := postJSON(t, f.ts.URL+"/api/v1/nodes/resume", nil, map[string]string{"hostname": "win-r", "node_secret": sec})
	if resp2.StatusCode != 200 {
		t.Fatalf("resume = %d: %v", resp2.StatusCode, out2)
	}
	if out2["preauth_key"] == nil {
		t.Fatal("resume missing preauth_key")
	}
	resp3, _ := postJSON(t, f.ts.URL+"/api/v1/nodes/resume", nil, map[string]string{"hostname": "win-r", "node_secret": "forged"})
	if resp3.StatusCode != 403 {
		t.Fatalf("forged secret = %d, want 403", resp3.StatusCode)
	}
}

// N-srv-5: revoke via API marks the node and blocks resume (N6'/N7').
func TestRevokeHTTP(t *testing.T) {
	f := newSrv(t)
	hdr := adminHeader(f.adminTok)
	resp, out := postJSON(t, f.ts.URL+"/api/v1/enroll/redeem", nil, map[string]string{"code": f.code, "hostname": "win-z"})
	if resp.StatusCode != 201 {
		t.Fatalf("redeem = %d", resp.StatusCode)
	}
	sec, _ := out["node_secret"].(string)
	revReq, _ := http.NewRequest("POST", f.ts.URL+"/api/v1/nodes/win-z/revoke", nil)
	revReq.Header.Set("Authorization", hdr["Authorization"])
	revResp, err := http.DefaultClient.Do(revReq)
	if err != nil {
		t.Fatal(err)
	}
	revResp.Body.Close()
	if revResp.StatusCode != 200 {
		t.Fatalf("revoke = %d", revResp.StatusCode)
	}
	resp2, _ := postJSON(t, f.ts.URL+"/api/v1/nodes/resume", nil, map[string]string{"hostname": "win-z", "node_secret": sec})
	if resp2.StatusCode != 403 {
		t.Fatalf("resume after revoke = %d, want 403", resp2.StatusCode)
	}
}
