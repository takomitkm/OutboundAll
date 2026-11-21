// Package crypto generates and formats the ed25519 keys OutboundAll uses, in the
// OpenSSH on-disk format ("openssh-key-v1" container), using only the standard library.
//
// Security contract (docs/SECURITY_MODEL.md):
//   - private keys are generated IN PROCESS on the machine that will use them and are
//     written 0600 by the caller; they never travel over the wire
//   - the container is unencrypted (no passphrase) because the node runs headless;
//     the 0600/0700 permission model is the control, and tests pin it
package crypto

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"strings"
)

const (
	magic       = "openssh-key-v1\x00"
	keyTypeName = "ssh-ed25519"
)

// KeyPair is a generated ed25519 pair in OpenSSH wire forms.
type KeyPair struct {
	Private PEMBlock      // "-----BEGIN OPENSSH PRIVATE KEY-----" armoured
	Public  AuthorizedKey // "ssh-ed25519 AAAA... comment" line
	Seed    ed25519.PrivateKey
}

// AuthorizedKey is a parsed public key line.
type AuthorizedKey struct {
	Type    string
	Blob    []byte // the wire blob (string ssh-ed25519 || string pub)
	Comment string
}

// Line renders the authorized_keys line (without leading/trailing whitespace).
func (a AuthorizedKey) Line() string {
	return a.Type + " " + base64.StdEncoding.EncodeToString(a.Blob) + " " + a.Comment
}

// Fingerprint returns the SHA256 fingerprint in OpenSSH display form.
func (a AuthorizedKey) Fingerprint() string {
	sum := sha256.Sum256(a.Blob)
	return "SHA256:" + base64.RawStdEncoding.EncodeToString(sum[:])
}

// Generate creates a fresh key pair with the given comment.
func Generate(comment string) (KeyPair, error) {
	seed := make([]byte, ed25519.SeedSize)
	if _, err := rand.Read(seed); err != nil {
		return KeyPair{}, fmt.Errorf("keygen: %w", err)
	}
	return fromSeed(seed, comment)
}

// fromSeed builds the pair from a 32-byte seed. The public half is DERIVED:
// the previous signature took priv and pub as independent parameters, which
// made a mismatched assembly representable at the mint (the credential
// incident: a shipped container advertised a different public half than its
// seed, and the node could only discover it after the one-shot credential was
// spent). One parameter makes that state unconstructible here; verification
// remains for EXTERNAL artifacts (credentials received over the wire) via
// VerifyPrivateKey / parsePrivateKey.
func fromSeed(seed []byte, comment string) (KeyPair, error) {
	if len(seed) != ed25519.SeedSize {
		return KeyPair{}, fmt.Errorf("crypto: seed is %d bytes, expected %d", len(seed), ed25519.SeedSize)
	}
	priv := ed25519.NewKeyFromSeed(seed)
	pub := priv.Public().(ed25519.PublicKey)
	pubBlob := pubBlob(pub)
	var check [4]byte
	if _, err := rand.Read(check[:]); err != nil {
		return KeyPair{}, fmt.Errorf("keygen rand: %w", err)
	}

	var sec bytes.Buffer
	writeUint32(&sec, binary.LittleEndian.Uint32(check[:]))
	writeUint32(&sec, binary.LittleEndian.Uint32(check[:]))
	writeString(&sec, []byte(keyTypeName))
	writeString(&sec, pub)
	writeString(&sec, priv) // 64 bytes: seed || pub
	writeString(&sec, []byte(comment))
	// pad to 8-byte multiple with 1,2,3...
	for pad := byte(1); sec.Len()%8 != 0; pad++ {
		sec.WriteByte(pad)
	}

	var w bytes.Buffer
	w.WriteString(magic)
	writeString(&w, []byte("none")) // ciphername
	writeString(&w, []byte("none")) // kdfname
	writeString(&w, nil)            // kdfoptions
	writeUint32(&w, 1)              // number of keys
	writeString(&w, pubBlob)        // public key
	writeString(&w, sec.Bytes())    // private section

	armoured := armour(w.Bytes())
	return KeyPair{
		Private: PEMBlock(armoured),
		Public: AuthorizedKey{
			Type:    keyTypeName,
			Blob:    pubBlob,
			Comment: comment,
		},
		Seed: priv,
	}, nil
}

// PEMBlock is the armoured OpenSSH private key file body.
type PEMBlock string

const (
	beginMarker = "-----BEGIN OPENSSH PRIVATE KEY-----"
	endMarker   = "-----END OPENSSH PRIVATE KEY-----"
)

func armour(raw []byte) string {
	b64 := base64.StdEncoding.EncodeToString(raw)
	var out bytes.Buffer
	out.WriteString(beginMarker + "\n")
	for len(b64) > 70 {
		out.WriteString(b64[:70] + "\n")
		b64 = b64[70:]
	}
	if b64 != "" {
		out.WriteString(b64 + "\n")
	}
	out.WriteString(endMarker + "\n")
	return out.String()
}

func pubBlob(pub ed25519.PublicKey) []byte {
	var b bytes.Buffer
	writeString(&b, []byte(keyTypeName))
	writeString(&b, pub)
	return b.Bytes()
}

func writeString(w *bytes.Buffer, s []byte) {
	var l [4]byte
	binary.BigEndian.PutUint32(l[:], uint32(len(s)))
	w.Write(l[:])
	w.Write(s)
}

func writeUint32(w *bytes.Buffer, v uint32) {
	var b [4]byte
	binary.BigEndian.PutUint32(b[:], v)
	w.Write(b[:])
}

// --- self-consistency checks ---------------------------------------------
//
// An OpenSSH private key states its public half three times: in the container
// header, inside the private section, and implicitly in the last 32 bytes of the
// 64-byte ed25519 private key (which is seed||pub). They must agree. A container
// assembled from mismatched halves still looks like a valid key file and still
// loads into ssh, but the server compares the offered public key with
// authorized_keys and answers "Permission denied (publickey)" -- naming neither
// half, and enrollment credentials are single-use, so the chance to diagnose it
// is spent. These functions turn that silence into a local error.

// VerifyPrivateKey checks that armoured is an unencrypted openssh-key-v1 ed25519
// key whose three public-key copies agree, and that its wire blob equals want
// (skipped when want is nil).
func VerifyPrivateKey(armoured string, want []byte) error {
	parts, err := parsePrivateKey(armoured)
	if err != nil {
		return err
	}
	if want != nil && !bytes.Equal(parts.blob, want) {
		return fmt.Errorf("crypto: container advertises %s, expected %s",
			base64.StdEncoding.EncodeToString(parts.blob),
			base64.StdEncoding.EncodeToString(want))
	}
	return nil
}

// VerifyKeyPair is VerifyPrivateKey for a freshly minted pair: it catches a mint
// that built the container out of mismatched halves.
func VerifyKeyPair(kp KeyPair) error {
	return VerifyPrivateKey(string(kp.Private), kp.Public.Blob)
}

type privateParts struct {
	blob []byte
	pub  ed25519.PublicKey
	priv ed25519.PrivateKey
}

func parsePrivateKey(armoured string) (privateParts, error) {
	raw, err := unarmour(armoured)
	if err != nil {
		return privateParts{}, err
	}
	r := &binReader{b: raw}
	head, err := r.take(len(magic))
	if err != nil {
		return privateParts{}, err
	}
	if string(head) != magic {
		return privateParts{}, errors.New("crypto: not an openssh-key-v1 container")
	}
	for _, field := range []string{"ciphername", "kdfname"} {
		v, err := r.str()
		if err != nil {
			return privateParts{}, err
		}
		if string(v) != "none" {
			return privateParts{}, fmt.Errorf("crypto: %s is %q, expected an unencrypted key", field, v)
		}
	}
	if _, err := r.str(); err != nil { // kdfoptions
		return privateParts{}, err
	}
	n, err := r.u32()
	if err != nil {
		return privateParts{}, err
	}
	if n != 1 {
		return privateParts{}, fmt.Errorf("crypto: container holds %d keys, expected 1", n)
	}
	blob, err := r.str()
	if err != nil {
		return privateParts{}, err
	}
	sec, err := r.str()
	if err != nil {
		return privateParts{}, err
	}

	s := &binReader{b: sec}
	check1, err := s.u32()
	if err != nil {
		return privateParts{}, err
	}
	check2, err := s.u32()
	if err != nil {
		return privateParts{}, err
	}
	if check1 != check2 {
		return privateParts{}, errors.New("crypto: private section check bytes differ (truncated or re-assembled key)")
	}
	typ, err := s.str()
	if err != nil {
		return privateParts{}, err
	}
	if string(typ) != keyTypeName {
		return privateParts{}, fmt.Errorf("crypto: unsupported key type %q (only ssh-ed25519)", typ)
	}
	storedPub, err := s.str()
	if err != nil {
		return privateParts{}, err
	}
	storedPriv, err := s.str()
	if err != nil {
		return privateParts{}, err
	}
	if _, err := s.str(); err != nil { // comment
		return privateParts{}, err
	}
	if len(storedPub) != ed25519.PublicKeySize {
		return privateParts{}, fmt.Errorf("crypto: stored public key is %d bytes, expected %d", len(storedPub), ed25519.PublicKeySize)
	}
	if len(storedPriv) != ed25519.PrivateKeySize {
		return privateParts{}, fmt.Errorf("crypto: stored private key is %d bytes, expected %d", len(storedPriv), ed25519.PrivateKeySize)
	}

	derived := ed25519.NewKeyFromSeed(storedPriv[:32])
	derivedPub := derived[32:]
	if !bytes.Equal(derivedPub, storedPub) {
		return privateParts{}, fmt.Errorf("crypto: seed derives %s but the private section stores %s",
			base64.StdEncoding.EncodeToString(derivedPub),
			base64.StdEncoding.EncodeToString(storedPub))
	}
	if !bytes.Equal(storedPriv[32:], derivedPub) {
		return privateParts{}, errors.New("crypto: trailing public key inside the private key does not match its seed")
	}

	b := &binReader{b: blob}
	blobType, err := b.str()
	if err != nil {
		return privateParts{}, err
	}
	if string(blobType) != keyTypeName {
		return privateParts{}, fmt.Errorf("crypto: container header key type %q is not ssh-ed25519", blobType)
	}
	blobPub, err := b.str()
	if err != nil {
		return privateParts{}, err
	}
	if !bytes.Equal(blobPub, derivedPub) {
		return privateParts{}, fmt.Errorf("crypto: container header advertises %s but the seed derives %s",
			base64.StdEncoding.EncodeToString(blobPub),
			base64.StdEncoding.EncodeToString(derivedPub))
	}
	return privateParts{blob: blob, pub: ed25519.PublicKey(derivedPub), priv: derived}, nil
}

// unarmour strips the BEGIN/END markers and blank lines, then base64-decodes the
// body (OpenSSH wraps at 70 columns).
func unarmour(armoured string) ([]byte, error) {
	var body strings.Builder
	markers := 0
	for _, line := range strings.Split(armoured, "\n") {
		line = strings.TrimSpace(line)
		switch line {
		case beginMarker, endMarker:
			markers++
		case "":
		default:
			body.WriteString(line)
		}
	}
	if markers != 2 {
		return nil, errors.New("crypto: missing OPENSSH PRIVATE KEY armour markers")
	}
	raw, err := base64.StdEncoding.DecodeString(body.String())
	if err != nil {
		return nil, fmt.Errorf("crypto: bad armour base64: %w", err)
	}
	return raw, nil
}

type binReader struct {
	b []byte
	i int
}

func (r *binReader) take(n int) ([]byte, error) {
	if n < 0 || r.i+n > len(r.b) {
		return nil, errors.New("crypto: key container truncated")
	}
	out := r.b[r.i : r.i+n]
	r.i += n
	return out, nil
}

func (r *binReader) u32() (uint32, error) {
	raw, err := r.take(4)
	if err != nil {
		return 0, err
	}
	return binary.BigEndian.Uint32(raw), nil
}

func (r *binReader) str() ([]byte, error) {
	n, err := r.u32()
	if err != nil {
		return nil, err
	}
	if n > uint32(len(r.b)) {
		return nil, errors.New("crypto: key container field length out of range")
	}
	return r.take(int(n))
}

// ParsePublicKeyLine parses an authorized_keys line ("ssh-ed25519 AAAA... comment...").
// Only ssh-ed25519 is accepted: OutboundAll mints ed25519 keys exclusively.
func ParsePublicKeyLine(line string) (AuthorizedKey, error) {
	fields := strings.Fields(line)
	if len(fields) < 2 {
		return AuthorizedKey{}, errors.New("crypto: public key line needs at least type and body")
	}
	if fields[0] != keyTypeName {
		return AuthorizedKey{}, fmt.Errorf("crypto: unsupported key type %q (only ssh-ed25519)", fields[0])
	}
	blob, err := base64.StdEncoding.DecodeString(fields[1])
	if err != nil {
		return AuthorizedKey{}, fmt.Errorf("crypto: bad base64 body: %w", err)
	}
	comment := strings.Join(fields[2:], " ")
	return AuthorizedKey{Type: fields[0], Blob: blob, Comment: comment}, nil
}
