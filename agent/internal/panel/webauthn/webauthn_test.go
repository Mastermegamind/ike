package webauthn

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"math/big"
	"strings"
	"testing"
)

// ---- a tiny CBOR encoder for building authenticator responses

type kv struct {
	k any
	v any
}

func cbHead(major byte, n uint64) []byte {
	switch {
	case n < 24:
		return []byte{major<<5 | byte(n)}
	case n < 1<<8:
		return []byte{major<<5 | 24, byte(n)}
	case n < 1<<16:
		return []byte{major<<5 | 25, byte(n >> 8), byte(n)}
	default:
		b := []byte{major<<5 | 26, 0, 0, 0, 0}
		binary.BigEndian.PutUint32(b[1:], uint32(n))
		return b
	}
}

func cb(v any) []byte {
	switch x := v.(type) {
	case int:
		if x >= 0 {
			return cbHead(0, uint64(x))
		}
		return cbHead(1, uint64(-1-x))
	case []byte:
		return append(cbHead(2, uint64(len(x))), x...)
	case string:
		return append(cbHead(3, uint64(len(x))), x...)
	case []kv:
		out := cbHead(5, uint64(len(x)))
		for _, p := range x {
			out = append(out, cb(p.k)...)
			out = append(out, cb(p.v)...)
		}
		return out
	case []any:
		out := cbHead(4, uint64(len(x)))
		for _, e := range x {
			out = append(out, cb(e)...)
		}
		return out
	}
	panic("cb: unsupported")
}

// ---- a software authenticator

type fakeKey struct {
	alg  int64
	priv crypto.Signer
	cose []kv
}

func newKey(t *testing.T, alg int64) *fakeKey {
	switch alg {
	case AlgES256:
		k, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		pad := func(b *big.Int) []byte { o := make([]byte, 32); b.FillBytes(o); return o }
		return &fakeKey{alg, k, []kv{{1, 2}, {3, AlgES256}, {-1, 1}, {-2, pad(k.X)}, {-3, pad(k.Y)}}}
	case AlgEdDSA:
		pub, priv, _ := ed25519.GenerateKey(rand.Reader)
		return &fakeKey{alg, priv, []kv{{1, 1}, {3, AlgEdDSA}, {-1, 6}, {-2, []byte(pub)}}}
	default:
		k, err := rsa.GenerateKey(rand.Reader, 2048)
		if err != nil {
			t.Fatal(err)
		}
		return &fakeKey{alg, k, []kv{{1, 3}, {3, AlgRS256}, {-1, k.N.Bytes()}, {-2, big.NewInt(int64(k.E)).Bytes()}}}
	}
}

func (k *fakeKey) sign(msg []byte) []byte {
	if k.alg == AlgEdDSA {
		s, _ := k.priv.Sign(rand.Reader, msg, crypto.Hash(0))
		return s
	}
	d := sha256.Sum256(msg)
	s, _ := k.priv.Sign(rand.Reader, d[:], crypto.SHA256)
	return s
}

func clientJSON(typ string, challenge []byte, origin string, cross bool) []byte {
	b, _ := json.Marshal(map[string]any{"type": typ, "challenge": Encode(challenge), "origin": origin, "crossOrigin": cross})
	return b
}

func authBytes(rpID string, flags byte, count uint32, credID []byte, key *fakeKey) []byte {
	h := sha256.Sum256([]byte(rpID))
	out := append(h[:], flags, 0, 0, 0, 0)
	binary.BigEndian.PutUint32(out[33:], count)
	if credID != nil {
		out = append(out, make([]byte, 16)...)
		out = binary.BigEndian.AppendUint16(out, uint16(len(credID)))
		out = append(out, credID...)
		out = append(out, cb(key.cose)...)
	}
	return out
}

const (
	rp     = "panel.example.com"
	origin = "https://panel.example.com:2083"
)

func expect(ch []byte) Expect {
	return Expect{Challenge: ch, RPID: rp, Origins: []string{origin}, RequireUV: true}
}

func register(t *testing.T, alg int64) (*fakeKey, *Credential) {
	t.Helper()
	k := newKey(t, alg)
	ch := NewChallenge()
	id := []byte("credential-" + string(rune('a'+alg+300)))
	att := cb([]kv{{"fmt", "none"}, {"attStmt", []kv{}}, {"authData", authBytes(rp, flagUP|flagUV|flagAT|flagBE, 0, id, k)}})
	c, err := VerifyRegistration(expect(ch), clientJSON("webauthn.create", ch, origin, false), att)
	if err != nil {
		t.Fatalf("alg %d: %v", alg, err)
	}
	if !SameID(c.ID, id) || c.Alg != alg || !c.BackupEligible {
		t.Fatalf("credential = %+v", c)
	}
	return k, c
}

func assertion(k *fakeKey, ch []byte, rpID, org string, flags byte, count uint32) (cd, ad, sig []byte) {
	cd = clientJSON("webauthn.get", ch, org, false)
	ad = authBytes(rpID, flags, count, nil, nil)
	h := sha256.Sum256(cd)
	sig = k.sign(append(append([]byte{}, ad...), h[:]...))
	return
}

func TestRegisterAndSignIn(t *testing.T) {
	for _, alg := range Algorithms {
		k, c := register(t, alg)
		ch := NewChallenge()
		cd, ad, sig := assertion(k, ch, rp, origin, flagUP|flagUV, 5)
		n, err := VerifyAssertion(expect(ch), c, cd, ad, sig)
		if err != nil || n != 5 {
			t.Fatalf("alg %d: sign-in = %d, %v", alg, n, err)
		}
		c.SignCount = n
		// The counter must go up.
		ch = NewChallenge()
		cd, ad, sig = assertion(k, ch, rp, origin, flagUP|flagUV, 5)
		if _, err := VerifyAssertion(expect(ch), c, cd, ad, sig); err != ErrCloned {
			t.Errorf("alg %d: repeated counter: %v", alg, err)
		}
	}
}

func TestZeroCounterAuthenticators(t *testing.T) {
	// Synced passkeys (Apple, Google) always send 0.
	k, c := register(t, AlgES256)
	for i := 0; i < 2; i++ {
		ch := NewChallenge()
		cd, ad, sig := assertion(k, ch, rp, origin, flagUP|flagUV, 0)
		if _, err := VerifyAssertion(expect(ch), c, cd, ad, sig); err != nil {
			t.Fatal(err)
		}
	}
}

func TestAssertionRefusals(t *testing.T) {
	k, c := register(t, AlgES256)
	other := newKey(t, AlgES256)
	ch := NewChallenge()
	cases := map[string]func() (Expect, []byte, []byte, []byte){
		"wrong origin": func() (Expect, []byte, []byte, []byte) {
			cd, ad, sig := assertion(k, ch, rp, "https://evil.example", flagUP|flagUV, 1)
			return expect(ch), cd, ad, sig
		},
		"wrong site": func() (Expect, []byte, []byte, []byte) {
			cd, ad, sig := assertion(k, ch, "evil.example", origin, flagUP|flagUV, 1)
			return expect(ch), cd, ad, sig
		},
		"old challenge": func() (Expect, []byte, []byte, []byte) {
			cd, ad, sig := assertion(k, ch, rp, origin, flagUP|flagUV, 1)
			return expect(NewChallenge()), cd, ad, sig
		},
		"no challenge": func() (Expect, []byte, []byte, []byte) {
			cd, ad, sig := assertion(k, ch, rp, origin, flagUP|flagUV, 1)
			return expect(nil), cd, ad, sig
		},
		"not verified": func() (Expect, []byte, []byte, []byte) {
			cd, ad, sig := assertion(k, ch, rp, origin, flagUP, 1)
			return expect(ch), cd, ad, sig
		},
		"not present": func() (Expect, []byte, []byte, []byte) {
			cd, ad, sig := assertion(k, ch, rp, origin, flagUV, 1)
			return expect(ch), cd, ad, sig
		},
		"another key": func() (Expect, []byte, []byte, []byte) {
			cd, ad, sig := assertion(other, ch, rp, origin, flagUP|flagUV, 1)
			return expect(ch), cd, ad, sig
		},
		"tampered data": func() (Expect, []byte, []byte, []byte) {
			cd, ad, sig := assertion(k, ch, rp, origin, flagUP|flagUV, 1)
			ad[36] = 9
			return expect(ch), cd, ad, sig
		},
		"registration data": func() (Expect, []byte, []byte, []byte) {
			_, ad, sig := assertion(k, ch, rp, origin, flagUP|flagUV, 1)
			return expect(ch), clientJSON("webauthn.create", ch, origin, false), ad, sig
		},
		"cross origin": func() (Expect, []byte, []byte, []byte) {
			cd := clientJSON("webauthn.get", ch, origin, true)
			ad := authBytes(rp, flagUP|flagUV, 1, nil, nil)
			h := sha256.Sum256(cd)
			return expect(ch), cd, ad, k.sign(append(append([]byte{}, ad...), h[:]...))
		},
		"trailing bytes": func() (Expect, []byte, []byte, []byte) {
			cd := clientJSON("webauthn.get", ch, origin, false)
			ad := append(authBytes(rp, flagUP|flagUV, 1, nil, nil), 0)
			h := sha256.Sum256(cd)
			return expect(ch), cd, ad, k.sign(append(append([]byte{}, ad...), h[:]...))
		},
	}
	for name, f := range cases {
		e, cd, ad, sig := f()
		if _, err := VerifyAssertion(e, c, cd, ad, sig); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	// Without RequireUV, presence alone is enough.
	cd, ad, sig := assertion(k, ch, rp, origin, flagUP, 1)
	e := expect(ch)
	e.RequireUV = false
	if _, err := VerifyAssertion(e, c, cd, ad, sig); err != nil {
		t.Errorf("presence only: %v", err)
	}
}

func TestRegistrationRefusals(t *testing.T) {
	k := newKey(t, AlgES256)
	ch := NewChallenge()
	good := func(flags byte, key []kv) []byte {
		h := sha256.Sum256([]byte(rp))
		ad := append(h[:], flags, 0, 0, 0, 0)
		ad = append(ad, make([]byte, 16)...)
		ad = append(ad, 0, 4, 'a', 'b', 'c', 'd')
		ad = append(ad, cb(key)...)
		return cb([]kv{{"fmt", "none"}, {"attStmt", []kv{}}, {"authData", ad}})
	}
	cd := clientJSON("webauthn.create", ch, origin, false)
	weakRSA, _ := rsa.GenerateKey(rand.Reader, 1024)
	for name, att := range map[string][]byte{
		"no credential":   cb([]kv{{"fmt", "none"}, {"attStmt", []kv{}}, {"authData", authBytes(rp, flagUP|flagUV, 0, nil, nil)}}),
		"not verified":    good(flagUP|flagAT, k.cose),
		"unknown alg":     good(flagUP|flagUV|flagAT, []kv{{1, 2}, {3, -36}, {-1, 1}, {-2, make([]byte, 32)}, {-3, make([]byte, 32)}}),
		"point off curve": good(flagUP|flagUV|flagAT, []kv{{1, 2}, {3, AlgES256}, {-1, 1}, {-2, make([]byte, 32)}, {-3, []byte(strings.Repeat("\x01", 32))}}),
		"weak RSA":        good(flagUP|flagUV|flagAT, []kv{{1, 3}, {3, AlgRS256}, {-1, weakRSA.N.Bytes()}, {-2, []byte{1, 0, 1}}}),
		"backed up only":  good(flagUP|flagUV|flagAT|flagBS, k.cose),
		"not a map":       cb([]any{1, 2}),
		"no format":       cb([]kv{{"authData", []byte{1}}}),
		"trailing":        append(good(flagUP|flagUV|flagAT, k.cose), 0),
	} {
		if _, err := VerifyRegistration(expect(ch), cd, att); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if _, err := VerifyRegistration(expect(ch), clientJSON("webauthn.get", ch, origin, false), good(flagUP|flagUV|flagAT, k.cose)); err == nil {
		t.Error("sign-in client data accepted for registration")
	}
}

// Truncated or corrupted input never panics.
func TestCBORRobust(t *testing.T) {
	k := newKey(t, AlgES256)
	att := cb([]kv{{"fmt", "none"}, {"attStmt", []kv{}}, {"authData", authBytes(rp, flagUP|flagUV|flagAT, 0, []byte("id"), k)}})
	for i := 0; i < len(att); i++ {
		_, _, _ = decodeCBOR(att[:i])
		b := append([]byte{}, att...)
		b[i] ^= 0xff
		_, _, _ = decodeCBOR(b)
		_, _ = VerifyRegistration(expect(nil), nil, b)
	}
	deep := []byte{}
	for i := 0; i < 40; i++ {
		deep = append(deep, 0x81)
	}
	if _, _, err := decodeCBOR(append(deep, 0)); err == nil {
		t.Error("deep nesting accepted")
	}
	if _, _, err := decodeCBOR([]byte{0x5a, 0xff, 0xff, 0xff, 0xff}); err == nil {
		t.Error("huge length accepted")
	}
	if v, _, err := decodeCBOR([]byte{0xf9, 0x3c, 0x00}); err != nil || v != 1.0 {
		t.Errorf("half float = %v %v", v, err)
	}
}
