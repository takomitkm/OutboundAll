// singbox.go acquires the pinned sing-box binary for the node runtime.
//
// Pins (2026-11-17, GitHub release asset digests):
//
//	windows/amd64: 99185d75d314a07dbba4a130c8ad986cbeb629a07927975636b341ca61f991df
//	linux/amd64:   699a1b510aa4396b2176d32d1181920399ebf1e267835da6f3b598d0f7c24968
//
// Bumping a version = bump the pin in the same commit (hs/scripts/e2e-deps.sh
// carries the same pins for CI).
package node

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// VersionPin is the deployed sing-box version.
const VersionPin = "1.14.4"

type pin struct {
	name string
	sha  string
}

// pinFor returns the release asset name and sha256 for a platform. Kept as a
// switch (not a map literal) so no composite-literal alignment can drift.
func pinFor(goos, goarch string) (pin, bool) {
	switch {
	case goos == "windows" && goarch == "amd64":
		return pin{
			name: "sing-box-" + VersionPin + "-windows-amd64.zip",
			sha:  "99185d75d314a07dbba4a130c8ad986cbeb629a07927975636b341ca61f991df",
		}, true
	case goos == "linux" && goarch == "amd64":
		return pin{
			name: "sing-box-" + VersionPin + "-linux-amd64.tar.gz",
			sha:  "699a1b510aa4396b2176d32d1181920399ebf1e267835da6f3b598d0f7c24968",
		}, true
	}
	return pin{}, false
}

// ReleaseURL builds the download URL for the current platform.
func ReleaseURL() (string, error) {
	p, ok := pinFor(runtime.GOOS, runtime.GOARCH)
	if !ok {
		return "", fmt.Errorf("no pinned sing-box for %s/%s", runtime.GOOS, runtime.GOARCH)
	}
	return "https://github.com/SagerNet/sing-box/releases/download/v" + VersionPin + "/" + p.name, nil
}

// VerifySHA256 returns an error unless data hashes to want.
func VerifySHA256(data []byte, want string) error {
	sum := sha256.Sum256(data)
	got := hex.EncodeToString(sum[:])
	if !strings.EqualFold(got, want) {
		return fmt.Errorf("sha256 mismatch: want %s, got %s", want, got)
	}
	return nil
}

// Ensure downloads (if missing) and verifies the pinned sing-box archive into
// binDir, then extracts the sing-box binary next to it. Returns the binary path.
// Test escape hatch: OUTBOUNDALL_HS_SINGBOX points at an already-provisioned
// binary (CI reuses the e2e-deps download; documented like OUTBOUNDALL_RELAY_DIR).
func Ensure(binDir string, offlineArchive string) (string, error) {
	if v := os.Getenv("OUTBOUNDALL_HS_SINGBOX"); v != "" {
		if _, err := os.Stat(v); err == nil {
			return v, nil
		}
		return "", fmt.Errorf("OUTBOUNDALL_HS_SINGBOX set but missing: %s", v)
	}
	p, ok := pinFor(runtime.GOOS, runtime.GOARCH)
	if !ok {
		return "", fmt.Errorf("no pinned sing-box for %s/%s", runtime.GOOS, runtime.GOARCH)
	}
	exe := "sing-box"
	if runtime.GOOS == "windows" {
		exe = "sing-box.exe"
	}
	binPath := filepath.Join(binDir, exe)
	if info, err := os.Stat(binPath); err == nil && info.Size() > 0 {
		return binPath, nil // already provisioned
	}

	var data []byte
	var err error
	if offlineArchive != "" {
		data, err = os.ReadFile(offlineArchive)
		if err != nil {
			return "", fmt.Errorf("read offline archive: %w", err)
		}
	} else {
		url, err2 := ReleaseURL()
		if err2 != nil {
			return "", err2
		}
		client := &http.Client{Timeout: 10 * time.Minute}
		resp, err2 := client.Get(url)
		if err2 != nil {
			return "", fmt.Errorf("download %s: %w", url, err2)
		}
		defer resp.Body.Close()
		if resp.StatusCode != 200 {
			return "", fmt.Errorf("download %s: status %d", url, resp.StatusCode)
		}
		data, err = io.ReadAll(resp.Body)
		if err2 != nil {
			return "", err2
		}
	}

	if err := VerifySHA256(data, p.sha); err != nil {
		return "", err
	}

	if err := os.MkdirAll(binDir, 0o755); err != nil {
		return "", err
	}
	switch {
	case strings.HasSuffix(p.name, ".zip"):
		err = extractZip(data, binDir, exe)
	case strings.HasSuffix(p.name, ".tar.gz"):
		err = extractTarGz(data, binDir, exe)
	default:
		err = fmt.Errorf("unknown archive type %s", p.name)
	}
	if err != nil {
		return "", err
	}
	return binPath, nil
}

// extractZip pulls exe out of a zip archive, refusing path escapes (zip-slip).
func extractZip(data []byte, dir, exe string) error {
	r, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return fmt.Errorf("open zip: %w", err)
	}
	for _, f := range r.File {
		if f.Name != exe && filepath.Base(f.Name) != exe {
			continue
		}
		if strings.Contains(f.Name, "..") || filepath.IsAbs(f.Name) {
			return fmt.Errorf("refusing suspicious zip entry %q", f.Name)
		}
		rc, err := f.Open()
		if err != nil {
			return err
		}
		defer rc.Close()
		out, err := os.OpenFile(filepath.Join(dir, exe), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
		if err != nil {
			return err
		}
		if _, err := io.Copy(out, rc); err != nil {
			out.Close()
			return err
		}
		return out.Close()
	}
	return fmt.Errorf("%s not found in archive", exe)
}

// extractTarGz pulls exe out of a .tar.gz archive (strip-components:1 shape).
func extractTarGz(data []byte, dir, exe string) error {
	gz, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("open gzip: %w", err)
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("tar: %w", err)
		}
		if filepath.Base(hdr.Name) != exe || hdr.Typeflag != '0' {
			continue
		}
		if strings.Contains(hdr.Name, "..") || filepath.IsAbs(hdr.Name) {
			return fmt.Errorf("refusing suspicious tar entry %q", hdr.Name)
		}
		out, err := os.OpenFile(filepath.Join(dir, exe), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
		if err != nil {
			return err
		}
		if _, err := io.Copy(out, tr); err != nil {
			out.Close()
			return err
		}
		return out.Close()
	}
	return fmt.Errorf("%s not found in archive", exe)
}
