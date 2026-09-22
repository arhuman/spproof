# Security Policy

## Supported versions

| Version | Supported |
| ------- | --------- |
| latest tag / `main` | yes |
| older | no |

## Reporting a vulnerability

Do not open a public issue for a security report. Email arhuman@gmail.com with a
description and, if possible, reproduction steps. Expect an acknowledgement
within a few business days. Please allow time for a fix before any public
disclosure.

## Scope

spproof reads files and writes a report. It never writes to the tree it checks,
and `--fix` is structurally absent. The interesting surface is therefore the
policy parser and the regexes a policy supplies: a policy file is trusted input,
so treat one from an untrusted source the way you would treat any other
executable config.
