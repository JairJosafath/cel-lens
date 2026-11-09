# Security policy

Report a vulnerability privately, through GitHub's private vulnerability reporting: <https://github.com/JairJosafath/cel-lens/security/advisories/new>. Never report one in a public issue.

The details, including how fast we answer, are in:

- [engine/SECURITY.md](engine/SECURITY.md) for the engine.
- [vscode/SECURITY.md](vscode/SECURITY.md) for the VS Code extension.

The code and its dependencies are watched: Dependabot proposes dependency updates every week, CodeQL analyses every change, govulncheck checks the engine against the Go vulnerability database on every change, and the OpenSSF Scorecard runs every week.
