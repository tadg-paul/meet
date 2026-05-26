# Review — issue #9

> [Migrate meet's per-subcommand help text to docs/help/](https://github.com/tadg-paul/meet/issues/9)

## Result

| Check | Result |
|---|---|
| `make test` | ✅ PASS — entire regression pack green |
| New warnings | 0 |
| Hard-block status | clear |
| Implementation commit | `44186a9` (pushed to origin/master) |

## What changed

- `docs/help/meet.txt`, `docs/help/meet-serve.txt`, `docs/help/meet-token.txt`, `docs/help/meet-create.txt`, `docs/help/meet-cancel.txt`, `docs/help/meet-list.txt` — canonical sources (committed).
- `cmd/meet/main.go` — removed every inline `fmt.Fprintln(os.Stderr, ...)` help string; replaced with `fmt.Fprint(os.Stderr, helpXxx)` against `//go:embed`'d variables. `fs.PrintDefaults()` is still called after each prose block so flag descriptions stay auto-generated.
- `Makefile` — `stage-help-text` target extended with 6 new `cp` lines.
- `.gitignore` — `cmd/meet/help-*.txt` added to the staged-copy exclusions.
- `tests/regression/registry_cli_test.go` — `buildMeetBinary` stages the docs files before `go build` so `go test` works without a Makefile run.
- `tests/regression/meet_helper_test.go` — RT-8.7 and RT-8.8 refined: the literal `docs/help/meet-token.txt` is now stripped before the substring check so it doesn't false-positive (`meet token` subcommand's docs file vs the old `meet-token` binary).

## Acceptance criteria

| ID | Tests |
|---|---|
| AC9.1 | ✅ RT-9.1, ✅ RT-9.2, ✅ RT-9.3, ✅ RT-9.4, ⏳ UT-9.1 |

## Standards self-check

- **GIT.md**: commit message uses `Implement #N:` form. No auto-close keyword. No `--no-verify`.
- **TESTING.md**: RT-9.3 statically scans Go source — explicit goal-state assertion ("no hardcoded help strings"), not a sneaky substitute for behavioural testing. RT-9.1/9.2 exercise the real entry point (`meet -h`, `meet <sub> -h`).
- **CODING.md**: no error suppression patterns, no string-built shell commands, no cross-language string assembly. Help text is plain UTF-8 with a single embed boundary.
- **CODE/GO.md**: stdlib `embed` only; no new third-party deps. `go vet` clean. No `http.DefaultClient` or similar regressions.

## Pending UT

- **UT-9.1**: edit `docs/help/meet.txt`, `make build`, observe edited content in `./bin/meet -h`; revert + rebuild restores original. No Go source change between edits.
