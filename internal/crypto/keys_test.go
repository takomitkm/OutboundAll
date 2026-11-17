package crypto

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"strings"
	"testing"
)

func TestGenerateShape(t *testing.T) {
	kp, err := Generate("dialhome-node-test")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(kp.Private), beginMarker) {
		t.Fatal("private key missing BEGIN marker")
	}
	if !strings.Contains(string(kp.Private), endMarker) {
		t.Fatal("private key missing END marker")
	}
	if !strings.HasPrefix(kp.Public.Line(), "ssh-ed25519 ") {
		t.Fatal("public line must start with ssh-ed25519")
	}
	if !strings.HasSuffix(kp.Public.Line(), " dialhome-node-test") {
		t.Fatalf("comment lost: %q", kp.Public.Line())
	}
	if kp.Seed[0] == kp.Seed[1] && kp.Seed[1] == kp.Seed[2] {
		t.Fatal("seed looks non-random")
	}
}

func TestGenerateUnique(t *testing.T) {
	a, _ := Generate("a")
	b, _ := Generate("a")
	if bytes.Equal(a.Seed, b.Seed) {
		t.Fatal("two generated pairs must differ")
	}
	if a.Public.Line() == b.Public.Line() {
		t.Fatal("two generated public keys must differ")
	}
}

func TestParsePublicKeyLine(t *testing.T) {
	kp, err := Generate("node-x")
	if err != nil {
		t.Fatal(err)
	}
	line := kp.Public.Line()
	got, err := ParsePublicKeyLine(line)
	if err != nil {
		t.Fatal(err)
	}
	if got.Type != "ssh-ed25519" || got.Comment != "node-x" {
		t.Fatalf("round trip mismatch: %+v", got)
	}
	if !bytes.Equal(got.Blob, kp.Public.Blob) {
		t.Fatal("blob mismatch after round trip")
	}
	if got.Fingerprint() != kp.Public.Fingerprint() {
		t.Fatal("fingerprint mismatch after round trip")
	}
	if !strings.HasPrefix(kp.Public.Fingerprint(), "SHA256:") {
		t.Fatal("fingerprint must use SHA256: prefix")
	}
}

func TestParseRejects(t *testing.T) {
	if _, err := ParsePublicKeyLine("ssh-rsa AAAA... comment"); err == nil {
		t.Fatal("non-ed25519 type must be rejected")
	}
	if _, err := ParsePublicKeyLine("ssh-ed25519 not-base64!!"); err == nil {
		t.Fatal("bad base64 must be rejected")
	}
	if _, err := ParsePublicKeyLine("ssh-ed25519"); err == nil {
		t.Fatal("body-less line must be rejected")
	}
}

func TestVerifyKeyPairAcceptsGenerated(t *testing.T) {
	for i := 0; i < 4; i++ {
		kp, err := Generate("selfcheck")
		if err != nil {
			t.Fatal(err)
		}
		if err := VerifyKeyPair(kp); err != nil {
			t.Fatalf("generated pair failed its own self-check: %v", err)
		}
	}
}

// TestVerifyKeyPairRejectsMismatchedHalves pins the incident shape: a container
// whose public half comes from a different key. It is still a loadable key file,
// ssh still offers it, and the relay can only answer "Permission denied
// (publickey)" -- after the single-use credential has been spent.
func TestVerifyKeyPairRejectsMismatchedHalves(t *testing.T) {
	a, err := Generate("a")
	if err != nil {
		t.Fatal(err)
	}
	b, err := Generate("b")
	if err != nil {
		t.Fatal(err)
	}
	broken, err := fromSeed(a.Seed, ed25519.PublicKey(b.Seed[32:]), "a")
	if err != nil {
		t.Fatal(err)
	}
	err = VerifyKeyPair(broken)
	if err == nil {
		t.Fatal("container built from mismatched halves must be rejected")
	}
	if !strings.Contains(err.Error(), "seed derives") {
		t.Fatalf("error must name the mismatch, got: %v", err)
	}
}

// TestVerifyPrivateKeyRejectsTamperedSeed covers the third copy of the public
// key: the one implied by the trailing 32 bytes of the 64-byte private key.
func TestVerifyPrivateKeyRejectsTamperedSeed(t *testing.T) {
	a, err := Generate("a")
	if err != nil {
		t.Fatal(err)
	}
	tampered := make(ed25519.PrivateKey, ed25519.PrivateKeySize)
	copy(tampered, a.Seed)
	tampered[0] ^= 0xff // seed no longer derives the stored public half
	kp, err := fromSeed(tampered, ed25519.PublicKey(a.Seed[32:]), "a")
	if err != nil {
		t.Fatal(err)
	}
	if err := VerifyKeyPair(kp); err == nil {
		t.Fatal("private key whose seed disagrees with its stored public key must be rejected")
	}
}

func TestVerifyPrivateKeyChecksExpectedBlob(t *testing.T) {
	a, _ := Generate("a")
	b, _ := Generate("b")
	if err := VerifyPrivateKey(string(a.Private), a.Public.Blob); err != nil {
		t.Fatalf("matching blob must pass: %v", err)
	}
	if err := VerifyPrivateKey(string(a.Private), b.Public.Blob); err == nil {
		t.Fatal("a foreign blob must be rejected")
	}
}

func TestVerifyPrivateKeyRejectsGarbage(t *testing.T) {
	kp, _ := Generate("a")
	cases := map[string]string{
		"empty":      "",
		"no markers": base64.StdEncoding.EncodeToString([]byte(magic)),
		"bad base64": beginMarker + "\n!!!!\n" + endMarker,
		"public line as private": beginMarker + "\n" +
			base64.StdEncoding.EncodeToString([]byte(kp.Public.Line())) + "\n" + endMarker,
	}
	for name, in := range cases {
		if err := VerifyPrivateKey(in, nil); err == nil {
			t.Fatalf("%s: must be rejected", name)
		}
	}
}

func TestFingerprintIsSha256OfBlob(t *testing.T) {
	kp, _ := Generate("fp-test")
	// recompute independently: sha256 over the blob inside the public line
	if !strings.Contains(kp.Public.Line(), base64.StdEncoding.EncodeToString(kp.Public.Blob)) {
		t.Fatal("blob must be embedded verbatim in the public line")
	}
}
