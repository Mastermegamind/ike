// Package softkey is a software passkey authenticator for tests: it answers
// the panel's creation and request options the way a browser and a security
// key would. It is never used outside tests and end-to-end checks.
package softkey

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"

	"github.com/YOUR-ORG/ike/agent/internal/panel/webauthn"
)

// Key is one ES256 passkey.
type Key struct {
	ID         []byte
	UserHandle string
	Count      uint32
	// Flags sent with every answer; the default is present + verified.
	Flags byte
	priv  *ecdsa.PrivateKey
}

// New makes a passkey. Count 0 behaves like a synced passkey (the counter
// stays 0); set Count to 1 to make it count up like a security key.
func New() *Key {
	k, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	id := make([]byte, 16)
	_, _ = rand.Read(id)
	return &Key{ID: id, priv: k, Flags: 0x01 | 0x04}
}

func head(major byte, n int) []byte {
	switch {
	case n < 24:
		return []byte{major<<5 | byte(n)}
	case n < 256:
		return []byte{major<<5 | 24, byte(n)}
	default:
		return []byte{major<<5 | 25, byte(n >> 8), byte(n)}
	}
}

func cbInt(v int) []byte {
	if v >= 0 {
		return head(0, v)
	}
	return head(1, -1-v)
}

func cbBytes(b []byte) []byte { return append(head(2, len(b)), b...) }
func cbText(s string) []byte  { return append(head(3, len(s)), s...) }

func (k *Key) cose() []byte {
	x, y := make([]byte, 32), make([]byte, 32)
	k.priv.X.FillBytes(x)
	k.priv.Y.FillBytes(y)
	out := head(5, 5)
	out = append(out, cbInt(1)...)
	out = append(out, cbInt(2)...)
	out = append(out, cbInt(3)...)
	out = append(out, cbInt(-7)...)
	out = append(out, cbInt(-1)...)
	out = append(out, cbInt(1)...)
	out = append(out, cbInt(-2)...)
	out = append(out, cbBytes(x)...)
	out = append(out, cbInt(-3)...)
	out = append(out, cbBytes(y)...)
	return out
}

func (k *Key) authData(rpID string, withCred bool) []byte {
	h := sha256.Sum256([]byte(rpID))
	flags := k.Flags
	if withCred {
		flags |= 0x40
	}
	out := append(h[:], flags, 0, 0, 0, 0)
	binary.BigEndian.PutUint32(out[33:], k.Count)
	if withCred {
		out = append(out, make([]byte, 16)...)
		out = binary.BigEndian.AppendUint16(out, uint16(len(k.ID)))
		out = append(out, k.ID...)
		out = append(out, k.cose()...)
	}
	return out
}

func clientData(typ, challenge, origin string) []byte {
	b, _ := json.Marshal(map[string]any{"type": typ, "challenge": challenge, "origin": origin, "crossOrigin": false})
	return b
}

// Options is the part of the panel's options the authenticator reads.
type Options struct {
	PublicKey struct {
		Challenge string `json:"challenge"`
		RPID      string `json:"rpId"`
		RP        struct {
			ID string `json:"id"`
		} `json:"rp"`
		User struct {
			ID string `json:"id"`
		} `json:"user"`
	} `json:"publicKey"`
}

// Register answers creation options with the body POST /me/passkeys wants
// (without name and password).
func (k *Key) Register(o Options, origin string) map[string]any {
	k.UserHandle = o.PublicKey.User.ID
	cd := clientData("webauthn.create", o.PublicKey.Challenge, origin)
	ad := k.authData(o.PublicKey.RP.ID, true)
	att := head(5, 3)
	att = append(att, cbText("fmt")...)
	att = append(att, cbText("none")...)
	att = append(att, cbText("attStmt")...)
	att = append(att, head(5, 0)...)
	att = append(att, cbText("authData")...)
	att = append(att, cbBytes(ad)...)
	return map[string]any{"id": webauthn.Encode(k.ID), "client_data_json": webauthn.Encode(cd), "attestation_object": webauthn.Encode(att)}
}

// SignIn answers request options with the body POST /session/passkey wants.
func (k *Key) SignIn(o Options, origin string) map[string]any {
	if k.Count > 0 {
		k.Count++
	}
	cd := clientData("webauthn.get", o.PublicKey.Challenge, origin)
	ad := k.authData(o.PublicKey.RPID, false)
	h := sha256.Sum256(cd)
	d := sha256.Sum256(append(append([]byte{}, ad...), h[:]...))
	sig, _ := ecdsa.SignASN1(rand.Reader, k.priv, d[:])
	return map[string]any{"id": webauthn.Encode(k.ID), "client_data_json": webauthn.Encode(cd), "authenticator_data": webauthn.Encode(ad),
		"signature": webauthn.Encode(sig), "user_handle": k.UserHandle}
}
