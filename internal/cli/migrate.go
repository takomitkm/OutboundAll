package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/takomitkm/outboundall/internal/relay"
)

// relayMigrateLegacy imports the PS-era state into the new registry WITHOUT
// touching running tunnels (spec §10: "不得直接撤销正在运行的旧隧道").
//
// Sources (REFACTOR_BASELINE §6):
//   - legacy machines.json (~/.magpie-connector/machines.json): fp/uuid/macs/user
//   - legacy AK lines: magpie-tunnel-node-<port>  /  magpie-node-<name>
//
// Transformation: for each legacy line, keep key + ports EXACTLY, rewrite only the
// forced command to the new helper path, and re-mark the comment OutboundAll-node-<name>.
// A backup of the system AK is taken before any write.
func relayMigrateLegacy(store *relay.Store, akPath, helperBin, base string, oldAK string) error {
	legacyAK := akPath // the same file: legacy lines live in the system AK
	raw, err := os.ReadFile(legacyAK)
	if err != nil {
		return fmt.Errorf("legacy ak: %w", err)
	}
	content := string(raw)

	// legacy machines.json (optional: identity facts only)
	var legacyHW = map[string]relay.Hardware{}
	if rawHW, err := os.ReadFile(filepath.Join(oldAK, "machines.json")); err == nil {
		var m map[string]map[string]any
		if json.Unmarshal(rawHW, &m) == nil {
			for name, rec := range m {
				h := relay.Hardware{}
				if v, ok := rec["uuid"].(string); ok {
					h.UUID = v
				}
				if v, ok := rec["macs"].(string); ok && v != "" {
					h.MACs = []string{v}
				}
				if v, ok := rec["user"].(string); ok {
					h.User = v
				}
				legacyHW[strings.TrimPrefix(name, "magpie-node-")] = h
			}
		}
	}

	rePort := regexp.MustCompile(`magpie-tunnel-node-(\d+)$`)
	reName := regexp.MustCompile(`magpie-node-([a-z0-9-]+)$`)
	helper := helperBin

	var imported []string
	needsRewrite := false // set when a LEGACY line's forced command is rewritten
	err = store.WithLock(func(r *relay.Registry) error {
		entries := relay.Parse(content)
		out := make([]relay.Entry, 0, len(entries))
		for _, e := range entries {
			keep := e.Raw
			// Idempotent re-import: an ALREADY-migrated line (OutboundAll-node-* marker)
			// is left byte-for-byte intact; only its registry record is rebuilt. This
			// makes migrate-legacy safe to run twice, and lets operators recover a
			// registry that was lost while the AK lines themselves survived
			// (evidence: 2026-11-15 -- a test wiped registry.json but AK kept both
			// production node lines; one migrate-legacy restored both records).
			if e.Kind == "node" && e.Name != "" {
				pl := e.Ports
				if len(pl) < 2 {
					pl = relay.PermitListens(keep)
				}
				ports := relay.Ports{}
				if len(pl) >= 2 {
					// NodeLine renders permitlisten="<SSH>",permitlisten="<APP>"
					ports.SSH, ports.App = pl[0], pl[1]
				} else if len(pl) == 1 {
					ports.SSH = pl[0]
				}
				rec := r.Nodes[e.Name]
				if rec == nil {
					rec = &relay.NodeRecord{Ports: ports, RegisteredAt: "migrated"}
					r.Nodes[e.Name] = rec
				}
				rec.Ports = ports
				rec.State = "active"
				if e.KeyFP != "" {
					rec.TunnelKeyFP = e.KeyFP
				}
				if h, ok := legacyHW[e.Name]; ok {
					rec.Hardware = h
				}
				imported = append(imported, "RESTORED "+e.Name+" -> ports("+fmt.Sprint(ports.App)+","+fmt.Sprint(ports.SSH)+")")
				out = append(out, relay.Entry{Raw: keep, Kind: "node", Name: e.Name})
				continue
			}
			name := ""
			var ports relay.Ports
			isLegacy := false
			if m := reName.FindStringSubmatch(e.Raw); m != nil {
				name = m[1]
				isLegacy = true
			} else if m := rePort.FindStringSubmatch(e.Raw); m != nil {
				name = "port-" + m[1]
				isLegacy = true
			}
			if !isLegacy || e.Kind != "other" {
				// not a legacy node line (or unparseable): preserve byte-for-byte
				out = append(out, relay.Entry{Raw: keep, Kind: e.Kind, Name: e.Name})
				continue
			}
			pl := relay.PermitListens(keep)
			if len(pl) < 1 {
				out = append(out, relay.Entry{Raw: keep, Kind: "other"})
				continue
			}
			// LEGACY lines list permitlisten="<APP>",permitlisten="<SSH>" (reverse
			// of the new NodeLine template) -- keep the original PS-era convention.
			ports = relay.Ports{App: pl[0]}
			if len(pl) >= 2 {
				ports.SSH = pl[1]
			}
			pub := publicKeyBody(keep)
			newLine := relay.NodeLineRaw(helper, ports, pub, name)
			if newLine != keep {
				needsRewrite = true
			}
			// registry: same identity, state active, hardware imported when present
			rec := r.Nodes[name]
			if rec == nil {
				rec = &relay.NodeRecord{Ports: ports, RegisteredAt: "migrated"}
				r.Nodes[name] = rec
			}
			rec.Ports = ports
			rec.State = "active"
			if h, ok := legacyHW[name]; ok {
				rec.Hardware = h
			}
			imported = append(imported, name+" -> ports("+fmt.Sprint(ports.App)+","+fmt.Sprint(ports.SSH)+")")
			out = append(out, relay.Entry{Raw: newLine, Kind: "node", Name: name})
		}
		// Idempotency: when every line was already migrated (pure RESTORED), do NOT
		// rewrite the file at all. Render() normalises trailing newlines, so an
		// unconditional write appended a blank line on every run (evidence:
		// 2026-11-15 -- AK sha changed after a no-op migrate). Only a real forced-
		// command rewrite may touch authorized_keys.
		if needsRewrite {
			return relay.WriteWithBackup(akPath, relay.Render(out))
		}
		return nil
	})
	if err != nil {
		return err
	}
	for _, l := range imported {
		fmt.Println("MIGRATED " + l)
	}
	if needsRewrite {
		fmt.Printf("legacy import done (%d nodes); AK backup written beside %s\n", len(imported), akPath)
	} else {
		fmt.Printf("legacy import done (%d nodes, all already migrated); authorized_keys left byte-for-byte unchanged\n", len(imported))
	}
	return nil
}

// publicKeyBody extracts the base64 body from a full AK line (options + type + b64 + comment).
func publicKeyBody(line string) relay.PublicKeyLine {
	fields := strings.Fields(line)
	for i, f := range fields {
		if f == "ssh-ed25519" && i+1 < len(fields) {
			return relay.PublicKeyLine{Type: f, Body: fields[i+1], Comment: strings.Join(fields[i+2:], " ")}
		}
	}
	return relay.PublicKeyLine{Type: "ssh-ed25519", Body: "", Comment: ""}
}
