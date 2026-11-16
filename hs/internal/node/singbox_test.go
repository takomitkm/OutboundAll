package node

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// N-sb-1: sha256 verification rejects wrong bytes (pin integrity).
func TestVerifySHA256Mismatch(t *testing.T) {
	data := bytes.Repeat([]byte("x"), 1024)
	if err := VerifySHA256(data, strings.Repeat("0", 64)); err == nil {
		t.Fatal("wrong sha accepted -- pin integrity broken")
	}
	sum := sha256.Sum256(data)
	if err := VerifySHA256(data, hex.EncodeToString(sum[:])); err != nil {
		t.Fatalf("matching sha rejected: %v", err)
	}
}

// N-sb-2: pins cover the current platform and URL shape is exact.
func TestReleaseURL(t *testing.T) {
	url, err := ReleaseURL()
	if err != nil {
		t.Skipf("no pin for %s/%s (expected only on unpinned platforms)", runtime.GOOS, runtime.GOARCH)
	}
	want := "https://github.com/SagerNet/sing-box/releases/download/v" + VersionPin + "/"
	if !strings.HasPrefix(url, want) {
		t.Fatalf("unexpected URL %q", url)
	}
}

// N-sb-3 (zip-slip): an archive entry escaping the target dir must be refused.
func TestExtractZipRefusesZipSlip(t *testing.T) {
	// craft a zip with a hostile escaping entry AND the expected innocent one
	buf := &bytes.Buffer{}
	w := zip.NewWriter(buf)
	hostile, err := w.Create(`..\evil-sing-box.exe`)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = hostile.Write([]byte("evil"))
	innocent, err := w.Create("sing-box.exe")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = innocent.Write([]byte("good"))
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	if err := extractZip(buf.Bytes(), dir, "sing-box.exe"); err != nil {
		t.Logf("extractor refused: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "..", "evil-sing-box.exe")); err == nil {
		t.Fatal("zip-slip: file escaped the target directory")
	}
}

// N-sb-4: an archive missing the binary is a clean error, not a silent pass.
func TestExtractZipMissingBinary(t *testing.T) {
	buf := &bytes.Buffer{}
	w := zip.NewWriter(buf)
	other, _ := w.Create("something-else.txt")
	_, _ = other.Write([]byte("hi"))
	_ = w.Close()

	dir := t.TempDir()
	if err := extractZip(buf.Bytes(), dir, "sing-box.exe"); err == nil {
		t.Fatal("missing binary accepted")
	}
}
