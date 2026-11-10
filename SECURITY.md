# Security policy

Report a vulnerability privately, through GitHub's private vulnerability reporting: <https://github.com/JairJosafath/cel-lens/security/advisories/new>. Never report one in a public issue.

The details, including how fast we answer, are in:

- [engine/SECURITY.md](engine/SECURITY.md) for the engine.
- [vscode/SECURITY.md](vscode/SECURITY.md) for the VS Code extension.

The code and its dependencies are watched: Dependabot proposes dependency updates every week, CodeQL analyses every change, govulncheck checks the engine against the Go vulnerability database on every change, and the OpenSSF Scorecard runs every week.

## Verifying a release

Every file of a [release](https://github.com/JairJosafath/cel-lens/releases) has a signature beside it: a file of the same name with `.sigstore.json` after it. The [sign-release.yml](.github/workflows/sign-release.yml) workflow of this repository makes it with [Sigstore](https://www.sigstore.dev/), under the workflow's own identity, so there is no signing key that could leak. To check a file, download it and its `.sigstore.json` into one folder and run [cosign](https://docs.sigstore.dev/cosign/system_config/installation/) 3 or newer there. For `checksums.txt` of an engine release:

```sh
cosign verify-blob \
  --bundle checksums.txt.sigstore.json \
  --certificate-identity-regexp '^https://github\.com/JairJosafath/cel-lens/\.github/workflows/sign-release\.yml@' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com \
  checksums.txt
```

`Verified OK` says that the file is the one this repository's workflow saw when it signed it, and that nothing changed it since. It is not a statement about how the file was built. Releases published before the workflow existed were signed afterwards, by the same workflow.
