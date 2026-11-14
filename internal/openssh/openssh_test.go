package openssh

import (
	"os"
	"path/filepath"
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
