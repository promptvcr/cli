package doctor

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"testing"
	"time"

	"github.com/promptvcr/cli/internal/ca"
)

func findCheck(checks []Check, name string) (Check, bool) {
	for _, c := range checks {
		if c.Name == name {
			return c, true
		}
	}
	return Check{}, false
}

// writeCert writes a CA cert with the given validity window (plus a placeholder
// key file so the "present" check passes) into dir using the canonical filenames.
func writeCert(t *testing.T, dir string, notBefore, notAfter time.Time) {
	t.Helper()
	certPath, keyPath := ca.Paths(dir)
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("genkey: %v", err)
	}
	tmpl := x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "test"},
		NotBefore:             notBefore,
		NotAfter:              notAfter,
		IsCA:                  true,
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, &tmpl, &tmpl, &priv.PublicKey, priv)
	if err != nil {
		t.Fatalf("createcert: %v", err)
	}
	if err := os.WriteFile(certPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyPath, []byte("placeholder"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestRunFailsWhenCAMissing(t *testing.T) {
	checks := Run(Options{CADir: t.TempDir(), ProxyAddr: "127.0.0.1:8889"})
	c, ok := findCheck(checks, "Root CA present")
	if !ok || c.Status != Fail {
		t.Fatalf("expected Root CA present = FAIL, got %+v", c)
	}
	if OK(checks) {
		t.Fatal("missing CA should make the overall run not OK")
	}
}

func TestRunPassesWithFreshCA(t *testing.T) {
	dir := t.TempDir()
	if err := ca.Ensure(dir); err != nil {
		t.Fatalf("ensure: %v", err)
	}
	t.Setenv("HTTPS_PROXY", "http://127.0.0.1:8889")
	checks := Run(Options{CADir: dir, ProxyAddr: "127.0.0.1:8889"})

	if c, _ := findCheck(checks, "Root CA present"); c.Status != Pass {
		t.Errorf("Root CA present = %v, want PASS", c.Status)
	}
	if c, _ := findCheck(checks, "Root CA valid"); c.Status != Pass {
		t.Errorf("Root CA valid = %v, want PASS", c.Status)
	}
	if !OK(checks) {
		t.Errorf("a fresh CA with proxy set should be OK overall: %+v", checks)
	}
}

func TestExpiredCAFails(t *testing.T) {
	dir := t.TempDir()
	writeCert(t, dir, time.Now().AddDate(-1, 0, 0), time.Now().AddDate(0, 0, -1))
	checks := Run(Options{CADir: dir, ProxyAddr: "127.0.0.1:8889"})
	if c, _ := findCheck(checks, "Root CA valid"); c.Status != Fail {
		t.Fatalf("expired CA should be FAIL, got %+v", c)
	}
	if OK(checks) {
		t.Fatal("expired CA should make the run not OK")
	}
}

func TestNearExpiryWarns(t *testing.T) {
	dir := t.TempDir()
	writeCert(t, dir, time.Now().AddDate(-1, 0, 0), time.Now().Add(10*24*time.Hour))
	checks := Run(Options{CADir: dir, ProxyAddr: "127.0.0.1:8889"})
	if c, _ := findCheck(checks, "Root CA valid"); c.Status != Warn {
		t.Fatalf("near-expiry CA should WARN, got %+v", c)
	}
}

func TestProxyEnvDetected(t *testing.T) {
	t.Setenv("HTTPS_PROXY", "http://127.0.0.1:8889")
	checks := Run(Options{CADir: t.TempDir(), ProxyAddr: "127.0.0.1:8889"})
	if c, _ := findCheck(checks, "HTTPS_PROXY set"); c.Status != Pass {
		t.Fatalf("HTTPS_PROXY set should PASS when env present, got %+v", c)
	}
}

func TestProxyEnvWarnsWhenUnset(t *testing.T) {
	t.Setenv("HTTPS_PROXY", "")
	t.Setenv("https_proxy", "")
	checks := Run(Options{CADir: t.TempDir(), ProxyAddr: "127.0.0.1:8889"})
	c, _ := findCheck(checks, "HTTPS_PROXY set")
	if c.Status != Warn {
		t.Fatalf("HTTPS_PROXY set should WARN when unset, got %+v", c)
	}
	if c.Remedy == "" {
		t.Error("expected a remediation hint when HTTPS_PROXY is unset")
	}
}
