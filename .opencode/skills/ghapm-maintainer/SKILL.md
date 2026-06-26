---
name: ghapm-maintainer
description: Use when operating the ghapm binary in a repository: initializing workflow pins, checking tracking health, and safely upgrading action SHAs.
---

# ghapm Maintainer

Use this skill for day-to-day `ghapm` CLI usage in repositories with GitHub Actions workflows.

## Core intent

- Keep GitHub Actions references reproducible by pinning to commit SHAs.
- Preserve and trust inline tracking comments like `# ghapm:v4` or `# ghapm:github-v1`.
- Enforce the safety window default of 14 days unless a command flag changes it.
- Avoid behavior that silently crosses major-version boundaries unless `--major` is enabled.

## Common command flow

- Run `ghapm init` first to pin floating references and add tracking comments.
- Run `ghapm check` to review status (tracked, floating, missing or invalid comments).
- Run `ghapm upgrade` to move to the newest safe release in the tracked major line.
- Add `--major` only when you intentionally want major upgrades.

## Practical defaults

- Prefer default workflow directory `.github/workflows` unless a repo uses another path.
- Prefer the default safety window of 14 days; set `--safety-window 0` only when needed.
- Prefer `gh` CLI-backed mode by default; use `--api` when direct REST calls are required.
- Use `--dry-run` before repository-wide upgrades.
- Use `--json` for CI automation or machine-readable reporting.

## Command examples

```bash
# Initialize and annotate all action refs
ghapm init

# Preview upgrade changes without writing files
ghapm upgrade --dry-run

# Permit major upgrades when intentionally requested
ghapm upgrade --major

# Emit JSON status for scripts or CI
ghapm check --json
```

## Troubleshooting checklist

- "No eligible tagged release found": confirm tracking comment major and safety window settings.
- Missing annotation warnings: rerun `ghapm init` to normalize and add `# ghapm:...` comments.
- API/rate-limit issues: authenticate `gh` (`gh auth status`) or use `--api` with `GITHUB_TOKEN`.
- Unexpected upgrade results: rerun with `--verbose` to inspect resolver behavior.
