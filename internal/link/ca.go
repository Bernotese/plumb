package link

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"slices"
	"time"
)

// File names in the link directory. Key files have mode 0600.
const (
	caCertFile     = "ca.crt"
	caKeyFile      = "ca.key"
	serverCertFile = "agent-link.crt"
	serverKeyFile  = "agent-link.key"
)

const (
	caValidity     = 10 * 365 * 24 * time.Hour
	serverValidity = 365 * 24 * time.Hour
	renewBefore    = 30 * 24 * time.Hour
)

// CA is the Plumb CA. Agents pin its certificate (ARCHITECTURE §5).
type CA struct {
	Cert    *x509.Certificate
	CertPEM []byte
	key     *ecdsa.PrivateKey
}

// loadOrCreateCA reads the CA from dir, or creates it at first start.
func loadOrCreateCA(dir string, now time.Time) (*CA, error) {
	_, certErr := os.Stat(filepath.Join(dir, caCertFile))
	_, keyErr := os.Stat(filepath.Join(dir, caKeyFile))
	switch {
	case errors.Is(certErr, os.ErrNotExist) && errors.Is(keyErr, os.ErrNotExist):
		return createCA(dir, now) // first start
	case errors.Is(certErr, os.ErrNotExist) || errors.Is(keyErr, os.ErrNotExist):
		// A new CA would lock out every enrolled agent without a word.
		return nil, fmt.Errorf("link: only one of %s and %s exists in %s; restore the missing file (a new CA needs re-enrollment of every agent)",
			caCertFile, caKeyFile, dir)
	}
	certPEM, key, err := readPair(dir, caCertFile, caKeyFile)
	if err != nil {
		return nil, err
	}
	cert, err := parseCert(certPEM)
	if err != nil {
		return nil, err
	}
	if !cert.IsCA || !publicKeyMatches(cert, key) {
		return nil, errors.New("link: ca.crt and ca.key do not form a CA")
	}
	return &CA{Cert: cert, CertPEM: certPEM, key: key}, nil
}

func createCA(dir string, now time.Time) (*CA, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	tmpl := &x509.Certificate{
		SerialNumber:          serial(),
		Subject:               pkix.Name{CommonName: "Plumb CA", Organization: []string{"Plumb"}},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.Add(caValidity),
		IsCA:                  true,
		BasicConstraintsValid: true,
		MaxPathLenZero:        true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return nil, err
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	if err := writePair(dir, caCertFile, caKeyFile, certPEM, key); err != nil {
		return nil, err
	}
	cert, _ := x509.ParseCertificate(der)
	return &CA{Cert: cert, CertPEM: certPEM, key: key}, nil
}

// serverCert reads the listener certificate from dir. It issues a new one
// when there is none, when it expires within 30 days, when it is not from
// this CA, or when the host names changed.
func (ca *CA) serverCert(dir string, hosts []string, now time.Time) (tls.Certificate, error) {
	certPEM, key, err := readPair(dir, serverCertFile, serverKeyFile)
	if err == nil {
		cert, perr := parseCert(certPEM)
		if perr == nil && now.Add(renewBefore).Before(cert.NotAfter) &&
			cert.CheckSignatureFrom(ca.Cert) == nil && publicKeyMatches(cert, key) &&
			slices.Equal(certHosts(cert), sortedHosts(hosts)) {
			return tlsPair(certPEM, key, ca)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return tls.Certificate{}, err
	}
	certPEM, key, err = ca.Issue(hosts, now, serverValidity)
	if err != nil {
		return tls.Certificate{}, err
	}
	if err := writePair(dir, serverCertFile, serverKeyFile, certPEM, key); err != nil {
		return tls.Certificate{}, err
	}
	return tlsPair(certPEM, key, ca)
}

// Issue makes a server certificate for the host names and IP addresses.
// The API listener (port 8443, D-040) uses it too.
func (ca *CA) Issue(hosts []string, now time.Time, validity time.Duration) ([]byte, *ecdsa.PrivateKey, error) {
	if len(hosts) == 0 {
		return nil, nil, errors.New("link: a server certificate needs at least one host name or IP")
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, err
	}
	tmpl := &x509.Certificate{
		SerialNumber: serial(),
		Subject:      pkix.Name{CommonName: hosts[0], Organization: []string{"Plumb"}},
		NotBefore:    now.Add(-time.Hour),
		NotAfter:     now.Add(validity),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	for _, h := range hosts {
		if ip := net.ParseIP(h); ip != nil {
			tmpl.IPAddresses = append(tmpl.IPAddresses, ip)
		} else {
			tmpl.DNSNames = append(tmpl.DNSNames, h)
		}
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, ca.Cert, &key.PublicKey, ca.key)
	if err != nil {
		return nil, nil, err
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), key, nil
}

// Pool returns a cert pool with only the Plumb CA, as an agent pins it.
func (ca *CA) Pool() *x509.CertPool {
	p := x509.NewCertPool()
	p.AddCert(ca.Cert)
	return p
}

func tlsPair(certPEM []byte, key *ecdsa.PrivateKey, ca *CA) (tls.Certificate, error) {
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return tls.Certificate{}, err
	}
	chain := append(append([]byte(nil), certPEM...), ca.CertPEM...)
	return tls.X509KeyPair(chain, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}))
}

func certHosts(c *x509.Certificate) []string {
	var hosts []string
	hosts = append(hosts, c.DNSNames...)
	for _, ip := range c.IPAddresses {
		hosts = append(hosts, ip.String())
	}
	slices.Sort(hosts)
	return hosts
}

func sortedHosts(hosts []string) []string {
	out := make([]string, 0, len(hosts))
	for _, h := range hosts {
		if ip := net.ParseIP(h); ip != nil {
			h = ip.String()
		}
		out = append(out, h)
	}
	slices.Sort(out)
	return slices.Compact(out)
}

func publicKeyMatches(c *x509.Certificate, key *ecdsa.PrivateKey) bool {
	pub, ok := c.PublicKey.(*ecdsa.PublicKey)
	return ok && pub.Equal(&key.PublicKey)
}

func serial() *big.Int {
	n, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 127))
	if err != nil {
		panic(err)
	}
	return n
}

func parseCert(certPEM []byte) (*x509.Certificate, error) {
	block, _ := pem.Decode(certPEM)
	if block == nil || block.Type != "CERTIFICATE" {
		return nil, errors.New("link: no certificate in PEM")
	}
	return x509.ParseCertificate(block.Bytes)
}

// readPair reads a certificate and its key. A key file that others can
// read is an error: it would mean someone changed the mode.
func readPair(dir, certName, keyName string) ([]byte, *ecdsa.PrivateKey, error) {
	certPEM, err := os.ReadFile(filepath.Join(dir, certName))
	if err != nil {
		return nil, nil, err
	}
	keyPath := filepath.Join(dir, keyName)
	info, err := os.Stat(keyPath)
	if err != nil {
		return nil, nil, err
	}
	if info.Mode().Perm()&0o077 != 0 {
		return nil, nil, fmt.Errorf("link: %s has mode %v; it must be 0600", keyPath, info.Mode().Perm())
	}
	keyPEM, err := os.ReadFile(keyPath)
	if err != nil {
		return nil, nil, err
	}
	block, _ := pem.Decode(keyPEM)
	if block == nil {
		return nil, nil, fmt.Errorf("link: no key in %s", keyPath)
	}
	key, err := x509.ParseECPrivateKey(block.Bytes)
	if err != nil {
		return nil, nil, fmt.Errorf("link: %s: %w", keyPath, err)
	}
	return certPEM, key, nil
}

// writePair writes the key (0600) and the certificate (0644). Each file
// is written to a temp file and renamed, so a crash leaves no half file.
func writePair(dir, certName, keyName string, certPEM []byte, key *ecdsa.PrivateKey) error {
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return err
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	if err := writeFile(filepath.Join(dir, keyName), keyPEM, 0o600); err != nil {
		return err
	}
	return writeFile(filepath.Join(dir, certName), certPEM, 0o644)
}

func writeFile(path string, data []byte, mode os.FileMode) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
