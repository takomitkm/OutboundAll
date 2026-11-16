// Package authz compiles the management plane's authorization decisions into
// a Headscale policy file (ADR-003).
//
// Model:
//   - deny-by-default: the rendered policy's grants section is EMPTY unless a
//     node's profile explicitly allows something (headscale treats omitted
//     grants as allow-all -- the explicit empty array is the deny baseline)
//   - SSH: only nodes tagged tag:dialhome-node-ssh (profile opt-in) are
//     reachable by the controller tag; everything else has no covering rule
//   - node-to-node: no rule, no reachability
package authz

import (
	"fmt"
	"sort"
	"strings"
)

// Tags used by the dialhome stack (ADR-003).
const (
	TagController = "tag:dialhome-controller"
	TagNode       = "tag:dialhome-node"
	TagNodeSSH    = "tag:dialhome-node-ssh"
)

// NodeView is what the renderer needs per node (from the controller store).
type NodeView struct {
	Hostname string
	SSH      bool // profile grants controller SSH access
}

// Render produces the headscale policy JSON (huJSON, trailing comments ok).
// owner names the headscale user allowed to manage the dialhome tags
// (policy v2 rejects tags without owners -- CI-proven). deny-by-default is
// structural: no node without the SSH tag gets any rule.
func Render(nodes []NodeView, owner string) string {
	var sshNodes []string
	for _, n := range nodes {
		if n.SSH {
			sshNodes = append(sshNodes, n.Hostname)
		}
	}
	sort.Strings(sshNodes)
	if owner == "" {
		owner = "ci"
	}

	var b strings.Builder
	b.WriteString("{\n")
	// tag ownership: headscale rejects policies that reference undeclared
	// tags, so every dialhome tag is declared with exactly one owner.
	// headscale policy v2 Username syntax REQUIRES a trailing "@" when the
	// login name does not naturally contain one (types.go isUser = Contains
	// "@"; resolveUser trims the suffix before matching the user name).
	b.WriteString(`  "tagOwners": {` + "\n")
	b.WriteString(`    ` + quote(TagController) + `: [` + quote(owner+"@") + `],` + "\n")
	b.WriteString(`    ` + quote(TagNode) + `: [` + quote(owner+"@") + `],` + "\n")
	b.WriteString(`    ` + quote(TagNodeSSH) + `: [` + quote(owner+"@") + `]` + "\n")
	b.WriteString(`  },` + "\n")
	// deny-by-default baseline: an explicitly EMPTY grants array means
	// "no data-plane reachability unless something else grants it".
	// (Headscale docs: omitted/absent section == allow-all -- do NOT omit.)
	//
	// SSH needs BOTH gates: the ssh section authorizes the session, and the
	// data plane (grants) must carry tcp/22 -- without a grant the peer is
	// filtered out of the netmap entirely (CI-proven: status listed only the
	// controller). Only SSH-enabled nodes get the grant; node-to-node stays
	// ruleless.
	if len(sshNodes) == 0 {
		b.WriteString(`  "grants": [],` + "\n")
		// no SSH-enabled nodes: no ssh rules either -- fully deny
		b.WriteString(`  "ssh": []` + "\n")
	} else {
		b.WriteString(`  "grants": [` + "\n")
		b.WriteString(`    // data plane: controller tag -> SSH-enabled nodes, tcp/22 only` + "\n")
		b.WriteString(`    { "src": [` + quote(TagController) + `], "dst": [` + quote(TagNodeSSH) + `], "ip": ["tcp/22"] }` + "\n")
		b.WriteString(`  ],` + "\n")
		b.WriteString(`  "ssh": [` + "\n")
		b.WriteString(`    // controller tag -> SSH-enabled node tags only` + "\n")
		b.WriteString(`    { "action": "accept", "src": [` + quote(TagController) + `], "dst": [` + quote(TagNodeSSH) + `], "users": ["autogroup:nonroot"] }` + "\n")
		b.WriteString(`  ]` + "\n")
	}
	b.WriteString("}\n")
	return b.String()
}

// TagsForProfile returns the preauth-key tags for a profile name.
// Default profile: managed node WITH controller SSH (the reference posture:
// every node is SSH-manageable by the controller). Profiles without ssh get
// only the base tag -> no covering rule -> unreachable.
func TagsForProfile(profile string) []string {
	switch profile {
	case "", "default":
		return []string{TagNode, TagNodeSSH}
	case "no-ssh":
		return []string{TagNode}
	default:
		return []string{TagNode, TagNodeSSH}
	}
}

// Summary renders a human line for audit/CLI.
func Summary(nodes []NodeView) string {
	var ssh []string
	for _, n := range nodes {
		if n.SSH {
			ssh = append(ssh, n.Hostname)
		}
	}
	if len(ssh) == 0 {
		return "deny-by-default: no SSH-enabled nodes"
	}
	return fmt.Sprintf("deny-by-default: SSH-enabled nodes: %s", strings.Join(ssh, ", "))
}

func quote(s string) string { return `"` + s + `"` }
