# Contributing

## Setup

```bash
git clone https://github.com/arhuman/spproof
cd spproof
make build      # binary lands in bin/spproof
make tools      # install golangci-lint and govulncheck at the pinned versions
```

No database, no services, no code generation. Go and the two lint tools are the
whole toolchain.

## Make targets

| Target | Purpose |
| ------ | ------- |
| `make build` | Compile the binary (version-stamped via `-ldflags`). |
| `make test` | Unit tests. |
| `make fulltest` | All tests with the race detector and no cache. |
| `make cover` | Tests with coverage; fails below `COVER_MIN`. |
| `make audit` | `make cover` + `go vet` + `golangci-lint` + `govulncheck`. Same command locally and in CI. |
| `make bench` | Benchmarks. |
| `make install` | Install spproof from this checkout (falls back to the released binary when Go is absent). |
| `make tidy` | `gofmt` + `go mod tidy`. |
| `make release` | Derive the next semver from Conventional Commits, tag and push. |

## Adding a check

Implement `rules.Rule` (`Init`, `OnLine`, `Finish`) and call `rules.Register`.
The engine knows no rule by name.

- Check-specific configuration goes in the policy's `with:` block, decoded in
  `Validate` through `Spec.DecodeWith`, which rejects any key your struct does
  not name. A check taking no configuration calls `Spec.RejectWith`.
- Build violation text through `Spec.Msg(generated)`, never by assigning it
  directly, or a policy's `message:` override is silently ignored.
- Implement `rules.Preparer` when decoding costs anything, so the policy parser
  stays out of the walk.
- A rule needing the filesystem implements `rules.Contextual`.

## Invariants

These are load-bearing. A change that breaks one defeats the point of the tool,
so it needs to be argued for, not slipped in:

- An unevaluable rule must never render as a rule that held. Exit 2 is distinct
  from exit 1 for this reason.
- The engine never writes to the filesystem.
- A file is read once, regardless of how many rules apply.
- Output is deterministic and byte-identical across runs over an unchanged tree.
- A tolerated violation is reported, never hidden.
- Every output format carries the same facts, including rule coverage and
  baseline-absorbed findings.

## Commit messages

[Conventional Commits](https://www.conventionalcommits.org/): `type(scope): subject`,
type one of `feat|fix|docs|style|refactor|perf|test|build|ci|chore|revert`.
Written in English. Enforced in CI on pull request commits.

Never use em-dashes or en-dashes. The repo enforces this on itself via
`.spproof.yml`.

## Before opening a PR

1. `make audit` passes.
2. `spproof check --policy .spproof.yml .` passes (CI runs the tool on itself).
3. `CHANGELOG.md` updated under `[Unreleased]`.
4. Tests cover the new behavior. Coverage sits above 90 percent; keep it there.
