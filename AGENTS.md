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
    - For unpinned references, resolve the latest safe commit, lock it in place, and append a tracking comment such as
      `# ghapm:v4`.
    - Skip entries that already point to a commit SHA.
2. **Monitor (`ghapm check`)**
    - Compare pinned commits with upstream releases within the tagged major version recorded in the comment.
    - Report available minor and patch updates that have cleared the 14-day safety window.
    - Flag major-version releases separately so maintainers can plan breaking changes.
3. **Upgrade (`ghapm upgrade [--major]`)**
    - For each action, move the pinned commit to the newest safe release allowed by the tracked major version.
    - Respect the `# ghapm:vX` annotation unless `--major` is provided, in which case the tool may bump to the next
      major and update the comment accordingly.

## Inline Annotation Format

- `uses: owner/repo@<commit-sha> # ghapm:v<major>`
- The comment is authoritative for determining the major line ghapm should follow.
- On upgrades, ghapm updates both the SHA and (if needed) the comment to reflect the new major alignment.

## Decision Rules

- **Safety delay**: ignore releases younger than 14 days.
- **Minor/Patch**: automatically eligible when within the tracked major.
- **Major**: reported during `check`; applied only when explicitly requested with `--major`.

## Open Questions / Next Steps

- Define how ghapm discovers workflow files (convention vs. configuration).
- Decide on handling private actions or enterprise registries.
- Determine the reporting format for `check` (CLI table, JSON output, exit codes).
