package openssh

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestVerifyFileHash(t *testing.T) {
	p := filepath.Join(t.TempDir(), "x")
	os.WriteFile(p, []byte("hello"), 0o600)
	want := "2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824" // sha256("hello")
	if err := VerifyFile(p, want); err != nil {
		t.Fatalf("verify: %v", err)
	}
	if err := VerifyFile(p, "0000000000000000000000000000000000000000000000000000000000000000"); err == nil {
		t.Fatal("wrong hash must fail")
	}
}

func TestPinnedPackageNoLatest(t *testing.T) {
	cur := Current()
	if cur.URL == "" || cur.SHA256 == "" || cur.Version == "" {
		t.Fatal("pinned package must carry URL+hash+version (never latest)")
	}
	if len(cur.SHA256) != 64 {
		t.Fatalf("sha256 must be hex64: %s", cur.SHA256)
	}
}

func TestHTTPDownloadToTempThenRename(t *testing.T) {
	dir := t.TempDir()
	dest := filepath.Join(dir, "pkg.zip")
	// local server substitute: file:// is not supported by httpDownload; use a tiny
	// in-process check instead -- ensure tmp litter is cleaned on failure
	if err := httpDownload("http://127.0.0.1:1/nothing", dest); err == nil {
		t.Fatal("must fail on closed port")
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 0 {
		t.Fatalf("temp litter after failed download: %v", entries)
	}
}

// TestPinnedURLMatchesReleaseTag pins the 404 that failed every fresh Windows
// node: upstream publishes the assets under a tag with no leading "V".
func TestPinnedURLMatchesReleaseTag(t *testing.T) {
	cur := Current()
	if !strings.Contains(cur.URL, "/download/"+cur.Version+"/") {
		t.Fatalf("pinned URL %s does not use release tag %q", cur.URL, cur.Version)
	}
	if strings.Contains(cur.URL, "/download/V") {
		t.Fatal(`release tag must not carry a leading "V" (upstream answers 404)`)
	}
}

// TestFetchFallsBackToHelper covers the path that was dead: EnsureWindowsPackage
// passed "" unconditionally, so `get openssh` could never run.
func TestFetchFallsBackToHelper(t *testing.T) {
	dest := filepath.Join(t.TempDir(), "pkg.zip")
	err := fetchFrom("http://127.0.0.1:1/nothing", dest, "dialhome-no-such-ssh-binary")
	if err == nil {
		t.Fatal("both channels failing must return an error")
	}
	if !strings.Contains(err.Error(), "gate pull") {
		t.Fatalf("helper fallback was not attempted: %v", err)
	}
	if _, statErr := os.Stat(dest); statErr == nil {
		t.Fatal("a failed fetch must not leave a package behind")
	}
}

// TestFetchWithoutHelperNamesTheEscapes: the no-fallback branch must not be a
// dead end for the user.
func TestFetchWithoutHelperNamesTheEscapes(t *testing.T) {
	dest := filepath.Join(t.TempDir(), "pkg.zip")
	err := fetchFrom("http://127.0.0.1:1/nothing", dest, "")
	if err == nil {
		t.Fatal("must fail on a closed port")
	}
	for _, want := range []string{"--openssh-zip", MirrorEnv} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error must mention %s, got: %v", want, err)
		}
	}
}
