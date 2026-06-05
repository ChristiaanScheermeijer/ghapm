# GitHub Actions Package Manager (ghapm)

ghapm is a CLI designed to keep GitHub Actions workflows reproducible by pinning every `uses:` reference to an exact
commit while still tracking upstream releases.

## Core Principles

- Replace floating action tags (for example `actions/checkout@v4`) with immutable commit SHAs.
- Persist the original major version in an inline comment so ghapm can enforce version boundaries.
- Only promote a new upstream release after it has been available for at least 14 days, reducing the risk of running
  malware (supply-chain) infected dependencies.

## Workflow Lifecycle

1. **Initialize (`ghapm init`)**
    - Scan workflow files for `uses:` statements.
    - For unpinned references, resolve the latest safe commit via the shared GitHub client (gh CLI by default, REST with
      `--api`), lock it in place, and append a tracking comment such as `# ghapm:v4`.
    - Preserve pinned references, updating their annotation when necessary.
2. **Monitor (`ghapm check`)**
    - Analyze workflow files locally and categorize each `uses:` reference (tracked, floating, missing annotation, etc.).
    - Group identical issues, colorize terminal output by default, and support JSON emission.
    - Highlight entries that would need attention before running `init` or `upgrade`; remote version validation is not yet performed.
3. **Upgrade (`ghapm upgrade [--major]`)**
    - For each action, move the pinned commit to the newest safe release allowed by the tracked major version (updating the
      `# ghapm:vX` annotation as needed).
    - With `--major`, opt in to the newest safe higher major; the resolver short-circuits once it finds an eligible release
      and updates both the commit and annotation accordingly.
    - Supports `--dry-run`, `--json`, `--safety-window`, `--api`, and `--verbose` flags.

## Inline Annotation Format

- `uses: owner/repo@<commit-sha> # ghapm:v<major>`
- The comment is authoritative for determining the major line ghapm should follow.
- On upgrades, ghapm updates both the SHA and (if needed) the comment to reflect the new major alignment.

## Decision Rules

- **Safety delay**: ignore releases younger than 14 days.
- **Minor/Patch**: automatically eligible when within the tracked major.
- **Major**: reported during `upgrade`; applied only when explicitly requested with `--major`.
- **Logging**: `--verbose` prints cache hits/misses and GitHub requests to stderr for all commands.

## Open Questions / Next Steps

- Define how ghapm discovers workflow files (convention vs. configuration).
- Decide on handling private actions or enterprise registries.
- Extend reporting formats beyond the current colorized/text and JSON outputs (e.g., richer tables, exit codes).
- Add remote release validation to `check` so it can surface actionable upgrades without running `upgrade`.
