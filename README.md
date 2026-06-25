# PromptVCR CLI

[![CI](https://github.com/promptvcr/cli/actions/workflows/ci.yml/badge.svg)](https://github.com/promptvcr/cli/actions/workflows/ci.yml)
[![Release](https://img.shields.io/github/v/release/promptvcr/cli?sort=semver)](https://github.com/promptvcr/cli/releases)
[![Go Report Card](https://goreportcard.com/badge/github.com/promptvcr/cli)](https://goreportcard.com/report/github.com/promptvcr/cli)
[![Coverage](https://img.shields.io/badge/coverage-in%20CI-informational)](https://github.com/promptvcr/cli/actions/workflows/ci.yml)
[![License: MIT](https://img.shields.io/github/license/promptvcr/cli)](LICENSE)

> Git for your LLM responses. Record once, replay free, and catch behavioral drift before it hits prod.

PromptVCR is a zero-config MITM proxy daemon that intercepts outbound HTTPS to the major LLM providers (OpenAI, Anthropic, Gemini, Ollama, OpenRouter), records each response as a deterministic cassette, and replays it at $0 and zero latency, including faithful SSE streaming timing. No SDK, no code changes: you point `HTTPS_PROXY` at it and run your tests.

This repository is the open-source CLI. It is mirrored automatically from our development monorepo.

## Install

Homebrew:

```bash
brew install promptvcr/tap/promptvcr
```

Shell (Linux/macOS):

```bash
curl -fsSL https://raw.githubusercontent.com/promptvcr/cli/main/scripts/install.sh | sh
```

PowerShell (Windows):

```powershell
irm https://raw.githubusercontent.com/promptvcr/cli/main/scripts/install.ps1 | iex
```

Go toolchain:

```bash
go install github.com/promptvcr/cli/cmd/promptvcr@latest
```

## Quick start

```bash
promptvcr init        # generate + trust a local root CA (one time)
promptvcr doctor      # verify CA / trust / proxy setup (add --verify for a live check)
promptvcr auto        # replay on a cache hit, record live on a miss

# Point any app or test suite at the proxy. No code changes:
export HTTPS_PROXY=http://127.0.0.1:8889
```

In CI, use replay-only so a missing fixture fails the build instead of spending tokens:

```bash
promptvcr replay
```

When the proxy stops it prints a savings summary (calls replayed, estimated dollars saved, cassettes recorded) and, inside GitHub Actions, writes the same table to the job summary.

```bash
promptvcr stats                  # cumulative totals + cassette inventory
promptvcr stats --github-summary # append the table to $GITHUB_STEP_SUMMARY
promptvcr ls                     # list cassettes (with age + STALE markers)
```

## GitHub Action

Wrap any test command so its LLM calls are served from cassettes:

```yaml
- uses: promptvcr/cli@v1
  with:
    mode: replay        # replay | auto | record
    fixtures: fixtures
    run: npm test       # optional: command to wrap; omit to leave the proxy running
```

The action installs the CLI, trusts the CA on the runner, exports the proxy and
CA environment variables, and flushes the savings summary to the job summary.

## Configuration

Drop a `.promptvcr.json` in your repo root (or `~/.promptvcr/config.json`) to
ignore volatile request fields, redact sensitive values before they are written
to disk, and tune the staleness threshold:

```json
{
  "ignorePaths": ["metadata.request_id"],
  "staleDays": 30,
  "redact": {
    "jsonPaths": ["messages[*].content"],
    "patterns": ["sk-[A-Za-z0-9]{20,}"],
    "replaceWith": "REDACTED"
  }
}
```

Auth headers (`Authorization`, `x-api-key`, and friends) are always stripped,
regardless of config.

## The cloud: team sync, visual diffs, and the Canary Guard

The CLI is the free, local half. PromptVCR Cloud adds a team fixture vault, a
visual side-by-side streaming-diff timeline, and the **Canary Guard**: an
out-of-band runner that replays your fixtures against the live provider APIs on a
schedule and pages you on Slack/PagerDuty the moment a response shape or behavior
drifts, before your users hit a broken schema.

Learn more and connect this CLI with `promptvcr push`: https://github.com/promptvcr

## License

MIT. See [LICENSE](LICENSE).
