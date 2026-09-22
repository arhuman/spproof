# Changelog

All notable changes to this project are documented here. Format: [Keep a Changelog](https://keepachangelog.com).

## [Unreleased]

### Added
- `spproof check` CLI: proves a declared set of static checks holds over a file or a tree, for CI jobs, git hooks, and agents.
- Six checks: pattern_absent, pattern_present, comment_line_char_max, comment_line_consecutive_max, file_line_max, and resolvable_local_path (a contextual check).
- Strict policy loader with clear errors for malformed or ambiguous policy files.
- Optional `message:` on a rule, replacing the generated violation text so a policy can say why a rule exists and what to write instead.
- Markdown fence and code-span awareness: `resolvable_local_path` ignores links inside code unconditionally, and `pattern_absent` opts in with `skip_code: true`.
- `fence_lang:` on `pattern_absent`, narrowing a rule to fenced blocks of one language. It is mutually exclusive with `skip_code:`, the opposite end of the same axis, and a rule naming a fence language drops for every non-markdown file.
- Ratchets: `baseline:` tolerates a count across the run and `baseline_per_file:` bounds the worst file, so a rule can be adopted on a tree that does not yet satisfy it. Tolerated findings are reported, never hidden.
- Text and SARIF-shaped JSON output formats.
- Exit codes: 0 when clean, 1 on violations, 2 when the engine could not run.
- Benchmark harness with a committed 10000 line corpus, plus CI gates on throughput and on end to end hook latency.
- Contributing guide and security policy.
- golangci-lint v2 configuration with gosec enabled.

### Fixed
- Version now resolves from build info when ldflags stamped nothing.
- `--stdin` silently truncated at 64 MiB, so content past the cap was reported as holding. An oversized payload now exits 2.
- A symlink in a walked tree was followed out of the tree, reporting an outside file under an inside path. Symlinks met while walking are now skipped.
- A FIFO or device file in a walked tree blocked the run indefinitely. Only regular files are walked.
- A symlink, FIFO or device named directly now exits 2 rather than being read or silently passing.
