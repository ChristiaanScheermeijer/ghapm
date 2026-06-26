# GitHub Actions Package Manager

[![License: Apache-2.0](https://img.shields.io/badge/License-Apache_2.0-blue.svg)](LICENSE)
![GitHub branch check runs](https://img.shields.io/github/check-runs/christiaanscheermeijer/ghapm/main)
![GitHub Release](https://img.shields.io/github/v/release/christiaanscheermeijer/ghapm)

GitHub Actions Package Manager (ghapm) keeps your workflow files reproducible by pinning every `uses:` reference to an exact commit while still tracking upstream releases, highlighting safe upgrades, and honoring a configurable safety window.

```diff
- uses: actions/checkout@v4
+ uses: actions/checkout@<hash> # ghapm:v4
```

### Why should you use ghapm?

- **No lock-in**: ghapm only rewrites workflow YAML; there is no runtime dependency, GitHub Action dependency, or required service.
- **Free and open source**: use, inspect, and contribute without vendor restrictions.
- **Safer upgrades by default**: releases can be delayed with the safety window to reduce exposure to fresh supply-chain compromises.
- **Easy workflow maintenance**: initialize once, then use `check` and `upgrade` to keep action refs healthy and consistent.
- **Reproducible CI**: commit-pinned `uses:` references make workflow runs deterministic across environments and time.
- **Clear audit trail**: tracking comments (`# ghapm:...`) make upgrade intent explicit in code review.

> [!WARNING]
> ghapm is a work in progress. Behavior, command flags, and output formats may change between releases.
> Please try it and share feedback or feature requests via GitHub Issues.

## Installation

### GitHub Releases

Download a prebuilt binary from the [GitHub Releases](https://github.com/christiaanscheermeijer/ghapm/releases) page.

If you use the GitHub CLI, you can fetch the latest release assets with:

```bash
gh release download --repo christiaanscheermeijer/ghapm --pattern "*"
```

### Build from Source

```bash
go build .
./ghapm --help
```

## Commands

### GitHub API strategy (important)

By default, ghapm uses the `gh` CLI for GitHub requests. This is intentional:

- it reuses your `gh auth` session,
- it avoids low unauthenticated API quotas,
- and it helps prevent rate-limit issues when processing many actions or tags.

You can force direct REST requests with `--api` (for `init` and `upgrade`). When using `--api`, set `GITHUB_TOKEN` to avoid tighter anonymous limits.

### `ghapm init`

Initialize (or normalize) workflow action pins.

What it does:

- scans workflow files (default: `.github/workflows`),
- converts floating refs such as `@v4` to commit SHAs,
- appends or updates tracking comments: `# ghapm:v<major>` or `# ghapm:<tag-prefix>v<major>`,
- keeps already pinned SHAs and refreshes annotations when needed.

Common usage:

```bash
# Pin everything in .github/workflows
ghapm init

# Preview only
ghapm init --dry-run

# Output JSON for scripting/CI
ghapm init --json

# Use REST API directly instead of gh CLI
ghapm init --api
```

Flags:

- `--workflows <dir>`: workflow directory to scan (default `.github/workflows`)
- `--dry-run`: show changes without writing files
- `--json`: emit machine-readable output
- `--safety-window <days>`: minimum release age before pinning (default `14`, set `0` to disable)
- `--api`: use GitHub REST API instead of `gh` CLI

### `ghapm check`

Inspect workflows and report action-reference health.

What it does:

- classifies each `uses:` entry (tracked, floating, dynamic, missing/invalid annotation, ignored),
- groups identical issues in text output for quick review,
- supports JSON output for automation.

`check` is currently a local analysis step; it does not query remote releases yet.

Common usage:

```bash
# Human-readable report
ghapm check

# CI-friendly JSON
ghapm check --json

# Scan a custom directory
ghapm check --workflows .github/workflows
```

Flags:

- `--workflows <dir>`: workflow directory to scan
- `--json`: emit machine-readable output

### `ghapm upgrade`

Move pinned actions to the newest safe release.

What it does:

- follows the tracked line in `# ghapm:v<major>` or `# ghapm:<tag-prefix>v<major>`,
- upgrades SHAs to newer eligible releases,
- enforces a safety window (default: 14 days),
- updates the SHA and tracking comment together when needed.

Common usage:

```bash
# Upgrade within tracked major versions
ghapm upgrade

# Preview changes
ghapm upgrade --dry-run

# Allow major-version bumps
ghapm upgrade --major

# Change or disable safety window
ghapm upgrade --safety-window 21
ghapm upgrade --safety-window 0

# JSON + verbose logs
ghapm upgrade --json --verbose
```

Flags:

- `--major`: allow upgrades to the next major line
- `--workflows <dir>`: workflow directory to scan
- `--dry-run`: show changes without writing files
- `--json`: emit machine-readable output
- `--safety-window <days>`: minimum release age before upgrades (default `14`, set `0` to disable)
- `--api`: use GitHub REST API instead of `gh` CLI
- `--verbose` / `-v`: print client/cache request details to stderr

## Development

- Format code with `gofmt -w .`.

## opencode Skill

This repository includes an opencode skill for operating the `ghapm` binary:

- `.opencode/skills/ghapm-maintainer/SKILL.md`

If you use opencode from this repository, the skill is discovered automatically. Restart opencode after pulling updates so newly added or changed skill content is loaded.

## License

Licensed under the Apache License 2.0. See `LICENSE`.
