// Command promptvcr is the zero-config LLM record/replay proxy daemon.
package main

import (
	"fmt"
	"net/http"
	"os"

	"github.com/promptvcr/cli/internal/ca"
	"github.com/promptvcr/cli/internal/config"
	"github.com/promptvcr/cli/internal/proxy"
	"github.com/promptvcr/cli/internal/sse"
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

	root.AddCommand(initCmd(), recordCmd(), replayCmd(), autoCmd(), lsCmd(), pushCmd(), uninstallCACmd())

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

func runProxy(mode config.Mode) error {
	st, err := store.Open(fixtures)
	if err != nil {
		return err
	}
	srv, err := proxy.New(caDir, st, mode, parseTiming(timing))
	if err != nil {
		return fmt.Errorf("setup proxy (did you run `promptvcr init`?): %w", err)
	}
	fmt.Printf("promptvcr %s on http://%s  (mode=%s, fixtures=%s)\n", version, addr, mode, fixtures)
	fmt.Printf("export HTTPS_PROXY=http://%s\n", addr)
	return http.ListenAndServe(addr, srv.Handler())
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
	return &cobra.Command{
		Use:   "ls",
		Short: "List local cassettes",
		RunE: func(_ *cobra.Command, _ []string) error {
			st, err := store.Open(fixtures)
			if err != nil {
				return err
			}
			recs, err := st.List()
			if err != nil {
				return err
			}
			for _, r := range recs {
				kind := "json"
				if len(r.Response.Stream) > 0 {
					kind = fmt.Sprintf("stream/%d", len(r.Response.Stream))
				}
				fmt.Printf("%-12s %-10s %s %s  [%s]\n", r.Key[:12], r.Provider, r.Request.Method, r.Request.Path, kind)
			}
			fmt.Printf("%d cassette(s)\n", len(recs))
			return nil
		},
	}
}

func pushCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "push",
		Short: "Upload local cassettes to the cloud vault",
		RunE: func(_ *cobra.Command, _ []string) error {
			base, key, token, team := os.Getenv("PROMPTVCR_SUPABASE_URL"), os.Getenv("PROMPTVCR_SUPABASE_ANON_KEY"),
				os.Getenv("PROMPTVCR_TOKEN"), os.Getenv("PROMPTVCR_TEAM_ID")
			if base == "" || key == "" || token == "" || team == "" {
				return fmt.Errorf("set PROMPTVCR_SUPABASE_URL, PROMPTVCR_SUPABASE_ANON_KEY, PROMPTVCR_TOKEN, PROMPTVCR_TEAM_ID")
			}
			st, err := store.Open(fixtures)
			if err != nil {
				return err
			}
			recs, err := st.List()
			if err != nil {
				return err
			}
			n, err := sync.New(base, key, token, team).PushAll(recs)
			fmt.Printf("pushed %d/%d cassette(s)\n", n, len(recs))
			return err
		},
	}
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
