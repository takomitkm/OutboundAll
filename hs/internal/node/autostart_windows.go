//go:build windows

package node

import (
	"fmt"
	"os/exec"
)

// installWindowsRun writes the HKCU Run value via reg.exe (user-level; the
// sandbox-denied case must surface as a real error, not a silent success --
// H13 in docs/PITFALLS.md).
func installWindowsRun(hsNodeBin string) (string, error) {
	data := WindowsRunValue(hsNodeBin)
	out, err := exec.Command("reg", "add", `HKCU\`+WindowsRunKey,
		"/v", AutostartKey, "/t", "REG_SZ", "/d", data, "/f").CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("reg add HKCU Run: %v: %s", err, out)
	}
	return "HKCU\\" + WindowsRunKey + "\\" + AutostartKey + " = " + data, nil
}

func removeWindowsRun() (string, error) {
	out, err := exec.Command("reg", "delete", `HKCU\`+WindowsRunKey,
		"/v", AutostartKey, "/f").CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("reg delete HKCU Run: %v: %s", err, out)
	}
	return "removed HKCU\\" + WindowsRunKey + "\\" + AutostartKey, nil
}
