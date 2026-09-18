# Changelog

All notable changes to this project are documented here. Format: [Keep a Changelog](https://keepachangelog.com).

## [Unreleased]

### Added
- `spproof check` CLI: proves a declared set of static checks holds over a file or a tree, for CI jobs, git hooks, and agents.
- Five checks: pattern_absent, pattern_present, comment_line_char_max, comment_line_consecutive_max, and resolvable_local_path (a contextual check).
- Strict policy loader with clear errors for malformed or ambiguous policy files.
- Text and SARIF-shaped JSON output formats.
- Exit codes: 0 when clean, 1 on violations, 2 when the engine could not run.
- Benchmark harness with a committed 10000 line corpus, plus CI gates on throughput and on end to end hook latency.
- Contributing guide and security policy.
- golangci-lint v2 configuration with gosec enabled.

### Fixed
- Version now resolves from build info when ldflags stamped nothing.
