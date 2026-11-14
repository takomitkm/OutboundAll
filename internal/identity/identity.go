// Package identity collects stable hardware identity facts per platform
// (spec §5.4): SMBIOS/machine-id/IOPlatformUUID first, MACs secondary.
// The username is auxiliary. Nothing here needs admin rights.
package identity

import (
	"crypto/sha256"
	"encoding/hex"
	"net"
	"os"
	"runtime"
	"strings"
)

// Facts is the hardware identity record (identity.json).
type Facts struct {
	UUID        string   `json:"uuid,omitempty"`
	MACs        []string `json:"macs,omitempty"`
	Hostname    string   `json:"hostname,omitempty"`
	User        string   `json:"user,omitempty"`
	Platform    string   `json:"platform"`
	Fingerprint string   `json:"fingerprint,omitempty"`
}

// Collect gathers the facts for the current machine.
func Collect() (Facts, error) {
	f := Facts{Platform: runtime.GOOS, Hostname: hostName(), User: userName()}
	f.UUID = platformUUID()
	if isZeroUUID(f.UUID) {
		f.UUID = ""
	}
	f.MACs = macs()
	f.Fingerprint = Fingerprint(f.UUID, f.MACs)
	return f, nil
}

// Fingerprint is the 16-hex identity hash: UUID first, MAC set second,
// empty when neither exists (spec: "硬件信息缺失时允许登记，但不能错误合并").
func Fingerprint(uuid string, macs []string) string {
	var basis string
	if uuid != "" {
		basis = "u|" + strings.ToUpper(uuid)
	} else if len(macs) > 0 {
		basis = "m|" + strings.Join(macs, ",")
	} else {
		return ""
	}
	sum := sha256.Sum256([]byte(basis))
	return hex.EncodeToString(sum[:])[:16]
}

func hostName() string {
	h, err := os.Hostname()
	if err != nil {
		return ""
	}
	return h
}

func userName() string {
	if u := os.Getenv("USERNAME"); u != "" {
		return u
	}
	if u := os.Getenv("USER"); u != "" {
		return u
	}
	return ""
}

func macs() []string {
	list, err := net.Interfaces()
	if err != nil {
		return nil
	}
	var out []string
	for _, i := range list {
		if len(i.HardwareAddr) == 0 {
			continue
		}
		s := i.HardwareAddr.String()
		if s == "" || strings.EqualFold(s, "00:00:00:00:00:00") {
			continue
		}
		out = append(out, s)
	}
	// deterministic order (insertion sort is fine at this size)
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j] < out[j-1]; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}
