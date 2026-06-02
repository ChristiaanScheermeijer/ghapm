# GitHub Actions Package Manager

GitHub Actions Package Manager (ghapm) keeps your workflow files reproducible by pinning every `uses:` reference to an exact commit while still tracking upstream releases.

```diff
- uses: actions/checkout@v4
+ uses: actions/checkout@<hash> # ghapm:v4
```

## Installation

### Package Managers

```shell
$ npm install -g ghapm
$ pnpm add -g ghapm
$ yarn global add ghapm
```

## Commands

- `ghapm init` &mdash; pin floating `uses:` references to commit SHAs and record the tracked major version (`# ghapm: vX`).
- `ghapm check` &mdash; report available minor and patch updates that are at least 14 days old, and flag newer major releases.
- `ghapm upgrade [--major]` &mdash; update pinned commits to the latest safe release; pass `--major` to opt in to major bumps.

> The current implementation provides scaffolding for these commands. The workflow discovery logic, GitHub release resolution, and upgrade engine still need to be implemented.

## Development

- Format code with `gofmt -w .`.

### Build from Source

```bash
go build .
./ghapm --help
```
