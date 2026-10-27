# Contributing

Thanks for looking at Kestrel. The language is young. Changes should keep
`docs/spec.md` honest: if a behavior is not tested, it is not part of the
language.

## Setup

- Go 1.22 or newer
- gcc or clang (`cc`, `gcc`, or `clang`)

## Before a pull request

```sh
gofmt -l .
go vet ./...
go test -race ./...
```

If you change the language, also run:

```sh
KESTREL_RANDOM_PROGRAMS=200 go test -count=1 -run TestRandomProgramsCompileAndRun ./internal/compiler
KESTREL_RANDOM_PROGRAMS=200 go test -count=1 -run TestRandomProgramsUnderSanitizers ./internal/compiler
```

## What to update

- `docs/spec.md` for every user-visible behavior
- the README sections "what exists" and "what does not exist"
- `examples/` with a program that exits
- unit tests, and the random program generator when the feature can appear in generated code

Compile errors stay in the form `file.ks:line:col: error: message`.

Do not add Go dependencies outside the standard library.
