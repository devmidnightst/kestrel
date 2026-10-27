# Development

## Checks

```sh
gofmt -l .
go vet ./...
go test -race ./...
```

Tests that need a C compiler skip themselves when none is found, and tests
that need AddressSanitizer/UndefinedBehaviorSanitizer skip themselves when a
sanitized build cannot be produced. Set `CC` to test a specific compiler, for
example `CC=clang go test ./...`.

## What the tests cover

- `lexer_test.go`, `parser_test.go`: tokens, positions, precedence, syntax
  errors, nesting limits.
- `checker_test.go`: type errors, scoping, mutability, imports (including path
  escapes, symlinks and cycles), structs across modules.
- `build_test.go`, `features_test.go`: programs compiled to native code and run,
  with exact expected output; runtime errors; stdin, arguments and files; value
  semantics; a long-running allocation-heavy program under a 64 MiB address
  space limit (Linux) to show memory is reclaimed.
- `randprog_test.go`: randomly generated well-typed programs (see below).
- `fuzz_test.go`: fuzz targets.
- `internal/cli`: the command-line interface.

## Memory safety and leak checking

The runtime uses reference counting. To check that it neither leaks nor
touches freed memory, the feature tests, the examples and the random programs
are also built with `--sanitize` (AddressSanitizer + UndefinedBehaviorSanitizer)
and run with leak detection enabled on Linux. Any sanitizer report fails the
test. Leak detection only applies to programs that exit normally; a runtime
error exits immediately without freeing memory, by design.

## Randomly generated programs

`randprog_test.go` generates well-typed Kestrel programs from fixed seeds:
ints, floats, strings, arrays, nested arrays, structs with array fields,
copies followed by mutation through paths like `v[i].items[j] = x`, `push` and
`pop`, `while` and `for` loops with `break`/`continue`, `fmt`, method calls on
`Pair` (`sum` and `bump`), early returns,
and calls between generated functions. `for` bounds in generated programs are
reduced modulo 3 so a random range cannot spin for a huge number of steps. Each program must type check, compile under strict C
warning flags, and exit with status 0 or a Kestrel runtime error (101).

```sh
KESTREL_RANDOM_PROGRAMS=1000 go test -run TestRandomProgramsCompileAndRun ./internal/compiler
KESTREL_RANDOM_PROGRAMS=1000 go test -run TestRandomProgramsUnderSanitizers ./internal/compiler
```

These tests prove generated code compiles and is memory safe; they do not
check that programs compute the right answer. Correctness is covered by the
hand-written expected-output tests.

## Fuzzing

```sh
go test -run '^$' -fuzz '^FuzzLex$' -fuzztime 60s ./internal/compiler
go test -run '^$' -fuzz '^FuzzParse$' -fuzztime 60s ./internal/compiler
go test -run '^$' -fuzz '^FuzzCheckAndGenerate$' -fuzztime 60s ./internal/compiler
```

The fuzz targets assert that the lexer, parser, checker and C generator never
panic on arbitrary input.

## Release builds

```sh
go build -trimpath -ldflags "-s -w -X github.com/devmidnightst/kestrel/internal/cli.Version=0.2.0" -o kestrel ./cmd/kestrel
```

Without the `-X` flag, `kestrel version` reports the development version.
