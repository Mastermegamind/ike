package webauthn

import (
	"crypto"
	"crypto/ecdh"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/x509"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
)

// COSE algorithm numbers Ike accepts, in the order it prefers them.
const (
	AlgES256 = -7
	AlgEdDSA = -8
	AlgRS256 = -257
)

// Algorithms are offered to the browser as pubKeyCredParams.
var Algorithms = []int64{AlgES256, AlgEdDSA, AlgRS256}

// Authenticator data flags.
const (
	flagUP = 0x01 // user present
	flagUV = 0x04 // user verified (PIN, fingerprint, face)
	flagBE = 0x08 // backup eligible (a synced passkey)
	flagBS = 0x10 // backed up
	flagAT = 0x40 // attested credential data included
	flagED = 0x80 // extensions included
)

var b64 = base64.RawURLEncoding

// Encode is base64url without padding, as WebAuthn uses for ids and challenges.
func Encode(b []byte) string { return b64.EncodeToString(b) }

// Decode reads base64url, with or without padding.
func Decode(s string) ([]byte, error) {
	for len(s)%4 != 0 && s[len(s)-1] == '=' {
		s = s[:len(s)-1]
	}
	return b64.DecodeString(s)
}

// NewChallenge is 32 random bytes.
func NewChallenge() []byte {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return b
}

// Expect is what the relying party (the panel) knows before a ceremony.
type Expect struct {
	Challenge []byte
	RPID      string   // the panel's host name, e.g. panel.example.com
	Origins   []string // e.g. https://panel.example.com:2083
	// RequireUV refuses authenticators that didn't check a PIN or biometric.
	RequireUV bool
}

// Credential is a registered passkey.
type Credential struct {
	ID             []byte
	PublicKey      []byte // PKIX DER
	Alg            int64
	SignCount      uint32
	AAGUID         []byte
	BackupEligible bool
	BackedUp       bool
}

type clientData struct {
	Type        string `json:"type"`
	Challenge   string `json:"challenge"`
	Origin      string `json:"origin"`
	CrossOrigin bool   `json:"crossOrigin"`
}

func checkClientData(e Expect, raw []byte, typ string) error {
	var cd clientData
	if err := json.Unmarshal(raw, &cd); err != nil {
		return errors.New("the browser's client data isn't valid JSON")
	}
	if cd.Type != typ {
		return fmt.Errorf("wrong ceremony type %q", cd.Type)
	}
	ch, err := Decode(cd.Challenge)
	if err != nil || len(e.Challenge) == 0 || subtle.ConstantTimeCompare(ch, e.Challenge) != 1 {
		return errors.New("the challenge doesn't match; start again")
	}
	okOrigin := false
	for _, o := range e.Origins {
		okOrigin = okOrigin || o == cd.Origin
	}
	if !okOrigin {
		return fmt.Errorf("the passkey was used on %q, which isn't this panel", cd.Origin)
	}
	if cd.CrossOrigin {
		return errors.New("passkeys can't be used from inside another site")
	}
	return nil
}

type authData struct {
	rpIDHash  []byte
	flags     byte
	signCount uint32
	aaguid    []byte
	credID    []byte
	credKey   map[any]any
}

func parseAuthData(b []byte, wantCred bool) (*authData, error) {
	if len(b) < 37 {
		return nil, errors.New("authenticator data is too short")
	}
	a := &authData{rpIDHash: b[:32], flags: b[32], signCount: binary.BigEndian.Uint32(b[33:37])}
	rest := b[37:]
	if a.flags&flagAT != 0 {
		if len(rest) < 18 {
			return nil, errors.New("attested credential data is too short")
		}
		a.aaguid = rest[:16]
		n := int(binary.BigEndian.Uint16(rest[16:18]))
		rest = rest[18:]
		if n == 0 || n > 1023 || len(rest) < n {
			return nil, errors.New("bad credential id length")
		}
		a.credID = rest[:n]
		rest = rest[n:]
		k, used, err := decodeCBOR(rest)
		if err != nil {
			return nil, fmt.Errorf("credential public key: %w", err)
		}
		m, ok := k.(map[any]any)
		if !ok {
			return nil, errors.New("credential public key isn't a COSE key")
		}
		a.credKey = m
		rest = rest[used:]
	} else if wantCred {
		return nil, errors.New("the authenticator didn't include the new credential")
	}
	if a.flags&flagED != 0 {
		_, used, err := decodeCBOR(rest)
		if err != nil {
			return nil, fmt.Errorf("extensions: %w", err)
		}
		rest = rest[used:]
	}
	if len(rest) != 0 {
		return nil, errors.New("unexpected bytes after the authenticator data")
	}
	return a, nil
}

func (e Expect) checkFlags(a *authData) error {
	want := sha256.Sum256([]byte(e.RPID))
	if subtle.ConstantTimeCompare(a.rpIDHash, want[:]) != 1 {
		return errors.New("the passkey belongs to another site")
	}
	if a.flags&flagUP == 0 {
		return errors.New("the authenticator didn't confirm someone was present")
	}
	if e.RequireUV && a.flags&flagUV == 0 {
		return errors.New("the authenticator didn't check your PIN, fingerprint or face")
	}
	if a.flags&flagBS != 0 && a.flags&flagBE == 0 {
		return errors.New("inconsistent backup flags")
	}
	return nil
}

func num(m map[any]any, k int64) (int64, bool) {
	v, ok := m[k].(int64)
	return v, ok
}

func bin(m map[any]any, k int64) []byte {
	v, _ := m[k].([]byte)
	return v
}

// coseKey turns a COSE_Key into a Go public key.
func coseKey(m map[any]any) (crypto.PublicKey, int64, error) {
	kty, _ := num(m, 1)
	alg, ok := num(m, 3)
	if !ok {
		return nil, 0, errors.New("the key has no algorithm")
	}
	switch {
	case kty == 2 && alg == AlgES256:
		crv, _ := num(m, -1)
		x, y := bin(m, -2), bin(m, -3)
		if crv != 1 || len(x) != 32 || len(y) != 32 {
			return nil, 0, errors.New("ES256 keys must be on P-256")
		}
		raw := append([]byte{4}, append(append([]byte{}, x...), y...)...)
		if _, err := ecdh.P256().NewPublicKey(raw); err != nil {
			return nil, 0, errors.New("the P-256 point isn't on the curve")
		}
		return &ecdsa.PublicKey{Curve: elliptic.P256(), X: new(big.Int).SetBytes(x), Y: new(big.Int).SetBytes(y)}, alg, nil
	case kty == 1 && alg == AlgEdDSA:
		crv, _ := num(m, -1)
		x := bin(m, -2)
		if crv != 6 || len(x) != ed25519.PublicKeySize {
			return nil, 0, errors.New("EdDSA keys must be Ed25519")
		}
		return ed25519.PublicKey(append([]byte{}, x...)), alg, nil
	case kty == 3 && alg == AlgRS256:
		n, e := bin(m, -1), bin(m, -2)
		if len(n) < 256 || len(e) == 0 || len(e) > 4 {
			return nil, 0, errors.New("RSA keys must be at least 2048 bits")
		}
		ee := 0
		for _, c := range e {
			ee = ee<<8 | int(c)
		}
		if ee < 3 || ee%2 == 0 {
			return nil, 0, errors.New("bad RSA exponent")
		}
		return &rsa.PublicKey{N: new(big.Int).SetBytes(n), E: ee}, alg, nil
	}
	return nil, 0, fmt.Errorf("key type %d with algorithm %d isn't supported (use ES256, EdDSA or RS256)", kty, alg)
}

// VerifyRegistration checks navigator.credentials.create()'s response and
// returns the new passkey. Attestation statements aren't checked: Ike asks
// for "none", so any authenticator the person owns is fine.
func VerifyRegistration(e Expect, clientDataJSON, attestationObject []byte) (*Credential, error) {
	if err := checkClientData(e, clientDataJSON, "webauthn.create"); err != nil {
		return nil, err
	}
	v, used, err := decodeCBOR(attestationObject)
	if err != nil || used != len(attestationObject) {
		return nil, errors.New("the attestation object isn't valid CBOR")
	}
	att, ok := v.(map[any]any)
	if !ok {
		return nil, errors.New("the attestation object isn't a map")
	}
	if _, ok := att["fmt"].(string); !ok {
		return nil, errors.New("the attestation object has no format")
	}
	raw, ok := att["authData"].([]byte)
	if !ok {
		return nil, errors.New("the attestation object has no authenticator data")
	}
	a, err := parseAuthData(raw, true)
	if err != nil {
		return nil, err
	}
	if err := e.checkFlags(a); err != nil {
		return nil, err
	}
	pub, alg, err := coseKey(a.credKey)
	if err != nil {
		return nil, err
	}
	der, err := x509.MarshalPKIXPublicKey(pub)
	if err != nil {
		return nil, err
	}
	return &Credential{ID: append([]byte{}, a.credID...), PublicKey: der, Alg: alg, SignCount: a.signCount,
		AAGUID: append([]byte{}, a.aaguid...), BackupEligible: a.flags&flagBE != 0, BackedUp: a.flags&flagBS != 0}, nil
}

// ErrCloned means the signature counter went backwards: two copies of the
// same authenticator may exist.
var ErrCloned = errors.New("this passkey's counter went backwards, which can mean it was copied; it has been turned off")

// VerifyAssertion checks navigator.credentials.get()'s response against a
// stored passkey and returns the new signature counter.
func VerifyAssertion(e Expect, c *Credential, clientDataJSON, authenticatorData, signature []byte) (uint32, error) {
	if err := checkClientData(e, clientDataJSON, "webauthn.get"); err != nil {
		return 0, err
	}
	a, err := parseAuthData(authenticatorData, false)
	if err != nil {
		return 0, err
	}
	if err := e.checkFlags(a); err != nil {
		return 0, err
	}
	pub, err := x509.ParsePKIXPublicKey(c.PublicKey)
	if err != nil {
		return 0, errors.New("the stored key can't be read")
	}
	h := sha256.Sum256(clientDataJSON)
	signed := append(append([]byte{}, authenticatorData...), h[:]...)
	if !verify(pub, c.Alg, signed, signature) {
		return 0, errors.New("the passkey's signature isn't valid")
	}
	if (a.signCount != 0 || c.SignCount != 0) && a.signCount <= c.SignCount {
		return 0, ErrCloned
	}
	return a.signCount, nil
}

func verify(pub crypto.PublicKey, alg int64, msg, sig []byte) bool {
	switch k := pub.(type) {
	case *ecdsa.PublicKey:
		if alg != AlgES256 {
			return false
		}
		d := sha256.Sum256(msg)
		return ecdsa.VerifyASN1(k, d[:], sig)
	case ed25519.PublicKey:
		return alg == AlgEdDSA && ed25519.Verify(k, msg, sig)
	case *rsa.PublicKey:
		if alg != AlgRS256 {
			return false
		}
		d := sha256.Sum256(msg)
		return rsa.VerifyPKCS1v15(k, crypto.SHA256, d[:], sig) == nil
	}
	return false
}

// SameID compares credential ids in constant time.
func SameID(a, b []byte) bool { return len(a) == len(b) && subtle.ConstantTimeCompare(a, b) == 1 }
