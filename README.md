# spproof

Automate your static checks. You declare the rules your files must satisfy in a
YAML policy, and spproof proves they hold, or tells you exactly where they do not.

It answers with an exit code, so it drops into a CI job, a git hook, or an
agent's loop without any glue:

```
0  every applicable rule held
1  at least one violation
2  the policy or the run was broken, so nothing was proven
```

That last code is the point. A tool that cannot tell "clean" from "never ran"
lets a broken config read as a passing build.

## Install

With Go:

```bash
go install github.com/arhuman/spproof/cmd/spproof@latest
```

Without Go (Linux and macOS, amd64 and arm64):

```bash
curl -sSfL https://raw.githubusercontent.com/arhuman/spproof/main/install.sh | sh
```

The script resolves the latest release, verifies the download against the
release's `checksums.txt`, and installs to `$HOME/.local/bin`. Set `VERSION` to
pin a tag and `INSTALL_DIR` to install elsewhere:

```bash
VERSION=v0.1.1 INSTALL_DIR=/usr/local/bin curl -sSfL \
  https://raw.githubusercontent.com/arhuman/spproof/main/install.sh | sh
```

From a clone, `make install` builds the checkout you have rather than fetching a
release, falling back to the script when Go is absent. Binaries for every
platform, including Windows, are on the
[releases page](https://github.com/arhuman/spproof/releases).

## Use it

Write a policy:

```yaml
# .spproof.yml
version: 1
rules:
  - id: no-em-dash
    check: pattern_absent
    files: ["*.md", "internal/**/*.go"]
    with:
      pattern: "[—–]"
    message: "Use a comma or a period instead."

  - id: keep-files-readable
    check: file_line_max
    files: ["**/*.go"]
    with:
      max: 500
```

Run it:

```bash
spproof check --policy .spproof.yml .
```

```
README.md:42:18: [no-em-dash] Use a comma or a period instead.
internal/engine/engine.go:1:1: [keep-files-readable] file has 612 lines, over the 500 limit
```

Check a single file, or pipe content in and name what it should be treated as:

```bash
spproof check --policy .spproof.yml docs/guide.md
cat draft.md | spproof check --policy .spproof.yml --stdin --as=draft.md
```

## Checks

| Check | Proves | `with:` |
|-------|--------|---------|
| `pattern_absent` | No line matches the pattern | `pattern`, and optionally `skip_code: true` or `fence_lang: <lang>` |
| `pattern_present` | Some line matches the pattern | `pattern` |
| `file_line_max` | The file is at most N lines | `max` |
| `comment_line_char_max` | No comment line exceeds N characters | `max` |
| `comment_line_consecutive_max` | No run of comment lines exceeds N | `max` |
| `resolvable_local_path` | Every local link points at a file that exists | optionally `pattern` to supply your own extractor |
| `yaml_frontmatter` | Frontmatter has the keys you require and none you forbid | `required_keys`, `forbid_extra_keys`, `key_constraints`, `require_present` |

Every rule takes `id`, `check`, `files` (glob patterns), and an optional
`message` replacing the generated wording. Check-specific options go under
`with:`.

A rule that cannot apply to a file's type simply drops for that file. The other
rules still run on it.

A `key_constraints` entry takes `forbidden`, the values a key may not carry.
Normally it compares each YAML value whole, whether a scalar or an item in a
sequence. Add `split: ","` inside the entry to compare each separator-delimited
token instead, so the same policy reads `tools: Read, Grep, Edit` and the list
form alike:

```yaml
- id: agent-tools-readonly
  check: yaml_frontmatter
  files: ["agents/*.md"]
  with:
    key_constraints:
      tools:
        forbidden: ["Edit", "NotebookEdit"]
        split: ","
```

## Adopting a rule on a tree that fails it

You rarely get to turn on a rule and have the tree already satisfy it. A
baseline tolerates a known count so the rule can start guarding against
regressions today:

```yaml
  - id: no-em-dash
    check: pattern_absent
    files: ["**/*.md"]
    baseline: 40           # tolerate 40 across the run
    baseline_per_file: 3   # and no more than 3 in any one file
    with:
      pattern: "[—–]"
```

Go over either number and the run fails. Tolerated violations are still
reported, never hidden, so you can ratchet the number down as you clean up.

## Output formats

```bash
spproof check --policy .spproof.yml --format json .    # machine-readable
spproof check --policy .spproof.yml --format sarif .   # GitHub code scanning
```

All three formats carry the same facts, including which rules never ran and
which violations a baseline absorbed. Output is deterministic: two runs over an
unchanged tree are byte-identical.

## In CI

```yaml
- name: static policy
  run: spproof check --policy .spproof.yml .
```

For GitHub's Security tab, emit SARIF and upload it:

```yaml
- run: spproof check --policy .spproof.yml --format sarif . > spproof.sarif
  continue-on-error: true
- uses: github/codeql-action/upload-sarif@v3
  with:
    sarif_file: spproof.sarif
```

## Notes and limits

spproof reads files as lines, runes, and an extension. There is no lexer and no
syntax tree, which keeps it fast and dependency-free but has consequences worth
knowing:

- `//` inside a Go string reads as a comment.
- A doc comment cannot be told apart from one inside a function.
- Markdown fences and inline code spans *are* recognized, so rules about prose
  can exclude code.

It never writes to your files. There is no `--fix`.

## Verifying a release

Release archives ship with an SBOM and a signed checksum file:

```bash
cosign verify-blob checksums.txt \
  --bundle checksums.txt.sigstore.json \
  --certificate-identity-regexp 'https://github.com/arhuman/spproof/.*' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com
```

Releases published before the move to Sigstore bundles carry the older
two-file form instead:

```bash
cosign verify-blob checksums.txt \
  --signature checksums.txt.sig \
  --certificate checksums.txt.pem \
  --certificate-identity-regexp 'https://github.com/arhuman/spproof/.*' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com
```

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md). Security reports go to the private path
in [SECURITY.md](SECURITY.md), not a public issue.

## License

[MIT](LICENSE).
