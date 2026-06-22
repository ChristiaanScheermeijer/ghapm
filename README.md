# GitHub Actions Package Manager

GitHub Actions Package Manager (ghapm) keeps your workflow files reproducible by pinning every `uses:` reference to an exact commit while still tracking upstream releases, highlighting safe upgrades, and honoring a configurable safety window.

```diff
- uses: actions/checkout@v4
+ uses: actions/checkout@<hash> # ghapm:v4
```

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

- `ghapm init` &mdash; scan workflow files, pin floating `uses:` references to commit SHAs, and append/update the tracking comment (`# ghapm:vX`). Existing pins are preserved unless the annotation needs to be refreshed. Supports `--api` to resolve refs via the REST API instead of the `gh` CLI.
- `ghapm check` &mdash; analyze workflow files locally and categorize each reference (floating, dynamic, tracked, missing annotation, etc.). Groups identical issues with colorized output and supports `--json` for machine-readable reports. (Remote release validation is planned in a future version.)
- `ghapm upgrade [--major]` &mdash; move pinned actions to the newest safe release. Respects the tracked major line, updates the annotation, and shows `(old -> new)` version ranges. Add `--major` to allow bumping to the next safe major, `--dry-run` to preview, `--safety-window` to override the default 14 days, `--api` to use the REST API instead of `gh`, and `--verbose` to log GitHub requests.

## Development

- Format code with `gofmt -w .`.
