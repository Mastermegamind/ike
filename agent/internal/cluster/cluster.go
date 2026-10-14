// Package cluster connects several Ike servers to one panel. The panel keeps
// a private certificate authority; each joined server's agent gets a
// certificate from it and listens on TCP port 2089 for the panel only
// (mutual TLS: both sides prove who they are with a certificate from the same
// authority).
//
// Joining:
//
//  1. The admin adds a server in the panel and gets a one-time join token
//     plus the panel certificate's SHA-256 fingerprint.
//  2. On the new server: ike-agent join https://panel.example.com:2087 <token> <fingerprint>
//     makes a key, sends a certificate request with the token, and gets back
//     its certificate and the authority's certificate.
//  3. The agent restarts with cluster_listen ":2089" and accepts only the panel.
package cluster

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Names inside certificates.
const (
	PanelCN  = "ike-panel"
	ServerCN = "ike-server:" // + server ID
)

// CA is the panel's certificate authority.
type CA struct {
	Cert *x509.Certificate
	Key  crypto.Signer
	PEM  []byte
}

func newKey() (*ecdsa.PrivateKey, error) { return ecdsa.GenerateKey(elliptic.P256(), rand.Reader) }

func serial() *big.Int {
	n, _ := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 126))
	return n
}

func keyPEM(k *ecdsa.PrivateKey) []byte {
	der, _ := x509.MarshalPKCS8PrivateKey(k)
	return pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})
}

// LoadOrCreateCA reads the authority from dir, making it on first use.
func LoadOrCreateCA(dir string) (*CA, error) {
	certPath, keyPath := filepath.Join(dir, "ca.crt"), filepath.Join(dir, "ca.key")
	if cb, err := os.ReadFile(certPath); err == nil {
		kb, err := os.ReadFile(keyPath)
		if err != nil {
			return nil, err
		}
		return parseCA(cb, kb)
	}
	k, err := newKey()
	if err != nil {
		return nil, err
	}
	tmpl := &x509.Certificate{
		SerialNumber: serial(), Subject: pkix.Name{CommonName: "Ike cluster authority", Organization: []string{"Ike"}},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().AddDate(20, 0, 0),
		IsCA: true, BasicConstraintsValid: true, MaxPathLenZero: true,
		KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &k.PublicKey, k)
	if err != nil {
		return nil, err
	}
	cb := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	if err := os.WriteFile(keyPath, keyPEM(k), 0o600); err != nil {
		return nil, err
	}
	if err := os.WriteFile(certPath, cb, 0o644); err != nil {
		return nil, err
	}
	return parseCA(cb, keyPEM(k))
}

func parseCA(cb, kb []byte) (*CA, error) {
	b, _ := pem.Decode(cb)
	if b == nil {
		return nil, errors.New("CA certificate unreadable")
	}
	c, err := x509.ParseCertificate(b.Bytes)
	if err != nil {
		return nil, err
	}
	kblk, _ := pem.Decode(kb)
	if kblk == nil {
		return nil, errors.New("CA key unreadable")
	}
	k, err := x509.ParsePKCS8PrivateKey(kblk.Bytes)
	if err != nil {
		return nil, err
	}
	s, ok := k.(crypto.Signer)
	if !ok {
		return nil, errors.New("CA key can't sign")
	}
	return &CA{Cert: c, Key: s, PEM: cb}, nil
}

// Sign issues a certificate for a CSR. client: usable as a TLS client;
// server: usable as a TLS server for the given names/addresses.
func (ca *CA) Sign(csrPEM []byte, cn string, hosts []string, client, server bool, days int) ([]byte, error) {
	b, _ := pem.Decode(csrPEM)
	if b == nil || b.Type != "CERTIFICATE REQUEST" {
		return nil, errors.New("certificate request unreadable")
	}
	csr, err := x509.ParseCertificateRequest(b.Bytes)
	if err != nil {
		return nil, err
	}
	if err := csr.CheckSignature(); err != nil {
		return nil, err
	}
	tmpl := &x509.Certificate{
		SerialNumber: serial(), Subject: pkix.Name{CommonName: cn, Organization: []string{"Ike"}},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().AddDate(0, 0, days),
		KeyUsage: x509.KeyUsageDigitalSignature,
	}
	if client {
		tmpl.ExtKeyUsage = append(tmpl.ExtKeyUsage, x509.ExtKeyUsageClientAuth)
	}
	if server {
		tmpl.ExtKeyUsage = append(tmpl.ExtKeyUsage, x509.ExtKeyUsageServerAuth)
		for _, h := range hosts {
			if ip := net.ParseIP(h); ip != nil {
				tmpl.IPAddresses = append(tmpl.IPAddresses, ip)
			} else if h != "" {
				tmpl.DNSNames = append(tmpl.DNSNames, h)
			}
		}
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, ca.Cert, csr.PublicKey, ca.Key)
	if err != nil {
		return nil, err
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), nil
}

// NewCSR makes a key and a certificate request.
func NewCSR(cn string) (keyPEMBytes, csrPEM []byte, err error) {
	k, err := newKey()
	if err != nil {
		return nil, nil, err
	}
	der, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{Subject: pkix.Name{CommonName: cn}}, k)
	if err != nil {
		return nil, nil, err
	}
	return keyPEM(k), pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: der}), nil
}

// IssueLocal makes a key and certificate in one go (the panel's own client
// certificate).
func (ca *CA) IssueLocal(cn string, hosts []string, client, server bool) (certPEM, keyPEMBytes []byte, err error) {
	kp, csr, err := NewCSR(cn)
	if err != nil {
		return nil, nil, err
	}
	cp, err := ca.Sign(csr, cn, hosts, client, server, 3650)
	return cp, kp, err
}

// Fingerprint is the SHA-256 of a certificate, as hex with colons.
func Fingerprint(der []byte) string {
	s := sha256.Sum256(der)
	h := strings.ToUpper(hex.EncodeToString(s[:]))
	var parts []string
	for i := 0; i < len(h); i += 2 {
		parts = append(parts, h[i:i+2])
	}
	return strings.Join(parts, ":")
}

// NormalizeFingerprint accepts hex with or without colons.
func NormalizeFingerprint(f string) string {
	f = strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(f), ":", ""))
	var parts []string
	for i := 0; i+1 < len(f); i += 2 {
		parts = append(parts, f[i:i+2])
	}
	return strings.Join(parts, ":")
}

// ServerTLS is the agent's listener config: only certificates from the
// authority with the panel's name are accepted.
func ServerTLS(dir string) (*tls.Config, error) {
	cert, err := tls.LoadX509KeyPair(filepath.Join(dir, "agent.crt"), filepath.Join(dir, "agent.key"))
	if err != nil {
		return nil, err
	}
	caPEM, err := os.ReadFile(filepath.Join(dir, "ca.crt"))
	if err != nil {
		return nil, err
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(caPEM) {
		return nil, errors.New("cluster authority certificate unreadable")
	}
	return &tls.Config{
		MinVersion: tls.VersionTLS13, Certificates: []tls.Certificate{cert},
		ClientAuth: tls.RequireAndVerifyClientCert, ClientCAs: pool,
		VerifyPeerCertificate: func(_ [][]byte, chains [][]*x509.Certificate) error {
			if len(chains) == 0 || chains[0][0].Subject.CommonName != PanelCN {
				return errors.New("only the panel may connect")
			}
			return nil
		},
	}, nil
}

// ClientTLS is the panel's config for talking to one server: it presents the
// panel certificate and accepts only that server's certificate.
func ClientTLS(ca *CA, panelCert tls.Certificate, serverID string) *tls.Config {
	pool := x509.NewCertPool()
	pool.AddCert(ca.Cert)
	return &tls.Config{
		MinVersion: tls.VersionTLS13, Certificates: []tls.Certificate{panelCert}, RootCAs: pool,
		InsecureSkipVerify: true, // names are checked below against the CA and the server ID
		VerifyPeerCertificate: func(raw [][]byte, _ [][]*x509.Certificate) error {
			if len(raw) == 0 {
				return errors.New("no certificate")
			}
			c, err := x509.ParseCertificate(raw[0])
			if err != nil {
				return err
			}
			if _, err := c.Verify(x509.VerifyOptions{Roots: pool, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}); err != nil {
				return err
			}
			if c.Subject.CommonName != ServerCN+serverID {
				return fmt.Errorf("connected to %s, expected server %s", c.Subject.CommonName, serverID)
			}
			return nil
		},
	}
}
