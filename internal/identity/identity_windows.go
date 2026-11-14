//go:build windows

package identity

import (
	"crypto/sha256"
	"encoding/hex"
	"net"
	"os/exec"
	"regexp"
	"strings"
)

func sha256hex(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

func interfaces() []net.Interface {
	list, err := net.Interfaces()
	if err != nil {
		return nil
	}
	return list
}

// platformUUID reads the SMBIOS UUID via WMI/CIM (no admin needed on normal setups).
var zeroUUIDRe = regexp.MustCompile(`(?i)^(none|0{8}(-0{4}){3}-0{12}|0+)$`)

func isZeroUUID(s string) bool { return zeroUUIDRe.MatchString(s) }

func platformUUID() string {
	out, err := exec.Command("powershell", "-NoProfile", "-Command",
		"(Get-CimInstance Win32_ComputerSystemProduct -EA 0).UUID").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(strings.TrimPrefix(strings.ToUpper(string(out)), "UUID"))
}
