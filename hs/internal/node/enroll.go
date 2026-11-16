// enroll.go -- the node-side enrollment client (Phase 3b).
//
// Flow (ADR-002):
//	hs-node enroll --code CODE --controller URL [--hostname NAME]
//	  1. POST /api/v1/enroll/redeem {code, hostname}  (rate-limited endpoint)
//	  2. persist config.json (no secrets) + node-secret file (hardened)
//	  3. write sing-box config WITH the single-use preauth key (0600)
//	hs-node run  (later boots)
//	  1. load config + node secret
//	  2. POST /api/v1/nodes/resume {hostname, node_secret} -> fresh preauth key
//	  3. same as above; first successful contact flips the node active
//
// The node secret is the machine-held resume credential: it grants exactly
// one capability -- a preauth key for ITSELF. It is not the invitation code
// and not a headscale credential.
package node

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/takomitkm/dialhome/internal/platform"
)

// SecretFileName holds the node resume secret inside the config dir.
const SecretFileName = "node-secret"

// EnrollClient talks to hs-controller over HTTPS.
type EnrollClient struct {
	ControllerURL string
	HTTP          *http.Client
}

func (c *EnrollClient) post(path string, body any) (int, map[string]any, error) {
	b, err := json.Marshal(body)
	if err != nil {
		return 0, nil, err
	}
	hc := c.HTTP
	if hc == nil {
		hc = &http.Client{Timeout: 30 * time.Second}
	}
	resp, err := hc.Post(strings.TrimRight(c.ControllerURL, "/")+path, "application/json", bytes.NewReader(b))
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	var out map[string]any
	raw, _ := io.ReadAll(resp.Body)
	if err := json.Unmarshal(raw, &out); err != nil {
		out = map[string]any{"raw": string(raw)}
	}
	return resp.StatusCode, out, nil
}

// Redeem exchanges an invitation code for the enrollment package.
func (c *EnrollClient) Redeem(code, hostname string) (secret, preauthKey, server string, err error) {
	code2, out, err := c.post("/api/v1/enroll/redeem", map[string]string{"code": code, "hostname": hostname})
	if err != nil {
		return "", "", "", err
	}
	if code2 != 201 {
		return "", "", "", fmt.Errorf("redeem: HTTP %d: %v", code2, out["error"])
	}
	secret, _ = out["node_secret"].(string)
	preauthKey, _ = out["preauth_key"].(string)
	server, _ = out["server"].(string)
	if secret == "" || preauthKey == "" || server == "" {
		return "", "", "", fmt.Errorf("redeem response incomplete: %v", out)
	}
	return secret, preauthKey, server, nil
}

// Resume asks hs-controller for a fresh preauth key using the node secret.
func (c *EnrollClient) Resume(hostname, secret string) (preauthKey, server string, err error) {
	code, out, err := c.post("/api/v1/nodes/resume", map[string]string{"hostname": hostname, "node_secret": secret})
	if err != nil {
		return "", "", err
	}
	if code != 200 {
		return "", "", fmt.Errorf("resume: HTTP %d: %v", code, out["error"])
	}
	preauthKey, _ = out["preauth_key"].(string)
	server, _ = out["server"].(string)
	if preauthKey == "" {
		return "", "", fmt.Errorf("resume response incomplete: %v", out)
	}
	return preauthKey, server, nil
}

// SaveSecret persists the node resume secret with a hardened ACL.
func SaveSecret(cfgDir, secret string) error {
	p := filepath.Join(cfgDir, SecretFileName)
	if err := os.WriteFile(p, []byte(secret+"\n"), 0o600); err != nil {
		return err
	}
	return platform.HardenFile(p)
}

// LoadSecret reads the node resume secret ("" if absent).
func LoadSecret(cfgDir string) string {
	b, err := os.ReadFile(filepath.Join(cfgDir, SecretFileName))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

// Enroll performs the full enrollment: redeem + persist + prepare sing-box
// config for the first supervised run. Returns a human-readable summary.
func Enroll(controllerURL, code, hostname string) (string, error) {
	l, err := DefaultLayout()
	if err != nil {
		return "", err
	}
	if err := l.Create(); err != nil {
		return "", err
	}
	cli := &EnrollClient{ControllerURL: controllerURL}
	secret, preauthKey, server, err := cli.Redeem(code, hostname)
	if err != nil {
		return "", err
	}
	// server comes from hs-controller; it must be https in production (the
	// TestAllowHTTP invariant lives in Config, enforced on Load)
	if !strings.HasPrefix(server, "https://") && os.Getenv("DIALHOME_HS_TEST_ALLOW_HTTP") != "1" {
		return "", fmt.Errorf("controller handed out a non-https control plane URL %q", server)
	}
	cfg := &Config{
		ConfigVersion: ConfigVersion,
		Server:        server,
		Controller:    controllerURL,
		Hostname:      hostname,
		SSHServer:     SSHServer{Enabled: true, DisableForwarding: true},
		Forwarding:    DefaultForwarding,
		StateDir:      l.StateDir,
	}
	// CI-only hatch (documented, like DIALHOME_RELAY_DIR): the e2e chain runs
	// against a plaintext localhost headscale; production enroll never sees it.
	if os.Getenv("DIALHOME_HS_TEST_ALLOW_HTTP") == "1" && strings.HasPrefix(server, "http://") {
		cfg.TestAllowHTTP = true
	}
	if err := cfg.Save(l.CfgDir); err != nil {
		return "", err
	}
	if err := SaveSecret(l.CfgDir, secret); err != nil {
		return "", err
	}
	if err := writeSingBoxConfig(l.CfgDir, cfg, preauthKey); err != nil {
		return "", err
	}
	return fmt.Sprintf("ENROLL ok node=%s server=%s (identity kept in %s)", hostname, server, l.StateDir), nil
}

// PrepareRunConfig renders sing-box.json for a supervised run: resume first
// (a fresh single-use key per boot, ADR-002) then render with that key.
func PrepareRunConfig() (cfgPath string, err error) {
	l, err := DefaultLayout()
	if err != nil {
		return "", err
	}
	cfg, err := Load(l.CfgDir)
	if err != nil {
		return "", err
	}
	secret := LoadSecret(l.CfgDir)
	if secret == "" {
		return "", errors.New("node secret missing (enroll first)")
	}
	cli := &EnrollClient{ControllerURL: cfg.Controller}
	preauthKey, server, err := cli.Resume(cfg.Hostname, secret)
	if err != nil {
		return "", err
	}
	if server != "" {
		cfg.Server = server // controller may hand back the canonical URL
	}
	if err := writeSingBoxConfig(l.CfgDir, cfg, preauthKey); err != nil {
		return "", err
	}
	return filepath.Join(l.CfgDir, "sing-box.json"), nil
}

// writeSingBoxConfig renders the endpoint config with an auth key. The file
// is 0600: the key is single-use and short-lived, but it is still a secret.
func writeSingBoxConfig(cfgDir string, cfg *Config, authKey string) error {
	doc, err := cfg.singBoxConfigWithKey(authKey)
	if err != nil {
		return err
	}
	p := filepath.Join(cfgDir, "sing-box.json")
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, []byte(doc), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, p)
}
