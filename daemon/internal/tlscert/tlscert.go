// Package tlscert loads the certificate Marina serves HTTPS with.
//
// A dashboard on plain HTTP gets "Not Secure" in the address bar, which is both
// noise and a lie about the risk — but the fix is not a self-signed certificate,
// because that trades one warning for a worse one. What works is a certificate
// signed by a CA the machine already trusts, which on a developer's Mac usually
// means mkcert: it installs its own root into the system keychain, so anything it
// signs is trusted by every browser on that machine with no warning at all.
//
// The certificate is minted by the installer rather than here — mkcert is a
// user-level tool and generating it once at install time is simpler than teaching
// the daemon to shell out. This package only finds and watches the result.
//
// Absent files are not an error. Marina serves plain HTTP and says so; nobody
// should be unable to see their dev servers because a certificate is missing.
package tlscert

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// ErrAbsent means no certificate is installed, so HTTPS is simply not offered.
var ErrAbsent = errors.New("tlscert: no certificate installed")

// Paths returns where the certificate and key live for a given state directory.
func Paths(stateDir string) (certPath, keyPath string) {
	dir := filepath.Join(stateDir, "tls")
	return filepath.Join(dir, "cert.pem"), filepath.Join(dir, "key.pem")
}

// Keeper holds the certificate and reloads it when the files change.
//
// Reloading matters because the certificate names include the machine's Bonjour
// name, and re-running the installer after that changes writes a new pair. A
// daemon that read the file once at startup would keep serving a certificate for
// a name it no longer answers to until someone thought to restart it.
type Keeper struct {
	certPath, keyPath string

	mu      sync.RWMutex
	cert    *tls.Certificate
	modTime time.Time
}

// Load reads the certificate, returning ErrAbsent when there is none.
func Load(stateDir string) (*Keeper, error) {
	certPath, keyPath := Paths(stateDir)
	k := &Keeper{certPath: certPath, keyPath: keyPath}
	if err := k.reload(); err != nil {
		return nil, err
	}
	return k, nil
}

func (k *Keeper) reload() error {
	info, err := os.Stat(k.certPath)
	if err != nil {
		if os.IsNotExist(err) {
			return ErrAbsent
		}
		return err
	}
	if _, err := os.Stat(k.keyPath); err != nil {
		// A certificate without its key is worse than neither: it looks configured
		// and cannot serve.
		return ErrAbsent
	}

	pair, err := tls.LoadX509KeyPair(k.certPath, k.keyPath)
	if err != nil {
		return err
	}

	k.mu.Lock()
	k.cert, k.modTime = &pair, info.ModTime()
	k.mu.Unlock()
	return nil
}

// Config returns a TLS config that picks up a replaced certificate without a
// restart. GetCertificate is consulted per handshake, which is the hook for that.
func (k *Keeper) Config() *tls.Config {
	return &tls.Config{
		MinVersion: tls.VersionTLS12,
		GetCertificate: func(*tls.ClientHelloInfo) (*tls.Certificate, error) {
			k.mu.RLock()
			cert, seen := k.cert, k.modTime
			k.mu.RUnlock()

			// Cheap enough per handshake on a local dashboard, and it means a
			// reinstall takes effect on the next page load.
			if info, err := os.Stat(k.certPath); err == nil && info.ModTime().After(seen) {
				if err := k.reload(); err == nil {
					k.mu.RLock()
					cert = k.cert
					k.mu.RUnlock()
				}
			}
			if cert == nil {
				return nil, ErrAbsent
			}
			return cert, nil
		},
	}
}

// Names lists the hostnames the certificate is valid for, so the daemon can log
// what will and will not get a padlock.
func (k *Keeper) Names() []string {
	k.mu.RLock()
	defer k.mu.RUnlock()
	if k.cert == nil || k.cert.Leaf == nil {
		if k.cert == nil {
			return nil
		}
		// Leaf is only populated when parsed; do it once so this stays cheap.
		if leaf, err := parseLeaf(k.cert); err == nil {
			k.cert.Leaf = leaf
		} else {
			return nil
		}
	}
	out := append([]string{}, k.cert.Leaf.DNSNames...)
	for _, ip := range k.cert.Leaf.IPAddresses {
		out = append(out, ip.String())
	}
	return out
}

// parseLeaf decodes the leaf certificate so its names can be read.
func parseLeaf(cert *tls.Certificate) (*x509.Certificate, error) {
	if len(cert.Certificate) == 0 {
		return nil, ErrAbsent
	}
	return x509.ParseCertificate(cert.Certificate[0])
}
