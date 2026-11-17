// Package openssh acquires and verifies the portable OpenSSH distribution.
// Contract (spec §11): pinned version + SHA-256, never "latest"; download falls
// back to the relay helper `get openssh` (the same raw-byte passthrough the PS
// edition used), and the hash is verified either way.
package openssh

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// PinnedPackage is the Win32-OpenSSH release the project ships (REFACTOR_BASELINE §2).
type PinnedPackage struct {
	URL     string
	SHA256  string
	Size    int64
	Version string
}

// Current is the pinned Win32-OpenSSH 10.0.0.0p2-Preview package.
//
// The upstream release tag carries no leading "V" (the assets live under
// ".../releases/download/10.0.0.0p2-Preview/"). Spelling it "V10.0.0.0p2-Preview"
// returns 404, which reached users as "download failed and no relay fallback
// configured" on every fresh Windows node.
func Current() PinnedPackage {
	return PinnedPackage{
		URL:     "https://github.com/PowerShell/Win32-OpenSSH/releases/download/10.0.0.0p2-Preview/OpenSSH-Win64.zip",
		SHA256:  "23f50f3458c4c5d0b12217c6a5ddfde0137210a30fa870e98b29827f7b43aba5",
		Size:    5704583,
		Version: "10.0.0.0p2-Preview",
	}
}

// MirrorEnv names an env var that overrides the download URL (a local mirror for
// hosts that cannot reach github.com). The pinned SHA-256 still applies, so a
// mirror cannot smuggle in a different build.
const MirrorEnv = "DIALHOME_OPENSSH_URL"

// sourceURL is the URL Fetch uses: the mirror override when set, else the pin.
func sourceURL() string {
	if v := strings.TrimSpace(os.Getenv(MirrorEnv)); v != "" {
		return v
	}
	return Current().URL
}

// ErrHashMismatch is fatal (a tampered or truncated download must not be extracted).
var ErrHashMismatch = errors.New("openssh: sha256 mismatch")

// VerifyFile hashes a file and compares it to want.
func VerifyFile(path, want string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return err
	}
	got := hex.EncodeToString(h.Sum(nil))
	if !strings.EqualFold(got, want) {
		return fmt.Errorf("%w: got %s want %s", ErrHashMismatch, got, want)
	}
	return nil
}

// Fetch downloads the pinned package to dest, falling back to the relay helper
// (`ssh <helperSSHArgs> get openssh`) when the download URL is unreachable.
func Fetch(dest, helperSSHArgs string) error {
	return fetchFrom(sourceURL(), dest, helperSSHArgs)
}

// fetchFrom is Fetch with the URL spelled out, so the fallback path is reachable
// in a test without touching the network.
func fetchFrom(url, dest, helperSSHArgs string) error {
	httpErr := httpDownload(url, dest)
	if httpErr == nil {
		return nil
	}
	if helperSSHArgs == "" {
		// No channel can serve `get openssh` here (a node before enrollment only
		// has the one-shot credential, whose forced command is `enroll`), so name
		// the two escapes the user actually has instead of a dead end.
		return fmt.Errorf("openssh: download of %s failed (%v); no relay fallback in this context: "+
			"re-run with --openssh-zip <pre-downloaded OpenSSH-Win64.zip> or point %s at a reachable mirror",
			url, httpErr, MirrorEnv)
	}
	if err := helperPull(helperSSHArgs, dest); err != nil {
		return fmt.Errorf("%w (download: %v)", err, httpErr)
	}
	return nil
}

func httpDownload(url, dest string) error {
	resp, err := http.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("openssh: http %d", resp.StatusCode)
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o700); err != nil {
		return err
	}
	out, err := os.CreateTemp(filepath.Dir(dest), filepath.Base(dest)+".tmp-*")
	if err != nil {
		return err
	}
	tmpName := out.Name()
	defer os.Remove(tmpName)
	if _, err := io.Copy(out, resp.Body); err != nil {
		out.Close()
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, dest)
}

// helperPull runs `ssh <args> get openssh` with OS-level stdout redirection so the
// zip arrives byte-exact (the PS-edition lesson: never pipe through text layers).
func helperPull(sshArgs, dest string) error {
	parts := strings.Fields(sshArgs)
	cmdLine := append(parts, "get", "openssh")
	tmp := dest + ".gatemode"
	cmd := exec.Command(cmdLine[0], cmdLine[1:]...)
	out, err := os.Create(tmp)
	if err != nil {
		return err
	}
	cmd.Stdout = out
	cmd.Stderr = os.Stderr
	runErr := cmd.Run()
	out.Close()
	if runErr != nil {
		os.Remove(tmp)
		return fmt.Errorf("openssh gate pull: %w", runErr)
	}
	return os.Rename(tmp, dest)
}
