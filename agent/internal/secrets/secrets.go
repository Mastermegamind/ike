// Package secrets encrypts the credentials Ike keeps (DNS provider tokens,
// storage keys, registrar keys, mail relay passwords, server join tokens) with
// AES-256-GCM under a master key that only root can read.
//
// Sealed values look like "ike1:<key id>:<base64(nonce|ciphertext)>". The
// keyring file holds every key still in use; Rotate makes a new key, re-seals
// every registered store with it and then drops the old keys.
package secrets

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/YOUR-ORG/ike/agent/internal/kit"
)

const prefix = "ike1:"

type keyFile struct {
	Current   string            `json:"current"`
	Keys      map[string]string `json:"keys"` // id → hex key
	RotatedAt time.Time         `json:"rotated_at"`
}

// Resealer re-encrypts one store: it opens every sealed value with open and
// writes it back sealed with seal.
type Resealer func(open, seal func(string) (string, error)) error

type Keyring struct {
	path string
	mu   sync.Mutex
	kf   keyFile
	// stores registered by feature packages, re-sealed on rotation
	stores map[string]Resealer
}

// Open loads the keyring, creating a first key when the file is missing.
func Open(path string) (*Keyring, error) {
	k := &Keyring{path: path, stores: map[string]Resealer{}}
	err := kit.ReadJSON(path, &k.kf)
	if errors.Is(err, kit.ErrNotFound) {
		k.kf = keyFile{Keys: map[string]string{}}
		k.addKey()
		k.kf.RotatedAt = kit.Now()
		return k, kit.WriteJSON(path, k.kf)
	}
	if err != nil {
		return nil, fmt.Errorf("master key %s: %w", path, err)
	}
	if _, ok := k.kf.Keys[k.kf.Current]; !ok {
		return nil, fmt.Errorf("master key %s: current key missing", path)
	}
	return k, nil
}

func (k *Keyring) addKey() string {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	id := kit.RandomHex(4)
	k.kf.Keys[id] = hex.EncodeToString(b)
	k.kf.Current = id
	return id
}

// Register adds a store that holds sealed values.
func (k *Keyring) Register(name string, r Resealer) {
	k.mu.Lock()
	k.stores[name] = r
	k.mu.Unlock()
}

func gcm(hexKey string) (cipher.AEAD, error) {
	key, err := hex.DecodeString(hexKey)
	if err != nil || len(key) != 32 {
		return nil, errors.New("bad key")
	}
	b, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(b)
}

// Seal encrypts plain with the current key. Empty stays empty.
func (k *Keyring) Seal(plain string) (string, error) {
	if plain == "" {
		return "", nil
	}
	k.mu.Lock()
	defer k.mu.Unlock()
	return k.sealWith(k.kf.Current, plain)
}

func (k *Keyring) sealWith(id, plain string) (string, error) {
	a, err := gcm(k.kf.Keys[id])
	if err != nil {
		return "", err
	}
	nonce := make([]byte, a.NonceSize())
	_, _ = rand.Read(nonce)
	ct := a.Seal(nonce, nonce, []byte(plain), []byte(id))
	return prefix + id + ":" + base64.RawStdEncoding.EncodeToString(ct), nil
}

// Unseal decrypts a sealed value. A value without the prefix is returned as
// is, so stores written before encryption keep working until the next save.
func (k *Keyring) Unseal(v string) (string, error) {
	if !strings.HasPrefix(v, prefix) {
		return v, nil
	}
	k.mu.Lock()
	defer k.mu.Unlock()
	return k.open(v)
}

func (k *Keyring) open(v string) (string, error) {
	rest := strings.TrimPrefix(v, prefix)
	id, data, ok := strings.Cut(rest, ":")
	if !ok {
		return "", errors.New("sealed value is malformed")
	}
	hk, ok := k.kf.Keys[id]
	if !ok {
		return "", fmt.Errorf("sealed with unknown key %s", id)
	}
	a, err := gcm(hk)
	if err != nil {
		return "", err
	}
	ct, err := base64.RawStdEncoding.DecodeString(data)
	if err != nil || len(ct) < a.NonceSize() {
		return "", errors.New("sealed value is malformed")
	}
	pt, err := a.Open(nil, ct[:a.NonceSize()], ct[a.NonceSize():], []byte(id))
	if err != nil {
		return "", errors.New("sealed value could not be decrypted")
	}
	return string(pt), nil
}

// Status is shown on the security page.
type Status struct {
	KeyID     string    `json:"key_id"`
	Keys      int       `json:"keys"`
	Stores    []string  `json:"stores"`
	RotatedAt time.Time `json:"rotated_at"`
}

func (k *Keyring) Status() Status {
	k.mu.Lock()
	defer k.mu.Unlock()
	st := Status{KeyID: k.kf.Current, Keys: len(k.kf.Keys), RotatedAt: k.kf.RotatedAt, Stores: []string{}}
	for n := range k.stores {
		st.Stores = append(st.Stores, n)
	}
	return st
}

// Rotate makes a new master key and re-seals every registered store. The new
// key is saved before any store is rewritten, so a crash halfway leaves values
// readable under either key. Old keys are dropped only when every store
// succeeded.
func (k *Keyring) Rotate() (Status, error) {
	k.mu.Lock()
	old := k.kf.Current
	id := k.addKey()
	if err := kit.WriteJSON(k.path, k.kf); err != nil {
		delete(k.kf.Keys, id)
		k.kf.Current = old
		k.mu.Unlock()
		return Status{}, err
	}
	stores := map[string]Resealer{}
	for n, r := range k.stores {
		stores[n] = r
	}
	k.mu.Unlock()

	open := func(v string) (string, error) {
		if !strings.HasPrefix(v, prefix) {
			return v, nil
		}
		k.mu.Lock()
		defer k.mu.Unlock()
		return k.open(v)
	}
	seal := func(v string) (string, error) {
		if v == "" {
			return "", nil
		}
		k.mu.Lock()
		defer k.mu.Unlock()
		return k.sealWith(id, v)
	}
	var errs []error
	for n, r := range stores {
		if err := r(open, seal); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", n, err))
		}
	}
	k.mu.Lock()
	if len(errs) == 0 {
		for kid := range k.kf.Keys {
			if kid != id {
				delete(k.kf.Keys, kid)
			}
		}
	}
	k.kf.RotatedAt = kit.Now()
	err := kit.WriteJSON(k.path, k.kf)
	k.mu.Unlock()
	if len(errs) > 0 {
		return k.Status(), fmt.Errorf("new key saved but some stores still use the old one: %w", errors.Join(errs...))
	}
	return k.Status(), err
}

// Mask shows the last four characters of a secret, for display.
func Mask(s string) string {
	if s == "" {
		return ""
	}
	if len(s) <= 6 {
		return "••••"
	}
	return "••••" + s[len(s)-4:]
}
