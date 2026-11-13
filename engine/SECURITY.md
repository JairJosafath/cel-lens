# Security policy

## Report a vulnerability

Report it privately through GitHub's private vulnerability reporting on
https://github.com/JairJosafath/cel-lens (the Security tab, "Report a
vulnerability"). Never report a vulnerability in a public issue, pull request or
discussion.

Say what you found, the version (`cel-lens-core --version`), and how to
reproduce it.

## What to expect

- We acknowledge your report within 7 days.
- Within 14 days we tell you whether we consider it a vulnerability and when a
  fix ships.
- A fix ships as a patch release. The `CHANGELOG.md` entry of that release names
  the advisory.

Only the latest release is supported.

## Dependencies

Every module `go.mod` requires, and the Go standard library at the Go version
its `go` line names, is looked up in the [OSV database](https://osv.dev), which
holds the Go vulnerability database and GitHub's advisories. The standard
library is the package `stdlib` there, and an empty answer means no known
advisory:

```sh
curl -s https://api.osv.dev/v1/query \
  -d '{"package": {"ecosystem": "Go", "name": "stdlib"}, "version": "1.26.11"}'
```

[OSV-Scanner](https://google.github.io/osv-scanner/) asks the same for every
module of `go.mod` (`osv-scanner scan -L go.mod`) and leaves aside the
advisories `osv-scanner.toml`, beside `go.mod`, ignores. The Vulnerabilities
check of the repository's OpenSSF Scorecard reports what it finds for the
modules.

Only the query above covers the standard library. OSV-Scanner from 2.4.0
skips the Go version of `go.mod` unless `ScanGoModVersion` is set, a key
older releases refuse, and the Scorecard discards what the scanner finds for
the standard library.

An advisory with a fixed version is fixed by requiring that version: of the
module in `go.mod`, or of Go in its `go` line, the oldest Go release the
engine is built with.

An advisory is ignored only when no version fixes it and it does not apply to
the engine. Its entry in `osv-scanner.toml` has a `reason` a reader can check:

- it starts with the module and the version it was checked at, as `go.mod`
  requires them;
- it names the advisory in the GitHub Advisory Database, what that one is
  about and the last version it lists as affected;
- it names the affected code and why the engine does not use it, such as a
  package it does not build, which `go list -deps ./... | grep <module>`
  shows.

A test, `TestIgnoredVulnerabilities`, fails when an ignored advisory has no
reason, or when `go.mod` requires another version of the reason's module or
none. The reason is then checked again: it names the new version, or the
entry goes.
