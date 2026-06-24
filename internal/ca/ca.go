// Package ca manages the local root CA used to MITM TLS traffic.
//
// `promptvcr init` generates the CA (once) and installs it into the OS trust
// store. goproxy then signs per-host leaf certificates from it on the fly.
package ca

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"time"
)

const (
	certName = "promptvcr-ca.crt"
	keyName  = "promptvcr-ca.key"
)

// Paths returns the cert and key file paths within dir.
func Paths(dir string) (certPath, keyPath string) {
	return filepath.Join(dir, certName), filepath.Join(dir, keyName)
}

// Ensure generates a root CA in dir if one does not already exist.
func Ensure(dir string) error {
	certPath, keyPath := Paths(dir)
	if fileExists(certPath) && fileExists(keyPath) {
		return nil
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}

	priv, err := rsa.GenerateKey(rand.Reader, 3072)
	if err != nil {
		return err
	}
	serial, _ := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	tmpl := x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: "PromptVCR Local Root CA", Organization: []string{"PromptVCR"}},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().AddDate(5, 0, 0),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
		IsCA:                  true,
		MaxPathLenZero:        false,
	}
	der, err := x509.CreateCertificate(rand.Reader, &tmpl, &tmpl, &priv.PublicKey, priv)
	if err != nil {
		return err
	}

	if err := writePEM(certPath, "CERTIFICATE", der, 0o644); err != nil {
		return err
	}
	return writePEM(keyPath, "RSA PRIVATE KEY", x509.MarshalPKCS1PrivateKey(priv), 0o600)
}

// Install adds the CA to the OS trust store. Some runtimes (Node, Python) keep
// their own bundle; Hints() prints the env vars to point them at our CA.
func Install(dir string) error {
	certPath, _ := Paths(dir)
	switch runtime.GOOS {
	case "windows":
		return run("certutil", "-user", "-addstore", "Root", certPath)
	case "darwin":
		return run("security", "add-trusted-cert", "-d", "-r", "trustRoot",
			"-k", filepath.Join(os.Getenv("HOME"), "Library/Keychains/login.keychain-db"), certPath)
	default: // linux
		dest := "/usr/local/share/ca-certificates/promptvcr-ca.crt"
		if err := run("cp", certPath, dest); err != nil {
			return fmt.Errorf("copy CA (try sudo): %w", err)
		}
		return run("update-ca-certificates")
	}
}

// Uninstall removes the CA from the OS trust store.
func Uninstall(dir string) error {
	switch runtime.GOOS {
	case "windows":
		return run("certutil", "-user", "-delstore", "Root", "PromptVCR Local Root CA")
	case "darwin":
		return run("security", "delete-certificate", "-c", "PromptVCR Local Root CA")
	default:
		_ = os.Remove("/usr/local/share/ca-certificates/promptvcr-ca.crt")
		return run("update-ca-certificates", "--fresh")
	}
}

// Hints returns runtime-specific environment variables that point language
// runtimes with their own trust stores at our CA.
func Hints(dir string) map[string]string {
	certPath, _ := Paths(dir)
	return map[string]string{
		"NODE_EXTRA_CA_CERTS": certPath,
		"REQUESTS_CA_BUNDLE":  certPath,
		"SSL_CERT_FILE":       certPath,
		"CURL_CA_BUNDLE":      certPath,
	}
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

func writePEM(path, typ string, der []byte, mode os.FileMode) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	defer f.Close()
	return pem.Encode(f, &pem.Block{Type: typ, Bytes: der})
}

func run(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}
