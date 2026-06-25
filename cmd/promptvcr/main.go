// Command promptvcr is the zero-config LLM record/replay proxy daemon.
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/promptvcr/cli/internal/ca"
	"github.com/promptvcr/cli/internal/config"
	"github.com/promptvcr/cli/internal/credentials"
	"github.com/promptvcr/cli/internal/doctor"
	"github.com/promptvcr/cli/internal/pricing"
	"github.com/promptvcr/cli/internal/proxy"
	"github.com/promptvcr/cli/internal/redact"
	"github.com/promptvcr/cli/internal/sse"
	"github.com/promptvcr/cli/internal/stats"
	"github.com/promptvcr/cli/internal/store"
	"github.com/promptvcr/cli/internal/sync"
	"github.com/spf13/cobra"
)

var (
	version  = "0.1.0"
	addr     string
	timing   string
	caDir    string
	fixtures string
)

func main() {
	root := &cobra.Command{
		Use:     "promptvcr",
		Short:   "Zero-config LLM record/replay proxy with drift detection",
		Version: version,
	}
	root.PersistentFlags().StringVar(&addr, "addr", "127.0.0.1:8889", "proxy listen address")
	root.PersistentFlags().StringVar(&caDir, "ca-dir", config.Dir(), "directory holding the local root CA")
	root.PersistentFlags().StringVar(&fixtures, "fixtures", config.FixturesDir(), "cassette directory")
	root.PersistentFlags().StringVar(&timing, "timing", "instant", "SSE replay timing: realtime|instant|accelerated")

	root.AddCommand(initCmd(), doctorCmd(), recordCmd(), replayCmd(), autoCmd(), lsCmd(), statsCmd(),
		loginCmd(), logoutCmd(), cloudLsCmd(), pullCmd(), pushCmd(), uninstallCACmd())

	if err := root.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func initCmd() *cobra.Command {
	var skipInstall bool
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Generate and trust the local root CA",
		RunE: func(_ *cobra.Command, _ []string) error {
			if err := ca.Ensure(caDir); err != nil {
				return err
			}
			fmt.Printf("Root CA ready in %s\n", caDir)
			if !skipInstall {
				if err := ca.Install(caDir); err != nil {
					fmt.Printf("warning: could not auto-install CA: %v\n", err)
				} else {
					fmt.Println("Root CA installed into the OS trust store.")
				}
			}
			fmt.Println("\nFor runtimes with their own trust store, export:")
			for k, v := range ca.Hints(caDir) {
				fmt.Printf("  %s=%s\n", k, v)
			}
			fmt.Printf("\nThen point your app at the proxy:\n  HTTPS_PROXY=http://%s\n", addr)
			return nil
		},
	}
	cmd.Flags().BoolVar(&skipInstall, "no-install", false, "generate the CA but do not modify the OS trust store")
	return cmd
}

func doctorCmd() *cobra.Command {
	var verify bool
	cmd := &cobra.Command{
		Use:   "doctor",
		Short: "Diagnose CA, trust, and proxy setup",
		// A failing check is a diagnostic result, not a usage error.
		SilenceUsage: true,
		RunE: func(_ *cobra.Command, _ []string) error {
			checks := doctor.Run(doctor.Options{CADir: caDir, ProxyAddr: addr, Verify: verify})
			doctor.Render(os.Stdout, checks)
			if !doctor.OK(checks) {
				return fmt.Errorf("one or more checks failed")
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&verify, "verify", false, "perform a live TLS-MITM handshake to confirm the OS trusts the CA")
	return cmd
}

func runProxy(mode config.Mode) error {
	cfg := config.Load()
	st, err := store.Open(fixtures)
	if err != nil {
		return err
	}
	st.IgnorePaths = cfg.IgnorePaths
	red := redact.Compile(cfg.Redact.JSONPaths, cfg.Redact.Patterns, cfg.Redact.ReplaceWith)
	prices := pricing.Load(config.Dir())

	srv, err := proxy.New(caDir, st, mode, parseTiming(timing),
		proxy.WithPricing(prices), proxy.WithRedaction(red))
	if err != nil {
		return fmt.Errorf("setup proxy (did you run `promptvcr init`?): %w", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	httpSrv := &http.Server{Addr: addr, Handler: srv.Handler()}
	fmt.Printf("promptvcr %s on http://%s  (mode=%s, fixtures=%s)\n", version, addr, mode, fixtures)
	fmt.Printf("export HTTPS_PROXY=http://%s\n", addr)

	errCh := make(chan error, 1)
	go func() {
		if err := httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errCh <- err
		}
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		stop() // restore default signal handling so a second Ctrl-C force-quits
		fmt.Fprintln(os.Stderr, "\nshutting down...")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = httpSrv.Shutdown(shutdownCtx)
		flushStats(srv.Stats())
		return nil
	}
}

// flushStats renders the session summary to stderr, appends a markdown table to
// $GITHUB_STEP_SUMMARY when running in GitHub Actions, and merges the session
// into the cumulative ~/.promptvcr/stats.json.
func flushStats(snap stats.Snapshot) {
	snap.View("PromptVCR session summary").WriteText(os.Stderr)

	if snap.Hits+snap.Misses+snap.Records == 0 {
		return // nothing happened; don't inflate the cumulative session count
	}

	if p := os.Getenv("GITHUB_STEP_SUMMARY"); p != "" {
		if f, err := os.OpenFile(p, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644); err == nil {
			snap.View("PromptVCR savings").WriteMarkdown(f)
			_ = f.Close()
		}
	}

	dir := config.Dir()
	cum := stats.LoadCumulative(dir)
	cum.Add(snap)
	if err := cum.Save(dir); err != nil {
		fmt.Fprintf(os.Stderr, "warning: could not persist stats: %v\n", err)
	}
}

func recordCmd() *cobra.Command {
	return &cobra.Command{Use: "record", Short: "Proxy on; always hit live and record",
		RunE: func(_ *cobra.Command, _ []string) error { return runProxy(config.ModeRecord) }}
}

func replayCmd() *cobra.Command {
	return &cobra.Command{Use: "replay", Short: "Proxy on; replay only (a miss is an error — CI default)",
		RunE: func(_ *cobra.Command, _ []string) error { return runProxy(config.ModeReplay) }}
}

func autoCmd() *cobra.Command {
	return &cobra.Command{Use: "auto", Short: "Proxy on; replay on hit, record on miss (local default)",
		RunE: func(_ *cobra.Command, _ []string) error { return runProxy(config.ModeAuto) }}
}

func lsCmd() *cobra.Command {
	var staleDays int
	cmd := &cobra.Command{
		Use:   "ls",
		Short: "List local cassettes (with age and STALE markers)",
		RunE: func(_ *cobra.Command, _ []string) error {
			if staleDays <= 0 {
				staleDays = config.Load().StaleDays
			}
			st, err := store.Open(fixtures)
			if err != nil {
				return err
			}
			recs, err := st.List()
			if err != nil {
				return err
			}
			cutoff := time.Now().AddDate(0, 0, -staleDays)
			stale := 0
			for _, r := range recs {
				kind := "json"
				if len(r.Response.Stream) > 0 {
					kind = fmt.Sprintf("stream/%d", len(r.Response.Stream))
				}
				age, tag := ageAndTag(r.RecordedAt, cutoff)
				if tag != "" {
					stale++
				}
				fmt.Printf("%-12s %-10s %-4s %-28s %-11s %6s  %s\n",
					r.Key[:12], r.Provider, r.Request.Method, r.Request.Path, kind, age, tag)
			}
			fmt.Printf("%d cassette(s)", len(recs))
			if stale > 0 {
				fmt.Printf(", %d stale (older than %dd)", stale, staleDays)
			}
			fmt.Println()
			return nil
		},
	}
	cmd.Flags().IntVar(&staleDays, "stale-days", 0, "age in days past which a cassette is marked STALE (0 = config/default)")
	return cmd
}

func statsCmd() *cobra.Command {
	var ghSummary, reset bool
	var staleDays int
	cmd := &cobra.Command{
		Use:          "stats",
		Short:        "Show cumulative savings and cassette inventory",
		SilenceUsage: true,
		RunE: func(_ *cobra.Command, _ []string) error {
			dir := config.Dir()
			if reset {
				if err := stats.ResetCumulative(dir); err != nil {
					return err
				}
				fmt.Println("Cumulative stats reset.")
				return nil
			}
			if staleDays <= 0 {
				staleDays = config.Load().StaleDays
			}
			cum := stats.LoadCumulative(dir)
			view := cum.View("PromptVCR cumulative savings")
			if st, err := store.Open(fixtures); err == nil {
				if recs, err := st.List(); err == nil {
					view.Cassettes = len(recs)
					view.Stale = countStale(recs, staleDays)
				}
			}
			view.WriteText(os.Stdout)

			if ghSummary {
				if p := os.Getenv("GITHUB_STEP_SUMMARY"); p != "" {
					if f, err := os.OpenFile(p, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644); err == nil {
						view.WriteMarkdown(f)
						_ = f.Close()
					}
				} else {
					view.WriteMarkdown(os.Stdout)
				}
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&ghSummary, "github-summary", false, "also write a markdown table to $GITHUB_STEP_SUMMARY (or stdout if unset)")
	cmd.Flags().BoolVar(&reset, "reset", false, "reset cumulative stats")
	cmd.Flags().IntVar(&staleDays, "stale-days", 0, "age in days past which a cassette is stale (0 = config/default)")
	return cmd
}

// ageAndTag returns a human-readable age for a cassette and "STALE" if it was
// recorded before cutoff.
func ageAndTag(recordedAt string, cutoff time.Time) (age, tag string) {
	t, err := time.Parse(time.RFC3339, recordedAt)
	if err != nil {
		return "?", ""
	}
	if t.Before(cutoff) {
		tag = "STALE"
	}
	return humanizeAge(time.Since(t)), tag
}

func humanizeAge(d time.Duration) string {
	switch days := int(d.Hours() / 24); {
	case days >= 1:
		return fmt.Sprintf("%dd", days)
	case d.Hours() >= 1:
		return fmt.Sprintf("%dh", int(d.Hours()))
	case d.Minutes() >= 1:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	default:
		return "new"
	}
}

func countStale(recs []*store.Record, staleDays int) int {
	if staleDays <= 0 {
		staleDays = config.DefaultStaleDays
	}
	cutoff := time.Now().AddDate(0, 0, -staleDays)
	n := 0
	for _, r := range recs {
		if t, err := time.Parse(time.RFC3339, r.RecordedAt); err == nil && t.Before(cutoff) {
			n++
		}
	}
	return n
}

func pushCmd() *cobra.Command {
	return &cobra.Command{
		Use:          "push",
		Short:        "Upload local cassettes to the cloud vault",
		SilenceUsage: true,
		RunE: func(_ *cobra.Command, _ []string) error {
			st, err := store.Open(fixtures)
			if err != nil {
				return err
			}
			recs, err := st.List()
			if err != nil {
				return err
			}

			// Prefer the saved PAT login when present; fall back to the legacy
			// env-based JWT/team flow.
			if creds, err := credentials.Load(); err == nil && strings.HasPrefix(creds.Token, "pvcr_") {
				base, key := resolveEndpoint(creds)
				if base == "" || key == "" {
					return fmt.Errorf("missing Supabase URL/api-key: re-run `promptvcr login` with --url/--api-key")
				}
				n, err := sync.NewWithToken(base, key, creds.Token).PushAll(recs)
				fmt.Printf("pushed %d/%d cassette(s)\n", n, len(recs))
				return err
			}

			base, key, token, team := os.Getenv("PROMPTVCR_SUPABASE_URL"), os.Getenv("PROMPTVCR_SUPABASE_ANON_KEY"),
				os.Getenv("PROMPTVCR_TOKEN"), os.Getenv("PROMPTVCR_TEAM_ID")
			if base == "" || key == "" || token == "" || team == "" {
				return fmt.Errorf("not logged in: run `promptvcr login`, or set PROMPTVCR_SUPABASE_URL, PROMPTVCR_SUPABASE_ANON_KEY, PROMPTVCR_TOKEN, PROMPTVCR_TEAM_ID")
			}
			n, err := sync.New(base, key, token, team).PushAll(recs)
			fmt.Printf("pushed %d/%d cassette(s)\n", n, len(recs))
			return err
		},
	}
}

// resolveEndpoint returns the Supabase URL and api-key, preferring saved
// credentials and falling back to the PROMPTVCR_SUPABASE_* env vars.
func resolveEndpoint(creds credentials.Credentials) (url, apiKey string) {
	url, apiKey = creds.URL, creds.APIKey
	if url == "" {
		url = os.Getenv("PROMPTVCR_SUPABASE_URL")
	}
	if apiKey == "" {
		apiKey = os.Getenv("PROMPTVCR_SUPABASE_ANON_KEY")
	}
	return url, apiKey
}

// cloudClient builds a token-authed sync client from saved credentials,
// erroring with actionable guidance when the login is incomplete.
func cloudClient() (*sync.Client, error) {
	creds, err := credentials.Load()
	if err != nil {
		return nil, err
	}
	if creds.Token == "" {
		return nil, fmt.Errorf("not logged in: run `promptvcr login`")
	}
	base, key := resolveEndpoint(creds)
	if base == "" || key == "" {
		return nil, fmt.Errorf("missing Supabase URL/api-key: re-run `promptvcr login` with --url/--api-key")
	}
	return sync.NewWithToken(base, key, creds.Token), nil
}

func loginCmd() *cobra.Command {
	var url, apiKey, token string
	cmd := &cobra.Command{
		Use:          "login",
		Short:        "Save cloud credentials (Supabase URL, api-key, and access token)",
		SilenceUsage: true,
		RunE: func(_ *cobra.Command, _ []string) error {
			if url == "" {
				url = os.Getenv("PROMPTVCR_SUPABASE_URL")
			}
			if apiKey == "" {
				apiKey = os.Getenv("PROMPTVCR_SUPABASE_ANON_KEY")
			}
			if url == "" || apiKey == "" {
				return fmt.Errorf("provide --url and --api-key (or set PROMPTVCR_SUPABASE_URL / PROMPTVCR_SUPABASE_ANON_KEY)")
			}
			if token == "" {
				fmt.Print("Paste your PromptVCR access token: ")
				line, err := bufio.NewReader(os.Stdin).ReadString('\n')
				if err != nil && line == "" {
					return fmt.Errorf("read token: %w", err)
				}
				token = strings.TrimSpace(line)
			}
			if token == "" {
				return fmt.Errorf("no token provided")
			}

			creds := credentials.Credentials{URL: url, APIKey: apiKey, Token: token}
			if err := credentials.Save(creds); err != nil {
				return err
			}
			fmt.Printf("Saved credentials to %s\n", credentials.Path())

			// Verify the token works by hitting cli_list_fixtures; a failure is a
			// warning, not fatal, since the credentials are already persisted.
			if _, err := sync.NewWithToken(url, apiKey, token).ListCloud(); err != nil {
				fmt.Fprintf(os.Stderr, "warning: could not verify token: %v\n", err)
			} else {
				fmt.Println("Token verified.")
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&url, "url", "", "Supabase project URL (or PROMPTVCR_SUPABASE_URL)")
	cmd.Flags().StringVar(&apiKey, "api-key", "", "Supabase anon/publishable key (or PROMPTVCR_SUPABASE_ANON_KEY)")
	cmd.Flags().StringVar(&token, "token", "", "PromptVCR access token (pvcr_...); prompted if omitted")
	return cmd
}

func logoutCmd() *cobra.Command {
	return &cobra.Command{
		Use:          "logout",
		Short:        "Remove saved cloud credentials",
		SilenceUsage: true,
		RunE: func(_ *cobra.Command, _ []string) error {
			if err := credentials.Clear(); err != nil {
				return err
			}
			fmt.Println("Logged out.")
			return nil
		},
	}
}

func cloudLsCmd() *cobra.Command {
	return &cobra.Command{
		Use:          "cloud-ls",
		Short:        "List cassettes stored in the cloud vault",
		SilenceUsage: true,
		RunE: func(_ *cobra.Command, _ []string) error {
			client, err := cloudClient()
			if err != nil {
				return err
			}
			list, err := client.ListCloud()
			if err != nil {
				return err
			}
			for _, f := range list {
				watch := ""
				if f.Watch {
					watch = "watch"
				}
				updated := f.UpdatedAt
				if updated == "" {
					updated = f.RecordedAt
				}
				fmt.Printf("%-28s %-10s %-20s %-6s %s\n", f.Name, f.Provider, f.Model, watch, updated)
			}
			fmt.Printf("%d cloud cassette(s)\n", len(list))
			return nil
		},
	}
}

func pullCmd() *cobra.Command {
	return &cobra.Command{
		Use:          "pull [cache_key]",
		Short:        "Download cloud cassettes into the local store",
		Args:         cobra.MaximumNArgs(1),
		SilenceUsage: true,
		RunE: func(_ *cobra.Command, args []string) error {
			client, err := cloudClient()
			if err != nil {
				return err
			}
			st, err := store.Open(fixtures)
			if err != nil {
				return err
			}

			var keys []string
			if len(args) == 1 {
				keys = []string{args[0]}
			} else {
				list, err := client.ListCloud()
				if err != nil {
					return err
				}
				for _, f := range list {
					keys = append(keys, f.CacheKey)
				}
			}

			n := 0
			for _, key := range keys {
				pf, err := client.Pull(key)
				if err != nil {
					return fmt.Errorf("pull %s: %w", key, err)
				}
				rec, err := recordFromPull(pf)
				if err != nil {
					return fmt.Errorf("pull %s: %w", key, err)
				}
				if err := st.Put(rec); err != nil {
					return fmt.Errorf("save %s: %w", key, err)
				}
				n++
			}
			fmt.Printf("pulled %d cassette(s)\n", n)
			return nil
		},
	}
}

// recordFromPull converts a cloud fixture into a local store.Record. The
// request/response payloads are stored as the marshaled store types, so they
// unmarshal directly back into them.
func recordFromPull(pf *sync.PulledFixture) (*store.Record, error) {
	rec := &store.Record{
		Key:        pf.CacheKey,
		Provider:   pf.Provider,
		Model:      pf.Model,
		TTFTMs:     pf.TTFTMs,
		RecordedAt: time.Now().UTC().Format(time.RFC3339),
	}
	if len(pf.Request) > 0 {
		if err := json.Unmarshal(pf.Request, &rec.Request); err != nil {
			return nil, fmt.Errorf("decode request: %w", err)
		}
	}
	if len(pf.Response) > 0 {
		if err := json.Unmarshal(pf.Response, &rec.Response); err != nil {
			return nil, fmt.Errorf("decode response: %w", err)
		}
	}
	// Fall back to the top-level host/path when the request body lacked them.
	if rec.Request.Host == "" {
		rec.Request.Host = pf.Host
	}
	if rec.Request.Path == "" {
		rec.Request.Path = pf.Path
	}
	return rec, nil
}

func uninstallCACmd() *cobra.Command {
	return &cobra.Command{
		Use:   "uninstall-ca",
		Short: "Remove the PromptVCR root CA from the OS trust store",
		RunE: func(_ *cobra.Command, _ []string) error {
			if err := ca.Uninstall(caDir); err != nil {
				return err
			}
			fmt.Println("Root CA removed.")
			return nil
		},
	}
}

func parseTiming(s string) sse.TimingMode {
	switch s {
	case "realtime":
		return sse.Realtime
	case "accelerated":
		return sse.Accelerated
	default:
		return sse.Instant
	}
}
