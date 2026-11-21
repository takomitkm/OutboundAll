// server.go -- the hs-controller HTTP API.
//
// Auth model (SECURITY_MODEL §3.7, N13'): everything except redeem/resume and
// health requires the admin bearer token. redeem/resume are rate limited per
// IP (fixed window). No endpoint leaks plaintext secrets; audit carries ids
// only.
package controller

import (
	"crypto/subtle"
	"encoding/json"
	"log"
	"net"

	"github.com/takomitkm/outboundall/hs/internal/authz"
	"net/http"
	"strings"
	"sync"
	"time"
)

// PolicyReloader applies a freshly rendered policy (write file + reload
// headscale). Implementation lives on the VPS: write the file headscale
// serves from, then signal headscale to re-read it.
type PolicyReloader func(policyJSON string) error

// Options configures the API server.
type Options struct {
	// hex sha256 of the admin token
	AdminTokenSHA string
	// control plane URL handed to nodes
	Server    string
	Headscale HeadscaleAPI
	// policy reload hook (nil = policy management disabled)
	ReloadPolicy PolicyReloader
	// headscale user that owns the OutboundAll tags (tagOwners)
	PolicyOwner string
	// redeem attempts per window per IP
	RateLimit  int
	RateWindow time.Duration
}

// Server is the hs-controller API.
type Server struct {
	store *Store
	opt   Options
	mu    sync.Mutex
	hits  map[string]*rateBucket
}

// syncPolicy renders the current authorization state and hands it to the
// reloader. Best effort on node-facing flows (registration is never blocked
// by policy I/O) but NEVER silent: a failure here leaves headscale enforcing
// a stale policy, which is a security-relevant divergence, so every failure
// is logged. Admin mutations additionally surface it in the response.
var syncPolicyLog = func(format string, args ...any) {
	log.Printf("policy sync FAILED: "+format, args...)
}

func (s *Server) syncPolicy() error {
	if s.opt.ReloadPolicy == nil {
		return nil
	}
	nodes, _ := s.store.Snapshot()
	views := make([]authz.NodeView, 0, len(nodes))
	for _, n := range nodes {
		views = append(views, authz.NodeView{Hostname: n.Hostname, SSH: n.SSHEnabled()})
	}
	rendered := authz.Render(views, s.opt.PolicyOwner)
	if err := s.opt.ReloadPolicy(rendered); err != nil {
		syncPolicyLog("%v", err)
		return err
	}
	return nil
}

type rateBucket struct {
	windowStart time.Time
	count       int
}

func NewServer(store *Store, opt Options) *Server {
	if opt.RateLimit <= 0 {
		opt.RateLimit = 6
	}
	if opt.RateWindow <= 0 {
		opt.RateWindow = time.Minute
	}
	return &Server{store: store, opt: opt, hits: map[string]*rateBucket{}}
}

// ---- auth ----

func (s *Server) adminAuth(r *http.Request) bool {
	auth := r.Header.Get("Authorization")
	if !strings.HasPrefix(auth, "Bearer ") {
		return false
	}
	token := strings.TrimPrefix(auth, "Bearer ")
	if token == "" {
		return false
	}
	// read the CURRENT hash from the store (single source of truth): a token
	// rotation must invalidate the old token immediately -- the Options
	// snapshot would keep it valid until restart (CI-caught).
	want := s.store.AdminTokenSHA()
	if want == "" {
		want = s.opt.AdminTokenSHA
	}
	if want == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(shaHex(token)), []byte(want)) == 1
}

func (s *Server) rateOK(ip string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	b, ok := s.hits[ip]
	if !ok || now.Sub(b.windowStart) >= s.opt.RateWindow {
		s.hits[ip] = &rateBucket{windowStart: now, count: 1}
		return true
	}
	b.count++
	return b.count <= s.opt.RateLimit
}

// ---- handlers ----

// Handler builds the route table.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/health", s.handleHealth)
	mux.HandleFunc("/api/v1/admin/token-rotate", s.guard(s.handleTokenRotate))
	mux.HandleFunc("/api/v1/invitations", s.guard(s.handleInvitations))
	mux.HandleFunc("/api/v1/invitations/", s.guard(s.handleInvitationOne))
	mux.HandleFunc("/api/v1/enroll/redeem", s.handleRedeem)
	mux.HandleFunc("/api/v1/nodes/resume", s.handleResume)
	mux.HandleFunc("/api/v1/nodes", s.guard(s.handleNodes))
	mux.HandleFunc("/api/v1/nodes/", s.guard(s.handleNodeOne))
	mux.HandleFunc("/api/v1/audit", s.guard(s.handleAudit))
	mux.HandleFunc("/api/v1/nodes-purge/", s.guard(s.handleNodePurge))
	return mux
}

func (s *Server) guard(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !s.adminAuth(r) {
			httpError(w, http.StatusUnauthorized, "admin token required")
			return
		}
		h(w, r)
	}
}

func httpError(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	nodes, active, invitations := s.store.Counts()
	writeJSON(w, 200, map[string]any{
		"status":       "ok",
		"time":         time.Now().UTC().Format(time.RFC3339),
		"nodes":        nodes,
		"nodes_active": active,
		"invitations":  invitations,
	})
}

// handleTokenRotate replaces the admin token (admin-only). The new token is
// returned exactly once; the store keeps only its sha256. Callers must
// update their CLI token file immediately.
func (s *Server) handleTokenRotate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		httpError(w, 405, "POST required")
		return
	}
	nt, err := RandomToken()
	if err != nil {
		httpError(w, 500, err.Error())
		return
	}
	if err := s.store.SetAdminToken(AdminTokenSHA(nt)); err != nil {
		httpError(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]string{"token": nt})
}

// POST /api/v1/invitations {label, profile, ttl_hours}
func (s *Server) handleInvitations(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		httpError(w, 405, "POST required")
		return
	}
	var req struct {
		Label    string  `json:"label"`
		Profile  string  `json:"profile"`
		TTLHours float64 `json:"ttl_hours"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpError(w, 400, "bad JSON: "+err.Error())
		return
	}
	ttl := 24 * time.Hour
	if req.TTLHours > 0 {
		ttl = time.Duration(req.TTLHours * float64(time.Hour))
	}
	code, inv, err := s.store.CreateInvitation(req.Label, req.Profile, ttl, "admin:"+clientIP(r))
	if err != nil {
		httpError(w, 500, err.Error())
		return
	}
	writeJSON(w, 201, map[string]any{
		"id":         inv.ID,
		"code":       code, // shown exactly once
		"expires_at": inv.ExpiresAt,
	})
}

// DELETE /api/v1/invitations/{id}
func (s *Server) handleInvitationOne(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		httpError(w, 405, "DELETE required")
		return
	}
	id := strings.TrimPrefix(r.URL.Path, "/api/v1/invitations/")
	if err := s.store.RevokeInvitation(id, "admin:"+clientIP(r)); err != nil {
		status := 500
		switch {
		case err == ErrNoInvitation:
			status = 404
		case err == ErrUsed:
			status = 409
		}
		httpError(w, status, err.Error())
		return
	}
	writeJSON(w, 200, map[string]string{"revoked": id})
}

// POST /api/v1/enroll/redeem {code, hostname}  (rate limited, no auth)
func (s *Server) handleRedeem(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		httpError(w, 405, "POST required")
		return
	}
	ip := clientIP(r)
	if !s.rateOK(ip) {
		httpError(w, 429, ErrRateLimited.Error())
		return
	}
	var req struct {
		Code     string `json:"code"`
		Hostname string `json:"hostname"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpError(w, 400, "bad JSON: "+err.Error())
		return
	}
	if req.Code == "" || req.Hostname == "" {
		httpError(w, 400, "code and hostname required")
		return
	}
	res, err := s.store.Redeem(req.Code, req.Hostname, s.opt.Headscale, s.opt.Server)
	if err == nil {
		// a new node joined: refresh the deny-by-default policy (best effort)
		_ = s.syncPolicy()
	}
	if err != nil {
		status := 400
		switch err {
		case ErrNoInvitation, ErrBadSecret:
			status = 403
		case ErrUsed, ErrNodeExists:
			status = 409
		case ErrExpired, ErrRevoked:
			status = 403
		}
		httpError(w, status, err.Error())
		return
	}
	writeJSON(w, 201, map[string]any{
		"node_id":       res.NodeID,
		"node_secret":   res.NodeSecret, // returned once over this response only
		"preauth_key":   res.PreauthKey,
		"server":        res.Server,
		"invitation_id": res.InvitationID,
	})
}

// POST /api/v1/nodes/resume {hostname, node_secret}  (rate limited, no auth;
// the node secret IS the credential)
func (s *Server) handleResume(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		httpError(w, 405, "POST required")
		return
	}
	ip := clientIP(r)
	if !s.rateOK("resume:" + ip) {
		httpError(w, 429, ErrRateLimited.Error())
		return
	}
	var req struct {
		Hostname   string `json:"hostname"`
		NodeSecret string `json:"node_secret"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpError(w, 400, "bad JSON: "+err.Error())
		return
	}
	wasPending := false
	if n, ok := s.store.Node(req.Hostname); ok && n.State == NodePending {
		wasPending = true
	}
	key, err := s.store.Resume(req.Hostname, req.NodeSecret, s.opt.Headscale)
	if err == nil && wasPending {
		// first contact: the node's tags are now live in headscale -- policy
		// refresh makes its authorization deterministic (best effort)
		_ = s.syncPolicy()
	}
	if err != nil {
		status := 400
		switch err {
		case ErrNoInvitation, ErrBadSecret, ErrNodeRevoked:
			status = 403
		}
		httpError(w, status, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{
		"preauth_key": key,
		"server":      s.opt.Server,
	})
}

// GET /api/v1/nodes
func (s *Server) handleNodes(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		httpError(w, 405, "GET required")
		return
	}
	nodes, invs := s.store.Snapshot()
	writeJSON(w, 200, map[string]any{"nodes": nodes, "invitations": invs})
}

// POST /api/v1/nodes/{id}/revoke
func (s *Server) handleNodeOne(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/api/v1/nodes/")
	if !strings.HasSuffix(rest, "/revoke") || r.Method != http.MethodPost {
		httpError(w, 405, "POST /api/v1/nodes/{id}/revoke required")
		return
	}
	id := strings.TrimSuffix(rest, "/revoke")
	if err := s.store.RevokeNode(id, "admin:"+clientIP(r), s.opt.Headscale); err != nil {
		status := 500
		if err == ErrNoInvitation {
			status = 404
		}
		httpError(w, status, err.Error())
		return
	}
	if err := s.syncPolicy(); err != nil {
		// revocation already applied at headscale (expire); a failed policy
		// reload must be visible to the admin, not swallowed
		httpError(w, 500, "node revoked but policy reload failed: "+err.Error())
		return
	}
	writeJSON(w, 200, map[string]string{"revoked": id})
}

// POST /api/v1/nodes-purge/{id} -- delete a REVOKED tombstone (frees the
// hostname). Active/pending records are refused by the store.
func (s *Server) handleNodePurge(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		httpError(w, 405, "POST required")
		return
	}
	id := strings.TrimPrefix(r.URL.Path, "/api/v1/nodes-purge/")
	if err := s.store.PurgeNode(id, "admin:"+clientIP(r)); err != nil {
		status := 500
		switch err {
		case ErrNoInvitation:
			status = 404
		case ErrNotRevoked:
			status = 409
		}
		httpError(w, status, err.Error())
		return
	}
	writeJSON(w, 200, map[string]string{"purged": id})
}

// GET /api/v1/audit -- recent audit events (admin).
func (s *Server) handleAudit(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		httpError(w, 405, "GET required")
		return
	}
	writeJSON(w, 200, map[string]any{"audit": s.store.Audit()})
}

func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// AdminTokenSHA computes the stored hash for a token (used at init).
func AdminTokenSHA(token string) string { return shaHex(token) }
