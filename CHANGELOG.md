# Changelog

All notable changes to this project are documented here. Format: [Keep a Changelog](https://keepachangelog.com).

## [Unreleased]

### Added
- `spproof check` CLI: proves a declared set of static checks holds over a file or a tree, for CI jobs, git hooks, and agents.
- Seven checks: pattern_absent, pattern_present, comment_line_char_max, comment_line_consecutive_max, file_line_max, yaml_frontmatter, and resolvable_local_path (a contextual check).
- Strict policy loader with clear errors for malformed or ambiguous policy files.
- Optional `message:` on a rule, replacing the generated violation text so a policy can say why a rule exists and what to write instead.
- Markdown fence and code-span awareness: `resolvable_local_path` ignores links inside code unconditionally, and `pattern_absent` opts in with `skip_code: true`.
- `fence_lang:` on `pattern_absent`, narrowing a rule to fenced blocks of one language. It is mutually exclusive with `skip_code:`, the opposite end of the same axis, and a rule naming a fence language drops for every non-markdown file.
- Ratchets: `baseline:` tolerates a count across the run and `baseline_per_file:` bounds the worst file, so a rule can be adopted on a tree that does not yet satisfy it. Tolerated findings are reported, never hidden.
- Text and SARIF-shaped JSON output formats.
- `--format sarif`: SARIF 2.1.0 output, validated against the OASIS schema, for upload to GitHub code scanning. Rule coverage rides in the descriptor properties and a ratchet-tolerated finding renders as a suppressed result, so neither a vacuous rule nor an absorbed violation reads as a clean run.
- Exit codes: 0 when clean, 1 on violations, 2 when the engine could not run.
- Benchmark harness with a committed 10000 line corpus, plus CI gates on throughput and on end to end hook latency.
- Contributing guide and security policy.
- golangci-lint v2 configuration with gosec enabled.
- MIT license.
- README covering install, policy syntax, the check table, baselines, output formats, CI wiring, and release signature verification.
- `install.sh`, installing a released binary on a machine without Go. It resolves the latest tag, detects OS and architecture, verifies the archive against the release's `checksums.txt` and installs to `$HOME/.local/bin`; `VERSION` and `INSTALL_DIR` override both. Verification is the default because the documented use is a pipe into a shell.
- `make install`, building the current checkout when Go is present and falling back to `install.sh` when it is not.

### Changed
- `go.mod` declares a `go 1.25.0` floor with a `toolchain go1.26.6` line, so the module imports on the oldest supported release instead of requiring the toolchain it was built with.
- CI runs the test suite on the go.mod floor and on stable, and splits lint, dogfood and SAST into their own jobs. Action pins moved to checkout v7, setup-go v7, cosign-installer v4 and goreleaser-action v7.
- CodeQL SAST job added.
- CI installs from a synthetic release to prove `install.sh` tracks goreleaser's archive naming and refuses a tampered checksum, since nothing else would catch that drift until a user ran the published one-liner.
- CI validates the SARIF output against the OASIS 2.1.0 schema, across a violating run, a ratchet-tolerated run and a clean run. The unit tests assert the `$schema` string; only a validator proves the document satisfies it, and keeping it in CI leaves the module on its single dependency and the test suite offline.
- `.spproof.yml` sets `skip_code: true` on its own em-dash rule: the README documents the rule, so its examples contain the characters the rule forbids.
- SECURITY.md and CONTRIBUTING.md carry the project's real contact, setup and make targets instead of the scaffold's TODO placeholders.
- The check count above read six while seven were registered.

### Fixed
- `make audit` exited 0 on a machine without the lint tools, printing "skipping" three times while running no linter, no static analysis and no vulnerability scan. Each tool is now installed when absent or stale, then run unconditionally, so the gate fails instead of passing vacuously. `go mod verify` was also missing and now runs.
- The pinned `golangci-lint` was v2.1.6 while CI installed v2.13.2, so `make audit` locally ran a different analyzer set than CI. Both pins now match CI (golangci-lint v2.13.2, govulncheck v1.7.0, previously `latest`), and `require-tools` compares the installed version against the pin rather than only testing that the binary exists.
- Version now resolves from build info when ldflags stamped nothing.
- `--stdin` silently truncated at 64 MiB, so content past the cap was reported as holding. An oversized payload now exits 2.
- A symlink in a walked tree was followed out of the tree, reporting an outside file under an inside path. Symlinks met while walking are now skipped.
- A FIFO or device file in a walked tree blocked the run indefinitely. Only regular files are walked.
- A symlink, FIFO or device named directly now exits 2 rather than being read or silently passing.
