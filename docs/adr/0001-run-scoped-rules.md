# 1. Run-scoped rules

Date: 2026-09-28

## Status

Proposed

## Context

Every check in v1 decides a file from that file alone. `pattern_absent` reads
one file's lines and answers about that file; `file_line_max` counts one file's
lines. The rule contract says so: a `Rule` is instantiated per (file, rule)
pair, sees `Init`, then `OnLine` per line, then `Finish`, and is never reused.

A real class of property is not expressible that way: agreement across files.
The motivating case is a tool version pinned in more than one place. The
engineering standard this repo follows keeps pins in a shared reference and
expects each consumer to copy the value, so `Makefile` and
`.github/workflows/ci.yml` both name the golangci-lint version. Duplication is
the intended model; a disagreement between the copies is the defect.

This repo shipped exactly that defect. The Makefile pinned v2.1.6 while CI
installed v2.13.2, so `make audit` locally ran an older analyzer set than CI
did. Nothing caught it: no check in the standard's manifest compares the two,
and spproof could not express the comparison either.

The obvious workaround is to assert each location against a literal:

```yaml
  - id: makefile-golangci-pin
    check: pattern_present
    files: ["Makefile"]
    with:
      pattern: 'GOLANGCI_VERSION\s*:=\s*v2\.13\.2'
```

That works and catches the drift, but it makes the policy a further copy of the
value. Bumping the pin then means editing the Makefile, the workflow and
`.spproof.yml`, and forgetting the last one blocks a correct upgrade. It trades
silent drift for a maintenance tax, and it never verifies agreement: it verifies
that each file matches a constant someone typed into the policy.

What is wanted is a rule that captures a value from each file it applies to and
fails when the captured values are not all equal, naming no value itself.

### Why this was previously believed impossible

An earlier reading of the invariants concluded that a cross-file assertion would
break "a file is read once, through a buffered line reader, regardless of how
many rules apply" and the per-(file, rule) rule contract.

That reading was wrong. The engine already has a post-walk phase, and a rule
that reports from it. `resolvable_local_path` cannot decide a link from the
file's bytes: it needs the filesystem. So it does not verdict from `OnLine` at
all. It accumulates candidates, the engine drains them through
`rules.Contextual.TakeCandidates`, and the verdict is produced after the walk
finishes, in `resolver.wait()`. `Run` already appends those violations after the
per-file loop, and already waits for them before returning, because "a run
cannot report a verdict while a path resolution is outstanding".

A run-scoped rule needs the same shape: observe during the walk, decide after
it. The difference is only what the deferred decision consults, other files'
observations rather than the filesystem.

### Performance

The stated constraint is that this must not cost throughput. It does not, and
the reason is structural rather than a matter of careful coding.

`scanLines` iterates three collections per line: `active` (every rule's
`OnLine`), `aware` (markdown classification consumers) and `contextual`
(candidate draining). A run-scoped rule extracts its value in `OnLine` like any
rule, returns no violation from it, and publishes one observation from `Finish`,
which runs once per file, outside the loop. It adds no per-line work beyond its
own pattern match and no allocation into the per-line violation slice.

It is also instantiated only where it applies. `activeRules` filters by glob and
by type before constructing anything, so a rule scoped to `Makefile` and
`ci.yml` is never built for the other files in a tree. The benchmark corpus does
not see it.

Measured baseline before any change, for comparison after: `BenchmarkThroughput`
583 MB/s, 9506 allocs/op; `BenchmarkStartup` 65 us/op. The existing `!race`
gates in `internal/engine` are the regression check.

## Decision

Add a third, optional half of the rule contract for checks whose verdict depends
on other files, and one check using it.

### The interface

```go
// Aggregating is implemented by a check whose verdict is not computable from
// one file. The engine drains observations after each file and decides once the
// walk is complete.
type Aggregating interface {
    TakeObservations() []Observation
}

type Observation struct {
    Path   string
    Line   int
    Column int
    Value  string
}
```

Named `Aggregating`, not folded into `Contextual`. `Contextual` means "needs the
filesystem"; this means "needs the other files". Overloading one name would make
both harder to reason about, and a rule could legitimately want neither or both.

A pure rule implements neither and is untouched, which is the same property that
made `Contextual` acceptable.

### Where the verdict is produced

`Run` gains an aggregation step between the per-file loop and `sortViolations`,
alongside the existing `res.wait()`. Observations are collected per rule id into
a map, and each aggregating rule's decision function turns its observations into
violations. Those violations join the others before sorting, so ordering,
ratchets and rendering are unchanged downstream.

### Determinism

Output must stay byte-identical across runs over an unchanged tree. Two rules
follow:

1. Observations are sorted by (path, line, column) before any decision is made.
   Walk order must not reach the verdict.
2. The reference value is the one observed at the lowest sorted path, not the
   most frequent. Frequency is undecidable on a tie and would make the verdict
   depend on how many files happen to hold each value.

Every file whose value differs from the reference yields its own violation, at
the position where its value was captured. A disagreement is relational and
belongs to no single file, but reporting it per divergent file gives a human and
a SARIF consumer a location to act on, and states the expectation: "value X,
which disagrees with Y at <reference path>".

### The under-populated case

A comparison needs at least two values. If fewer than two files produced an
observation, the rule did not fail, but it also did not decide anything. Treating
that as a pass would violate the invariant that an unevaluable rule must never
render as a rule that held, and it would do so precisely in the regression worth
catching: deleting the pin from the workflow leaves one value, and a rule that
passes on one value would bless the deletion.

Such a rule therefore emits a file-scoped violation naming how many files it saw
and how many it needed. It fails the run.

Coverage accounting is unchanged: `EvaluatedFiles` already counts the files a
rule was instantiated for, so a run-scoped rule with one evaluated file reports
`evaluatedFiles: 1` alongside its failure, and every output format already
distinguishes that from a rule that never ran.

### The check

`consistent_value`, configured with a `pattern` whose first capture group names
the value:

```yaml
  - id: golangci-pin-agrees
    check: consistent_value
    files: ["Makefile", ".github/workflows/ci.yml"]
    with:
      pattern: 'golangci-lint[@ :=v]+([0-9]+\.[0-9]+\.[0-9]+)'
    message: "The golangci-lint pin must be identical in the Makefile and CI."
```

The policy names no version. Bumping the pin requires no policy edit, which is
the property the `pattern_present` workaround lacks.

Multiple matches in one file each produce an observation, so a file that
disagrees with itself fails too.

## Consequences

### Positive

- A class of property that was previously unexpressible becomes a declared
  check: agreement across files, with the value living in the files rather than
  in the policy.
- The drift that actually shipped in this repo would have been caught, and so
  would its inverse: deleting one of the two pins leaves one observation and
  fails the under-populated rule.
- No throughput cost, for the structural reasons above, verifiable against the
  recorded baseline through the existing `!race` gates.
- Pure rules are untouched. The extension is optional in the same way
  `Contextual` is.
- The deferred-verdict shape is not new to the codebase, so the engine gains a
  second instance of an existing pattern rather than a novel mechanism.

### Negative

- `Result` gains an aggregation phase. The engine grows a concept, and "a rule
  decides one file" stops being true of every rule, which was a simple thing to
  hold in one's head.
- A violation that is really about a relation is attributed to a file. That is a
  deliberate lie of convenience: it gives the finding a location, at the cost of
  implying the file alone is wrong.
- `CLAUDE.md`'s rule-contract section needs amending, since it currently
  describes `Contextual` as the only optional half.
- A policy author can now write a rule that is expensive on a large glob: the
  aggregation map holds one observation per match, not per file. A pattern
  matching every line of a large tree would accumulate accordingly. This is
  bounded by the policy's own globs and is the author's choice, but it is a new
  way to write a slow policy.
- More surface to keep honest across three output formats: text, JSON and SARIF
  must each distinguish a disagreement from an under-populated run, since every
  format is required to carry the same facts.

### Neutral

- The engine still never writes to the filesystem, still reads each file once,
  and still produces deterministic output. None of the load-bearing invariants
  changes; one of them ("a rule decides a file from that file alone") was never
  written down as an invariant and is the only thing this relaxes.
