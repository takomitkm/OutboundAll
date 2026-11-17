// ssh.go builds the official tailscale-client command lines. The tailscale
// binary is the ONLY transport (PLAN deviation D5: >=1.104.4 required); the
// CLI never falls back to unauthenticated access.
package admincli

import (
	"fmt"
	"strings"
)

// SSHArgs builds `tailscale --socket S ssh [user@]node [cmd...]`.
// Interactive shells pass tty=true (the CLI adds no PTY flag itself: the
// tailscale client allocates a remote PTY when stdin is a terminal).
func SSHArgs(socket, user, node string, tty bool, cmd []string) []string {
	argv := []string{"tailscale"}
	if socket != "" {
		argv = append(argv, "--socket="+socket)
	}
	argv = append(argv, "ssh")
	target := node
	if user != "" {
		target = user + "@" + node
	}
	argv = append(argv, target)
	argv = append(argv, cmd...)
	return argv
}

// SFTPProxy builds the `sftp` invocation that tunnels through the tailnet:
// ProxyCommand uses `tailscale nc <node> 22` (raw TCP to the sing-box
// ssh_server), so SFTP rides the same authorized path as the shell.
func SFTPProxy(socket, user, node string) ([]string, string) {
	tsNc := []string{"tailscale"}
	if socket != "" {
		tsNc = append(tsNc, "--socket="+socket)
	}
	tsNc = append(tsNc, "nc", node, "22")
	proxy := strings.Join(tsNc, " ")
	target := node
	if user != "" {
		target = user + "@" + node
	}
	return []string{"sftp", "-o", "ProxyCommand=" + proxy, target}, proxy
}

// DoctorLines renders the read-only diagnostics for `hs-admin doctor`.
// Pure so the output format is pinned by tests.
func DoctorLines(controllerURL, health string, socket string, statusJSON string) []string {
	var lines []string
	h := "controller: " + controllerURL
	if health != "" {
		h += " (" + strings.TrimSpace(health) + ")"
	}
	lines = append(lines, h)
	lines = append(lines, "tailscale socket: "+socketLabel(socket))
	if strings.Contains(statusJSON, `"BackendState": "Running"`) {
		lines = append(lines, "tailscale state: Running")
	} else {
		lines = append(lines, "tailscale state: NOT Running (shell/exec will fail)")
	}
	return lines
}

func socketLabel(socket string) string {
	if socket == "" {
		return "default (/var/run/tailscale/tailscaled.sock)"
	}
	return socket
}

// FormatNodes renders the node list table (id/state/hostname/profile).
func FormatNodes(nodes []*Node) string {
	var b strings.Builder
	b.WriteString("NODE                 STATE      PROFILE\n")
	for _, n := range nodes {
		b.WriteString(fmt.Sprintf("%-20s %-10s %s\n", n.ID, n.State, n.Profile))
	}
	return b.String()
}

// FormatInvitations renders the invitation list table.
func FormatInvitations(invs []*Invitation) string {
	var b strings.Builder
	b.WriteString("INVITATION           STATE      LABEL                EXPIRES\n")
	for _, v := range invs {
		b.WriteString(fmt.Sprintf("%-20s %-10s %-20s %s\n", v.ID, v.State, v.Label, v.ExpiresAt))
	}
	return b.String()
}
