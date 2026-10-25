# Development

## Checks

```sh
gofmt -l .
go vet ./...
go test -race ./...
```

Tests that need a C compiler skip themselves when none is found. Set `CC` to
test against a specific compiler, for example `CC=clang go test ./...`.

## Randomly generated programs

`TestRandomProgramsCompileAndRun` generates well-typed Kestrel programs from
fixed seeds, checks them, compiles them with the C compiler, and runs them. A
program must exit with status 0 or with a Kestrel runtime error (status 101).
It runs 40 programs by default (10 with `-short`). Raise the count with:

```sh
KESTREL_RANDOM_PROGRAMS=1000 go test -run TestRandomProgramsCompileAndRun ./internal/compiler
```

This test proves that generated code compiles and does not crash. It does not
check that programs compute the right answer; that is covered by the
hand-written expected-output tests in `build_test.go`.

## Fuzzing

```sh
go test -run '^$' -fuzz '^FuzzLex$' -fuzztime 60s ./internal/compiler
go test -run '^$' -fuzz '^FuzzParse$' -fuzztime 60s ./internal/compiler
go test -run '^$' -fuzz '^FuzzCheckAndGenerate$' -fuzztime 60s ./internal/compiler
```

The fuzz targets assert that the lexer, parser, checker, and C generator never
panic on arbitrary input.

## Release builds

```sh
go build -trimpath -ldflags "-s -w -X github.com/devmidnightst/kestrel/internal/cli.Version=0.2.0" -o kestrel ./cmd/kestrel
```

Without the `-X` flag, `kestrel version` reports `0.1.0-dev`.
