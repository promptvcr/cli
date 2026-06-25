// Package doctor diagnoses a PromptVCR setup: the local root CA, OS/runtime trust,
// and proxy environment. It exists because the real onboarding cliff is TLS trust,
// not code changes — `promptvcr doctor` turns that into an actionable checklist.
package doctor

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/promptvcr/cli/internal/ca"
	"github.com/promptvcr/cli/internal/config"
	"github.com/promptvcr/cli/internal/proxy"
	"github.com/promptvcr/cli/internal/sse"
	"github.com/promptvcr/cli/internal/store"
)

// Status is the outcome of a single check.
type Status int

const (
	Pass Status = iota
	Warn
	Fail
)

func (s Status) String() string {
	switch s {
	case Pass:
		return "PASS"
	case Warn:
		return "WARN"
	case Fail:
		return "FAIL"
	default:
		return "????"
	}
}

// Check is one diagnostic result with optional remediation guidance.
type Check struct {
	Name   string
	Status Status
	Detail string
	Remedy string
}

// Options configures a doctor run.
type Options struct {
	CADir     string
	ProxyAddr string
	// Verify performs a live TLS-MITM handshake against a provider host to prove
	// the OS trust store actually trusts the CA. Requires network access.
	Verify bool
	// VerifyHost is the provider host used for the live handshake (default api.openai.com).
	VerifyHost string
}

// Run executes the offline checks (and, if opts.Verify, the live handshake) and
// returns the results in display order.
func Run(opts Options) []Check {
	if opts.VerifyHost == "" {
		opts.VerifyHost = "api.openai.com"
	}
	var checks []Check
	certPath, keyPath := ca.Paths(opts.CADir)
	caExists := fileExists(certPath) && fileExists(keyPath)

	// 1. CA files present.
	if caExists {
		checks = append(checks, Check{Name: "Root CA present", Status: Pass, Detail: opts.CADir})
	} else {
		checks = append(checks, Check{
			Name: "Root CA present", Status: Fail,
			Detail: fmt.Sprintf("not found in %s", opts.CADir),
			Remedy: "run `promptvcr init`",
		})
	}

	// 2. CA validity window.
	if caExists {
		checks = append(checks, caValidityCheck(opts.CADir))
	}

	// 3. Proxy environment.
	if p := firstNonEmpty(os.Getenv("HTTPS_PROXY"), os.Getenv("https_proxy")); p != "" {
		checks = append(checks, Check{Name: "HTTPS_PROXY set", Status: Pass, Detail: p})
	} else {
		checks = append(checks, Check{
			Name: "HTTPS_PROXY set", Status: Warn, Detail: "unset",
			Remedy: fmt.Sprintf("export HTTPS_PROXY=http://%s", opts.ProxyAddr),
		})
	}

	// 4. Runtime trust bundles (only for runtimes that are actually installed).
	if caExists {
		checks = append(checks, runtimeTrustChecks(certPath)...)
	}

	// 5. Live OS-trust verification (opt-in).
	if opts.Verify && caExists {
		checks = append(checks, verifyTrust(opts.CADir, opts.VerifyHost))
	}

	return checks
}

func caValidityCheck(caDir string) Check {
	const name = "Root CA valid"
	cert, err := ca.Load(caDir)
	if err != nil {
		return Check{Name: name, Status: Fail, Detail: err.Error(), Remedy: "run `promptvcr init` to regenerate"}
	}
	now := time.Now()
	switch {
	case now.Before(cert.NotBefore):
		return Check{Name: name, Status: Warn, Detail: "not valid until " + cert.NotBefore.Format("2006-01-02")}
	case now.After(cert.NotAfter):
		return Check{Name: name, Status: Fail, Detail: "expired " + cert.NotAfter.Format("2006-01-02"),
			Remedy: "run `promptvcr init` to regenerate, then re-trust it"}
	case now.Add(30 * 24 * time.Hour).After(cert.NotAfter):
		return Check{Name: name, Status: Warn, Detail: "expires soon (" + cert.NotAfter.Format("2006-01-02") + ")"}
	default:
		return Check{Name: name, Status: Pass, Detail: "expires " + cert.NotAfter.Format("2006-01-02")}
	}
}

func runtimeTrustChecks(certPath string) []Check {
	var checks []Check
	seenEnv := map[string]bool{}
	for _, rt := range []struct{ bin, env string }{
		{"node", "NODE_EXTRA_CA_CERTS"},
		{"python3", "REQUESTS_CA_BUNDLE"},
		{"python", "REQUESTS_CA_BUNDLE"},
		{"curl", "CURL_CA_BUNDLE"},
	} {
		if seenEnv[rt.env] {
			continue
		}
		if _, err := exec.LookPath(rt.bin); err != nil {
			continue // runtime not installed; nothing to nag about
		}
		seenEnv[rt.env] = true
		name := fmt.Sprintf("%s trust (%s)", rt.bin, rt.env)
		val := os.Getenv(rt.env)
		switch {
		case val == "":
			checks = append(checks, Check{Name: name, Status: Warn, Detail: "unset",
				Remedy: fmt.Sprintf("export %s=%s", rt.env, certPath)})
		case !samePath(val, certPath):
			checks = append(checks, Check{Name: name, Status: Warn, Detail: "points elsewhere: " + val,
				Remedy: fmt.Sprintf("export %s=%s", rt.env, certPath)})
		default:
			checks = append(checks, Check{Name: name, Status: Pass, Detail: val})
		}
	}
	return checks
}

// verifyTrust starts an in-process proxy and makes an HTTPS request to host
// through it using the system cert pool. A successful TLS handshake proves the OS
// trust store trusts our CA. No API key is needed and the body is never read.
func verifyTrust(caDir, host string) Check {
	const name = "OS trust (live MITM handshake)"
	tmp, err := os.MkdirTemp("", "promptvcr-doctor-")
	if err != nil {
		return Check{Name: name, Status: Warn, Detail: "could not create temp dir: " + err.Error()}
	}
	defer os.RemoveAll(tmp)

	st, err := store.Open(tmp)
	if err != nil {
		return Check{Name: name, Status: Warn, Detail: err.Error()}
	}
	// Replay mode: a cache miss returns immediately without dialing upstream, but
	// the client<->proxy TLS handshake (the thing we are testing) still happens.
	srv, err := proxy.New(caDir, st, config.ModeReplay, sse.Instant)
	if err != nil {
		return Check{Name: name, Status: Fail, Detail: err.Error(), Remedy: "run `promptvcr init`"}
	}

	proxySrv := httptest.NewServer(srv.Handler())
	defer proxySrv.Close()
	proxyURL, _ := url.Parse(proxySrv.URL)

	client := &http.Client{
		Timeout: 10 * time.Second,
		Transport: &http.Transport{
			Proxy:           http.ProxyURL(proxyURL),
			TLSClientConfig: &tls.Config{}, // system roots only — this is the point
		},
	}
	resp, err := client.Get("https://" + host + "/")
	if err != nil {
		var unknown x509.UnknownAuthorityError
		if errors.As(err, &unknown) || isUnknownAuthority(err) {
			return Check{Name: name, Status: Fail,
				Detail: "system does not trust the PromptVCR CA",
				Remedy: "run `promptvcr init` (installs the CA into the OS trust store)"}
		}
		return Check{Name: name, Status: Warn, Detail: "could not reach " + host + ": " + err.Error()}
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 0))
	return Check{Name: name, Status: Pass, Detail: fmt.Sprintf("intercepted %s (status %d)", host, resp.StatusCode)}
}

// OK reports whether no check failed (warnings are tolerated).
func OK(checks []Check) bool {
	for _, c := range checks {
		if c.Status == Fail {
			return false
		}
	}
	return true
}

// Render writes a human-readable checklist to w.
func Render(w io.Writer, checks []Check) {
	for _, c := range checks {
		line := fmt.Sprintf("[%s] %s", c.Status, c.Name)
		if c.Detail != "" {
			line += " - " + c.Detail
		}
		fmt.Fprintln(w, line)
		if c.Status != Pass && c.Remedy != "" {
			fmt.Fprintf(w, "       -> %s\n", c.Remedy)
		}
	}
}

func isUnknownAuthority(err error) bool {
	// Windows/macOS verifiers surface trust failures as platform-specific errors
	// that don't always unwrap to x509.UnknownAuthorityError.
	s := err.Error()
	for _, sub := range []string{"unknown authority", "not trusted", "untrusted", "SecTrust", "certificate is not trusted"} {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

func samePath(a, b string) bool {
	return filepath.Clean(a) == filepath.Clean(b)
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
