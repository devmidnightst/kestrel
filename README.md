# kestrel

kestrel is a small, statically typed programming language. the compiler is
written in go (standard library only), translates kestrel to c, and calls an
installed c compiler (cc, gcc or clang) to produce a native executable.

kestrel is young. the compiler is tested hard (unit tests, fuzzing, and
thousands of randomly generated programs compiled and run under address and
undefined behavior sanitizers in ci), but nobody has run it in production yet,
and it makes no performance promises. it has what a small network service
needs (recoverable errors, a poll-based multi-client tcp loop, graceful
shutdown, env config, hardened static builds, a docker image), and
[docs/deploy.md](docs/deploy.md) shows how to ship one. read the "what does
not exist" section before betting anything on it.

## requirements

- go 1.22 or newer
- gcc or clang on your path (as cc, gcc or clang)
- linux or macos (windows is untested)

## install

prebuilt binaries for linux and macos (amd64 and arm64) are attached to
[github releases](https://github.com/devmidnightst/kestrel/releases) with a
`SHA256SUMS` file when a `v*` tag is pushed. the install script downloads the
latest one, checks its checksum, and puts it in `/usr/local/bin`:

```sh
curl -fsSL https://raw.githubusercontent.com/devmidnightst/kestrel/main/install.sh | sh
```

(`KESTREL_VERSION=v0.3.0` pins a release, `KESTREL_INSTALL_DIR=~/bin` changes
where it goes.) compiling a kestrel program still needs `cc`, `gcc`, or
`clang` on that machine. the binaries are the compiler, not a replacement for
a c toolchain.

with docker, `docker build -t kestrel .` gives an image with the compiler,
gcc and musl.

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
kestrel build app.ks --release       # hardened, stripped binary for deployment
kestrel build app.ks --release --static   # no shared libraries (best with musl)
kestrel build app.ks --cflags "-march=native"  # extra c flags (--ldflags too)
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
- result types for errors you can recover from: `!T` is `ok(value)` or
  `err("message")`, `!` is `ok()` or an error. `try f()` unwraps a result or
  returns its error from the current function, `fn main() -> !` prints the
  error and exits with status 1, and `is_ok`, `error`, `unwrap` and
  `unwrap_or` inspect one:

  ```
  fn load(path: string) -> !int {
      let text = try try_read_file(path);
      let n = try try_parse_int(trim(text));
      return ok(n);
  }
  ```
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
  `file_exists`, plus `try_read_file`, `try_write_file`, `try_parse_int`,
  `try_parse_float` that return results), the environment (`getenv`,
  `has_env`, `pid`), time (`time_ms`, `clock_ms`, `sleep_ms`), and TCP
  (`tcp_listen`, `tcp_accept`, `tcp_read`, `tcp_write`, `tcp_close`,
  `tcp_peer`, `tcp_set_timeout`). failed sockets return `-1` or `""` instead
  of stopping the process
- serving many clients at once from one thread: `tcp_poll(fds, timeout_ms)`
  returns the sockets that are ready, so a slow client never blocks the
  others. `tcp_listen_shared` lets several processes share a port to use
  more cores. see [examples/http/service.ks](examples/http/service.ks)
- graceful shutdown: after `trap_shutdown()`, `SIGTERM` and `SIGINT` set
  `shutdown_requested()` and wake blocking socket calls instead of killing
  the process, so it can close its sockets and exit 0 (and work as pid 1 in
  a container)
- `eprint(...)` is `print` to standard error, for logs
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

no generics, closures, enums, tuples, optional types, globals, threads,
TLS, or unicode-aware string functions (strings are bytes). arrays and
structs cannot be compared with `==`. errors are messages, not types: you
can propagate and inspect them but not match on kinds of error. there is no
HTTP library (the service example parses requests by hand) and no TLS, so
put caddy or nginx in front for https. concurrency is one thread plus
`tcp_poll`, or several processes; `tcp_write` blocks until the data is sent
or its timeout passes. the c import is not a general ffi: no strings,
pointers, structs, callbacks, or extra libraries. there is no go package
interop.

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
PORT=8080 kestrel run examples/http/service.ks   # then curl localhost:8080
```

## deploying

[docs/deploy.md](docs/deploy.md) covers release and static builds, writing a
program as a service (config, logs, timeouts, shutdown), a two-stage docker
image that ships a ~1 MB scratch container
([examples/deploy/Dockerfile](examples/deploy/Dockerfile)), a sandboxed
systemd unit, and tls through a reverse proxy.

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
- `docs/deploy.md`: running kestrel programs in production
- `Dockerfile`, `install.sh`: builder image and release installer
