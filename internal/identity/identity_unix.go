//go:build linux || darwin

package identity

import (
	"crypto/sha256"
	"encoding/hex"
	"net"
	"os"
	"os/exec"
	"regexp"
	"runtime"
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

// platformUUID: /etc/machine-id (linux) or IOPlatformUUID (macOS).
var zeroUUIDRe = regexp.MustCompile(`(?i)^(none|0{8}(-0{4}){3}-0{12}|0+)$`)

func isZeroUUID(s string) bool { return zeroUUIDRe.MatchString(s) }

func platformUUID() string {
	if runtime_isLinux() {
		if raw, err := os.ReadFile("/etc/machine-id"); err == nil {
			return strings.TrimSpace(string(raw))
		}
		if raw, err := os.ReadFile("/var/lib/dbus/machine-id"); err == nil {
			return strings.TrimSpace(string(raw))
		}
		// DMI as a last resort (usually root-readable only; tolerate failure)
		if raw, err := os.ReadFile("/sys/class/dmi/id/product_uuid"); err == nil {
			return strings.TrimSpace(string(raw))
		}
		return ""
	}
	// darwin
	out, err := exec.Command("ioreg", "-rd1", "-c", "IOPlatformExpertDevice").Output()
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(out), "\n") {
		if strings.Contains(line, "IOPlatformUUID") {
			parts := strings.SplitN(line, "=", 2)
			if len(parts) == 2 {
				return strings.Trim(parts[1], " \"")
			}
		}
	}
	return ""
}

func runtime_isLinux() bool { return runtime.GOOS == "linux" }
