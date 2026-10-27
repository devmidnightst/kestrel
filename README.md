# kestrel

kestrel is a small, statically typed programming language. the compiler is
written in go (standard library only), translates kestrel to c, and calls an
installed c compiler (cc, gcc or clang) to produce a native executable.

kestrel is young. the compiler is tested hard (unit tests, fuzzing, and
thousands of randomly generated programs compiled and run under address and
undefined behavior sanitizers in ci), but nobody has run it in production yet,
and it makes no performance promises. read the "what does not exist" section
before betting anything on it.

## requirements

- go 1.22 or newer
- gcc or clang on your path (as cc, gcc or clang)
- linux or macos (windows is untested)

## install

prebuilt binaries for linux and macos (amd64 and arm64) are attached to
[github releases](https://github.com/devmidnightst/kestrel/releases) when a
`v*` tag is pushed. unpack the archive and put `kestrel` on your `PATH`.
compiling a kestrel program still needs `cc`, `gcc`, or `clang` on that
machine. the binaries are the compiler, not a replacement for a c toolchain.

from source:

```sh
go build -o kestrel ./cmd/kestrel
```

## usage

```sh
kestrel build app.ks -o app          # compile to a native executable
kestrel run app.ks -- a b c          # compile to a temp dir, run with arguments
kestrel check app.ks                 # parse and type check only
kestrel build app.ks --emit-c app.c  # also keep the generated c
kestrel run app.ks --sanitize        # debug build with address and ub sanitizers
```

`kestrel run` exits with the program's exit status (128 + signal number if it
was killed by a signal). compile errors look like
`app.ks:3:5: error: undefined: x`.

## a taste

```
struct item {
    name: string,
    qty: int,
}

fn total(items: [item]) -> int {
    var sum = 0;
    var i = 0;
    while i < len(items) {
        sum = sum + items[i].qty;
        i = i + 1;
    }
    return sum;
}

fn main() {
    var items: [item] = [];
    push(items, item { name: "rope", qty: 3 });
    push(items, item { name: "lamp", qty: 1 });
    let before = items;
    items[0].qty = 10;
    print(total(before), total(items));
    print(items[0]);
}
```

prints `4 11` and `item { name: "rope", qty: 10 }`. `before` keeps its old
contents: arrays and structs are values, not shared references.

## what exists

- types: `int` (signed 64-bit), `float` (64-bit), `bool`, `string`, arrays
  `[t]`, maps `{string: t}` and `{int: t}`, and structs
- `let` (immutable) and `var` (mutable) with lexical block scope
- methods: `fn (self: item) cost(price: int) -> int`, called as `rope.cost(4)`.
  `fn (var self: item) add(n: int)` updates the caller's place and is only
  legal on a `var` (the same rule as `push`). an immutable receiver cannot
  assign to `self`
- functions, recursion, `if` / `else if` / `else`, `while`, `for i in 0..n`,
  `for i in 0..=n`, `for x in array`, `break`, `continue`, `return`, `print`
- value semantics with automatic memory management: reference counting plus
  copy-on-write. memory is freed as soon as it is unreachable, and since values
  cannot form cycles, nothing leaks
- safety checks that stop the program with a clear message and exit status
  101: integer overflow, division by zero, out of range indexing, stack
  overflow from deep recursion, bad conversions, failed `assert`
- `fmt("{} is {}", a, b)` builds a string. `{}` is a hole, `{{` and `}}` are
  literal braces, and the values must be int, float, bool or string. a format
  written as a string literal is checked at compile time. `print` accepts
  every value, including arrays, maps and structs
- builtins for strings (`split`, `join`, `find`, `substr`, `trim`, `upper`,
  ...), math (`sqrt`, `pow`, `abs`, `min`, `max`, ...), arrays (`push`, `pop`,
  `fill`, `slice`, `len`), parsing (`parse_int`, `parse_float`), input
  (`read_line`, `eof`, `args`), files (`read_file`, `write_file`,
  `file_exists`), and blocking TCP (`tcp_listen`, `tcp_accept`, `tcp_read`,
  `tcp_write`, `tcp_close`). A server handles one connection at a time.
  Failed sockets return `-1` or `""` instead of stopping the process
- modules: `import "lib/x.ks";` with `pub fn` and `pub struct` exports. paths
  are relative and cannot escape the entry file's directory (symlinks
  included)
- a narrow c header import for functions that take and return only `int`,
  `bool` and `float`:

  ```
  import c "math.h" {
      fn cbrt(x: float) -> float;
  }
  ```

## what does not exist

no generics, closures, enums, tuples, optional or error types,
globals, concurrency, TLS, or unicode-aware string
functions (strings are bytes). arrays and structs cannot be compared with
`==`. errors that a real program might want to recover from (a missing file,
a bad number) either return a bool or stop the program; there is no way to
catch them. TCP is blocking and single-connection: there is no HTTP library
and no way to serve two clients at once. the c import is not a general ffi:
no strings, pointers, structs, callbacks, or extra libraries. there is no go
package interop.

copy-on-write has a cost model worth knowing: changing a value that is still
shared copies it first, so a function that takes an array, changes it and
returns it copies the whole array each call. changing a `var` in place
(`push(xs, x)`, `xs[i] = v`) does not copy.

[docs/spec.md](docs/spec.md) is the full and honest reference.

## examples

```sh
kestrel run examples/variables.ks
kestrel run examples/functions.ks
kestrel run examples/branching.ks
kestrel run examples/loops.ks
kestrel run examples/for.ks
kestrel run examples/fmt.ks
kestrel run examples/methods.ks
kestrel run examples/maps.ks
kestrel run examples/arrays.ks
kestrel run examples/structs.ks
kestrel run examples/modules/main.ks
kestrel run examples/cheader.ks
printf 'the cat\nthe dog\n' | kestrel run examples/wordfreq.ks
```

## development

```sh
gofmt -l .
go vet ./...
go test ./...
```

tests that need a c compiler or sanitizers skip themselves when those are
missing. ci runs formatting, vet, race-enabled tests, fuzzing, and randomly
generated programs (plain and sanitized) on linux (gcc and clang) and macos
(clang). details are in [docs/development.md](docs/development.md).

## license

[mit](LICENSE). copyright 2026 midnight.

## layout

- `cmd/kestrel`: command line entry point
- `internal/cli`: argument handling for build, run and check
- `internal/compiler`: lexer, parser, type checker, module loader, c code
  generator, c runtime and build driver
- `examples`: sample programs
- `docs/spec.md`: language specification
- `docs/development.md`: testing, fuzzing and release builds
