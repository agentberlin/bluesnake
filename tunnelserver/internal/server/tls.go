package server

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"math/big"
	"net"
	"time"

	"github.com/agentberlin/bluesnake/internal/tunnel/wire"
)

// ApplyALPN ensures a TLS config advertises the tunnel ALPN first, then
// http/1.1 for ordinary HTTPS. Tunnel clients offer only the tunnel ALPN and
// browsers/HTTP clients offer http/1.1, so the two never collide; we
// deliberately do NOT advertise h2 (MCP works fine over http/1.1, and h2 here
// would require a separate handler path).
func ApplyALPN(cfg *tls.Config) {
	cfg.NextProtos = append([]string{wire.ALPN, "http/1.1"}, cfg.NextProtos...)
}

// DevTLSConfig returns a self-signed TLS config covering the given names (plus
// localhost and loopback IPs), with the tunnel ALPN applied. It exists so the
// dev server and the e2e test exercise the exact same ALPN-muxed code path as
// production — only the certificate source differs. Clients must dial it with
// InsecureSkipVerify.
func DevTLSConfig(names ...string) (*tls.Config, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return nil, err
	}
	tmpl := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: "bluesnake-tunnel-dev"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		DNSNames:              append([]string{"localhost"}, names...),
		IPAddresses:           []net.IP{net.IPv4(127, 0, 0, 1), net.IPv6loopback},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return nil, err
	}
	cert := tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
	cfg := &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12}
	ApplyALPN(cfg)
	return cfg, nil
}
