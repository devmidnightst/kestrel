# Kestrel Language Specification (v0.3)

This document describes what the Kestrel compiler in this repository actually
implements. Anything not listed here is not supported. Kestrel is a young
language: it is tested heavily (see `docs/development.md`) but has no
production track record, and this document makes no performance promises.

## Source files

- Source files use the `.ks` extension, must be valid UTF-8, and may be at
  most 8 MiB.
- Whitespace separates tokens. `//` starts a comment that runs to the end of
  the line. There are no block comments.
- Diagnostics have the form `file.ks:LINE:COL: error: message`. Columns count
  Unicode code points, starting at 1.
- The lexer and parser stop at the first syntax error. The type checker reports
  every type error it finds, sorted by position. The `kestrel` command prints at
  most 50 errors.
- Blocks, `if`/`else if` chains, types and expressions may nest at most 256
  levels deep.

## Lexical elements

- Identifiers: `[A-Za-z_][A-Za-z0-9_]*` (ASCII only).
- Keywords: `fn pub struct let var if else while for in return print eprint
  true false import as break continue try`.
- Integer literals: decimal digits, `0` to `9223372036854775807`. `-5` is unary
  minus applied to `5`; as a special case `-9223372036854775808` is accepted.
- Float literals: `1.5`, `0.25`, `2e10`, `1.5e-3`. A literal needs digits on
  both sides of the `.` (`1.` and `.5` are not literals). Literals that overflow
  to infinity are rejected.
- String literals: double quoted, single line. Escapes: `\n \t \r \\ \"`.
  Raw control characters (other than tab) and NUL are rejected in literals.
- Punctuation: `+ - * / % = == != < <= > >= && || ! ( ) { } [ ] , ; : -> .`

## Types

| type        | meaning                                                         |
|-------------|-----------------------------------------------------------------|
| `int`       | signed 64-bit integer                                           |
| `float`     | IEEE 754 double precision                                       |
| `bool`      | `true` or `false`                                               |
| `string`    | immutable byte string with a length (may contain any byte)      |
| `[T]`       | growable array of `T`, for any type `T` (including `[[T]]`)     |
| `{K: V}`    | map from `string` or `int` keys to `V`                          |
| `Name`      | a struct declared in the same module                            |
| `mod.Name`  | a `pub struct` declared in an imported module                   |
| `!T`        | a result: either `ok` with a value of type `T`, or `err` with a message |
| `!`         | a result that carries no value: `ok()` or `err` with a message  |

There are no implicit conversions: `1 + 1.0` is an error; write
`float(1) + 1.0`.

### Value semantics

Every value behaves like a value. Assigning, passing or returning an array or
struct gives the receiver an independent value; changing one never changes
another:

```
var a = [1, 2];
var b = a;
b[0] = 99;        // a is still [1, 2]
```

This is implemented with reference counting and copy-on-write: copies are
cheap (they share storage) until one side is modified, at which point the
modified side copies the storage it changes. Because values cannot refer to
themselves, reference counting reclaims all memory as soon as it becomes
unreachable; there is no garbage collector and no leak from cycles.

Consequences worth knowing:

- Modifying a value that is shared copies it first. A function such as
  `fn add(xs: [int], x: int) -> [int] { var out = xs; push(out, x); return out; }`
  copies the whole array on every call when the caller still holds `xs`.
  Prefer modifying a `var` in place with `push(xs, x)` or `xs[i] = v`.
- Strings are immutable; every string operation that "changes" a string
  allocates a new one.

## Program structure

```
file      = { import } { struct | function } .
import    = "import" STRING [ "as" IDENT ] ";"
          | "import" "c" STRING "{" { "fn" signature ";" } "}" .
struct    = [ "pub" ] "struct" IDENT "{" [ field { "," field } [ "," ] ] "}" .
field     = IDENT ":" type .
function  = [ "pub" ] "fn" [ receiver ] signature block .
receiver  = "(" [ "var" ] IDENT ":" type ")" .
signature = IDENT "(" [ param { "," param } ] ")" [ "->" type ] .
param     = IDENT ":" type .
type      = "[" type "]" | "{" type ":" type "}" | IDENT [ "." IDENT ]
          | "!" [ type ] .
```

- Imports come before all declarations. There are no global variables or
  constants.
- Functions and structs may be used before they are declared. Functions may be
  recursive.
- Structs may contain other structs, and may contain arrays of themselves
  (`struct Node { kids: [Node] }`), but a struct may not contain itself
  directly or through other struct fields, since such a value could never be
  built.
- A struct and a function in the same module may not share a name. A method
  may use the same name as a function, because `total(x)` and `x.total()`
  are different calls. A method may not use the name of a field.
- The entry file must define `fn main()` with no parameters, returning nothing
  (exit status 0), `int` (the process exit status, truncated by the operating
  system) or `!`. When `main` returns `err(message)` the program prints
  `kestrel: error: message` to standard error and exits with status 1; `ok()`
  exits with status 0. Use `args()` for command-line arguments.

## Methods

```
fn (self: item) cost(price: int) -> int { return self.qty * price; }
fn (var self: item) add(n: int) { self.qty = self.qty + n; }

rope.add(2);
print(rope.cost(4));
```

The receiver is written before the method name. Its name is any identifier;
`self` is the convention used here. The receiver type must be a struct
declared in the same module. `pub fn` makes the method visible to importers.
`main` cannot be a method. A C import cannot declare one.

`self` without `var` is an immutable borrow of the value. The method cannot
assign to it or to its fields. `var self` is the caller's place: assigning
`self.field` or `self` updates that place, and copy-on-write still applies
when the value is shared with someone else. A `var` receiver is only legal
when the call's receiver is a place rooted in a `var` (a variable, field, or
element), the same rule as `push`. `item { qty: 1 }.add(1)` and `let x = ...;
x.add(1)` are errors.

This split keeps value semantics and the existing cost model. A method that
returned an updated copy would copy the whole struct whenever the caller
still held it. In-place mutation of a `var` does not. The `var` keyword on
the receiver makes that mutation visible at the declaration, not hidden in
the call.

Methods are not first-class values. `item.cost` is not a function value.
There is no inheritance and no method on `int`, `string`, arrays, or maps.

## Statements

```
block     = "{" { statement } "}" .
statement = ("let" | "var") IDENT [ ":" type ] "=" expr ";"
          | place "=" expr ";"
          | "if" expr block [ "else" ( if_stmt | block ) ]
          | "while" expr block
          | "for" IDENT "in" expr [ ".." [ "=" ] expr ] block
          | "return" [ expr ] ";"
          | ( "print" | "eprint" ) "(" [ expr { "," expr } ] ")" ";"
          | "break" ";" | "continue" ";"
          | [ "try" ] call ";"
          | block .
place     = IDENT { "[" expr "]" | "." IDENT } .
```

- `let` declares an immutable binding, `var` a mutable one. Every binding must
  be initialized. The type annotation is optional; it is required when the
  initializer is an empty array literal `[]`.
- Scopes are lexical. Each block opens a new scope. A name may shadow a name
  from an outer scope but may not be declared twice in the same scope.
  Parameters share the function body's scope. In `let x = ...` the initializer
  is evaluated before `x` is in scope.
- Assignment targets a variable, struct field or array element reached from a
  `var` binding: `v = e`, `v.f = e`, `v[i] = e`, `v[i].f[j] = e`. Parameters
  and `let` bindings, and anything reached through them, cannot be assigned.
  Strings cannot be modified in place.
- Evaluation order: index expressions in the target are evaluated first, left
  to right, then the right-hand side, then the store happens.
- `if` and `while` conditions must be `bool`. Braces are required. A struct
  literal inside a condition must be wrapped in parentheses.
- `break` and `continue` are only valid inside `while` or `for`. They apply
  to the innermost loop. `continue` in a `for` still advances the loop.
- A function with a result type must end in a `return` on every path. The
  check is syntactic: the last statement must be a `return`, a block ending in
  one, or an `if`/`else` chain whose branches all end in one. `while true` and
  `for` do not count, because the body might not run.
- Only function calls may be used as expression statements.
- `print` writes its arguments separated by single spaces plus a newline.
  It accepts every value type. Strings print raw at the top level and quoted
  inside arrays, maps and structs. Arrays print as `[1, 2]`, maps as
  `{"a": 1}` in the same order as `keys`, structs as `Point { x: 1.0, y: 2.0 }`.
  Floats print in the shortest form that reads
  back as the same value, with `.0` added to whole numbers. Results print as
  `ok(5)`, `ok()` and `err("message")`. Standard output is line buffered.
- `eprint` is `print` to standard error, which is also line buffered. Use it
  for logs and diagnostics so they stay out of a program's output.

### for

```
for i in 0..n { }     // i is 0, 1, ..., n-1
for i in 0..=n { }    // i is 0, 1, ..., n
for x in items { }    // x is each element of an array
```

- `..` excludes the end. `..=` includes it. Both count upward by 1. If the
  start is already past the end (`start > end`, or `start == end` for `..`),
  the body does not run. There is no downward range.
- The two bounds are evaluated once, left to right, before the body. A range
  is not an expression; `let x = 0..n` is an error that says to use `for`.
- The loop variable is a new immutable binding in the body only. Assigning to
  it is an error. It shadows an outer variable of the same name inside the
  body, and it is not visible after the loop.
- `for x in items` evaluates the array once and walks that value. `x` is a
  copy of each element (value semantics). Pushing onto the original variable
  during the loop does not change how many elements this loop visits, and it
  does not change the copies already produced. The loop holds its own retain
  of the array, so a later mutation copies instead of altering the walk.
- The step after an inclusive end is not computed. `for i in max..=max`,
  where `max` is the largest int, runs once and does not overflow.
- `for k in map` walks the keys, in the same order as `keys`. The map
  expression is evaluated once and retained, so deleting from the original
  `var` during the loop does not change which keys this loop visits. The
  loop variable is the key. Use `m[k]` for the value. Strings are not
  walked.
- `break` leaves the loop. `continue` abandons the rest of the body and
  advances. A `while` nested inside `for` has its own `break` and `continue`.
- A struct literal in the `for` header must be wrapped in parentheses, same
  as in an `if` or `while` condition. `for x in [item { n: 1 }]` is fine
  because the literal is inside the array.

## Results and errors

A function that can fail returns a result type. `!T` holds either a value of
type `T` or an error message; `!` holds either nothing or an error message.

```
fn parse_port(s: string) -> !int {
    let n = try try_parse_int(s);      // returns the error to our caller
    if n < 1 || n > 65535 {
        return err(fmt("port {} out of range", n));
    }
    return ok(n);
}

fn main() -> ! {
    let port = try parse_port(getenv("PORT"));
    print(port);
    return ok();
}
```

- `ok(v)` builds an `!T` from a value of type `T`; `ok()` builds a `!`.
  `err(message)` builds an error of whatever result type is expected there:
  a `return` in a function returning a result, an annotated `let`, a
  parameter, a field, an element. `let r = err("x");` without an annotation
  is an error because the type is unknown.
- `try e` requires `e` to be a result. If it is `ok`, `try e` is the value
  (nothing for `!`). If it is an error, the enclosing function returns that
  error at once, releasing everything it holds, as a `return` would. The
  enclosing function must itself return a result type (any one: the error
  message carries over).
- `try` must be the whole value of a `let`, `var`, assignment, `return` or
  call statement: `let x = try f();`, `x = try f();`, `try g();`. It cannot
  be nested inside a larger expression (`1 + try f()` is an error); bind the
  value first.
- Result types cannot be nested (`!!int`), compared with `==`, passed to
  `str` or `fmt`, or have methods. They can be stored in variables, arrays,
  maps and struct fields, and printed.
- Inspect a result with `is_ok`, `is_err`, `error`, `unwrap` and `unwrap_or`
  (see the builtin table). `unwrap` of an error is a runtime error.
- Errors are messages, not types: there is no matching on error kinds.

## Expressions

Precedence, lowest to highest; binary operators are left associative:

| level | operators            | operands                                  | result |
|-------|----------------------|-------------------------------------------|--------|
| 1     | `\|\|`               | bool                                      | bool   |
| 2     | `&&`                 | bool                                      | bool   |
| 3     | `==` `!=`            | two int, float, bool or string values     | bool   |
| 4     | `<` `<=` `>` `>=`    | two ints, two floats or two strings       | bool   |
| 5     | `+` `-`              | int or float; `+` also joins strings      | same   |
| 6     | `*` `/` `%`          | int or float; `%` is int only             | same   |
| 7     | unary `-` `!`        | int/float, bool                           | same   |
| 8     | call, `a[i]`, `s.f`  |                                           |        |

- `&&` and `||` short-circuit.
- Arrays and structs cannot be compared with `==`.
- Strings compare byte by byte.
- Integer `+ - * /` and unary `-` are checked: overflow is a runtime error.
  `/` truncates toward zero; `%` takes the sign of the dividend. Integer
  division or remainder by zero is a runtime error.
- Float arithmetic follows IEEE 754: dividing by zero gives `inf`, `-inf` or
  `nan`, and is not an error.
- `a[i]` requires `0 <= i < len(a)`; anything else is a runtime error.
- Array literal: `[e1, e2, ...]`; all elements must have the same type.
- Struct literal: `Name { field: value, ... }` or `mod.Name { ... }`. Every
  field must be given exactly once, in any order.

## Builtin functions

User functions may shadow builtin names.

| function | description |
|----------|-------------|
| `len(x)` | bytes in a string, elements in an array, or entries in a map |
| `str(x)` | int, float, bool or string to string |
| `float(i: int) -> float` | conversion |
| `int(f: float) -> int` | truncates toward zero; error for nan or out of range |
| `abs(x)`, `min(a, b)`, `max(a, b)` | int or float (both arguments the same type); `abs` of the minimum int is an overflow error |
| `sqrt`, `floor`, `ceil` | `float -> float` |
| `pow(x: float, y: float) -> float` | |
| `push(place, v)` | append to an array held in a `var` |
| `pop(place) -> T` | remove and return the last element; error when empty |
| `fill(v, n) -> [T]` | array of `n` copies of `v` |
| `slice(a, start, count) -> [T]` | copy of a range; error when out of range |
| `substr(s, start, count) -> string` | byte range; error when out of range |
| `find(s, sub) -> int` | first byte offset of `sub`, or `-1` |
| `contains`, `starts_with`, `ends_with` | `(string, string) -> bool` |
| `split(s, sep) -> [string]` | `sep` must not be empty |
| `join(parts: [string], sep) -> string` | |
| `trim(s)`, `upper(s)`, `lower(s)` | ASCII whitespace and case only |
| `byte_at(s, i) -> int` | byte value 0 to 255 |
| `chr(b: int) -> string` | one-byte string; `b` must be 0 to 255 |
| `parse_int(s) -> int`, `is_int(s) -> bool` | optional sign and decimal digits only, no spaces |
| `parse_float(s) -> float`, `is_float(s) -> bool` | decimal with optional fraction and exponent |
| `read_line() -> string` | next line of standard input without the line ending (`\n` or `\r\n`); `""` at end of input |
| `eof() -> bool` | true once `read_line` has hit the end of input |
| `args() -> [string]` | command-line arguments, without the program name |
| `read_file(path) -> string` | whole file; runtime error if it cannot be read |
| `write_file(path, content) -> bool` | creates or replaces a file; false on failure |
| `file_exists(path) -> bool` | true if the file can be opened for reading |
| `tcp_listen(host, port) -> int` | listening socket, or `-1`. `port` 0 asks the operating system for a free port. One accept at a time; there is no concurrency |
| `tcp_port(fd) -> int` | local port of a socket, or `-1` |
| `tcp_accept(fd) -> int` | next connection, or `-1`. Blocks until a client connects |
| `tcp_connect(host, port) -> int` | connected socket, or `-1` |
| `tcp_read(fd, max) -> string` | one read of at most `max` bytes (capped at 1 MiB). `""` on end of stream or error |
| `tcp_write(fd, data) -> int` | bytes written, or `-1` |
| `tcp_close(fd)` | close a socket. A bad descriptor is ignored |
| `fmt(format, ...)` | replace each `{}` in a string. `{{` and `}}` are literal braces. A string literal is checked at compile time for hole syntax and argument count; a computed format is checked at run time and stops the program on a mismatch. Values must be int, float, bool or string, rendered the same way as `print`. Arrays, maps and structs are rejected (use `print`) |
| `delete(place, key) -> bool` | remove `key` from a map held in a `var`. true if it was present. a missing key is left alone. same statement rule as `push` |
| `has(m, key) -> bool` | true if the map contains `key` |
| `keys(m) -> [K]` | the map's keys, in hash order |
| `values(m) -> [V]` | the map's values, in the same order as `keys` |
| `exit(code: int)` | flush standard output and exit immediately |
| `assert(cond: bool, message: string)` | runtime error with the message if `cond` is false |
| `ok(v) -> !T`, `ok() -> !` | a successful result |
| `err(message) -> !T` | an error result of the expected result type |
| `is_ok(r) -> bool`, `is_err(r) -> bool` | which kind of result `r` is |
| `error(r) -> string` | the error message, or `""` for `ok` |
| `unwrap(r) -> T` | the value; runtime error `unwrap of an error: message` for `err` |
| `unwrap_or(r, fallback) -> T` | the value, or `fallback` for `err`. Not for `!` |
| `try_read_file(path) -> !string` | whole file, or an error such as `cannot open "x": No such file or directory` |
| `try_write_file(path, content) -> !` | creates or replaces a file, or an error with the reason |
| `try_parse_int(s) -> !int` | like `parse_int`, with an error instead of stopping the program |
| `try_parse_float(s) -> !float` | like `parse_float`, with an error instead of stopping the program |
| `getenv(name) -> string` | value of an environment variable, `""` if unset |
| `has_env(name) -> bool` | true if the environment variable is set (even to `""`) |
| `time_ms() -> int` | wall clock time in milliseconds since the Unix epoch |
| `clock_ms() -> int` | monotonic milliseconds, for measuring durations and timeouts |
| `sleep_ms(ms)` | pause; returns early once a trapped shutdown signal arrives |
| `pid() -> int` | the process id |
| `trap_shutdown()` | catch `SIGTERM` and `SIGINT` instead of dying (see below) |
| `shutdown_requested() -> bool` | true once a trapped signal has arrived |
| `tcp_listen_shared(host, port) -> int` | like `tcp_listen` with `SO_REUSEPORT`: several processes can listen on one port and the kernel spreads connections across them |
| `tcp_poll(fds: [int], timeout_ms: int) -> [int]` | wait until sockets are readable. Returns the ready ones (in input order), or `[]` after the timeout or when a signal arrives. `-1` waits forever. A listening socket is ready when a client is waiting; a connection is ready when `tcp_read` will not block, including at end of stream |
| `tcp_set_timeout(fd, ms) -> bool` | read and write timeout for a socket; `0` clears it. A timed out `tcp_read` returns `""` and `tcp_write` returns `-1` |
| `tcp_peer(fd) -> string` | remote address as `1.2.3.4:5678` or `[::1]:5678`, `""` on failure |

`push` and `pop` change their first argument, so they may only be used as a
statement or as the entire value of a `let`, `var` or assignment. Their first
argument must be a place rooted in a `var`.

### Serving many clients

There are no threads. A server handles many connections at once from one
thread with `tcp_poll`, the same readiness model as `poll(2)` based servers:

```
trap_shutdown();
var conns: [int] = [];
while !shutdown_requested() {
    var watch = [server];
    for c in conns { push(watch, c); }
    for fd in tcp_poll(watch, 1000) {
        if fd == server {
            push(conns, tcp_accept(server));   // check for -1 in real code
        } else {
            let data = tcp_read(fd, 4096);     // does not block: fd is ready
            // ... "" means the client is gone: close it and drop it ...
        }
    }
}
```

`examples/http/service.ks` is a complete HTTP service in this style with
timeouts, logging and graceful shutdown. A connection that is ready to read
never blocks `tcp_read`, but `tcp_write` can block on a client that stops
reading; give each connection a `tcp_set_timeout`. To use more than one CPU
core, run several processes that each call `tcp_listen_shared` on the same
port.

### Signals

By default `SIGTERM` and `SIGINT` end the program immediately, like any C
program. After `trap_shutdown()` they set a flag instead: `shutdown_requested()`
becomes true, and a blocking `tcp_accept`, `tcp_read`, `tcp_poll` or
`sleep_ms` returns early (`-1`, `""`, `[]`) so the program can finish its
loop, close its sockets and exit. This is what process managers such as
systemd and Docker send when they stop a service, and a program running as
PID 1 in a container is not killed by `SIGTERM` at all unless it handles it.
`SIGPIPE` is always ignored, so writing to a closed socket returns `-1`.

Reading input:

```
var line = read_line();
while !eof() {
    print(line);
    line = read_line();
}
```

## Runtime errors

Runtime errors print `kestrel: runtime error: <reason>` to standard error,
flush standard output, and exit immediately with status 101. They cover
integer overflow, division by zero, index and range errors, `pop` on an empty
array, invalid `parse_int`/`parse_float` input, invalid conversions,
unreadable files in `read_file`, `unwrap` of an error, failed `assert`s, out
of memory, and stack overflow. Socket functions do not raise those errors:
they return `-1` or `""` so a server loop can keep running when a client
disconnects. Errors a program should recover from are results (`!T`); runtime
errors are for bugs.

Every function checks the remaining stack on entry. Recursion that would
overflow the stack is reported as `stack overflow (recursion too deep)`. The
limit is derived from the process stack limit (`ulimit -s`) with a safety
margin; with an unlimited stack the runtime assumes 1 GiB, which the
operating system may not actually provide. Stack overflow inside a C function called through a C import is not
detected.

`kestrel run` exits with the program's status, or 128 + signal number if the
program was killed by a signal.

## Modules

```
import "lib/math.ks";            // math.fn(), math.Type
import "lib/strings.ks" as s;    // s.fn(), s.Type
```

- The path must be relative, end in `.ks`, and use `/`. It is resolved against
  the directory of the importing file.
- The project root is the directory of the entry file. After resolving
  symbolic links every imported file must be inside it. Absolute paths, paths
  that escape the root, backslashes and NUL bytes are rejected.
- The module name defaults to the file name without `.ks` and must be a valid
  identifier; otherwise use `as`.
- Only `pub fn` functions and `pub struct` types are visible to importers. All
  fields of a visible struct are visible.
- Modules are loaded once. Import cycles are errors.
- There is no package manager and no search path.

## C header imports

```
import c "math.h" {
    fn cbrt(x: float) -> float;
}
```

A deliberately narrow mechanism, not a general foreign function interface.

- The header name must match `[A-Za-z0-9_][A-Za-z0-9_./-]*\.h` with no `.` or
  `..` segments, and is included as `#include <name>` from the C compiler's
  system include path. Only libc and libm are linked.
- Declared functions may take `int`, `bool` and `float` parameters and return
  one of those or nothing. Calls go through a generated wrapper, so the C
  compiler checks them against the header's prototype; a missing declaration or
  a pointer/number mismatch fails the build. Narrowing numeric conversions (for
  example a 64-bit `int` passed to a C `int`) are not detected.
- Declared C functions are callable unqualified in the importing file only.
  Names starting with `_` or `ks_`, and `main`, are reserved.
- Strings, arrays, structs, pointers, callbacks, variadic functions, macros and
  other libraries are not supported. Code called this way is outside Kestrel's
  safety guarantees.

## Compilation model

The compiler (Go, standard library only) lexes, parses and type checks all
modules, generates one C11 translation unit, and runs a C compiler to produce
a native executable. The C compiler is chosen from `--cc`, then the `CC`
environment variable, then the first of `cc`, `gcc`, `clang` on `PATH`. The
generated code needs GCC or Clang builtins (`__builtin_*_overflow`,
`__builtin_frame_address`). Linux and macOS are tested in CI; Windows is not.

Build modes:

- default: `-O2`.
- `--release`: `-O2`, `-fstack-protector-strong`, `-D_FORTIFY_SOURCE=2`, and
  on Linux a position independent executable with full RELRO
  (`-z relro -z now`), stripped. Use it for anything you deploy.
- `--static`: link statically. Fully static binaries work best with musl
  (Alpine's gcc); with glibc, `getaddrinfo` (used to resolve host names in
  `tcp_listen` and `tcp_connect`) still loads shared libraries at run time.
- `--sanitize`: AddressSanitizer and UndefinedBehaviorSanitizer for debugging.
  It cannot be combined with `--release` or `--static`.
- `--cflags "..."` and `--ldflags "..."` pass extra flags to the C compiler,
  before and after the source file. They are split on spaces.

The C compiler runs in a temporary directory on a file named `program.c`, so
the same source, compiler and flags produce the same binary byte for byte.

## Not supported

Generics, closures and first-class functions, interfaces, enums,
tuples, optional types, exceptions, typed errors (errors are messages),
compound assignment (`+=`), global variables and constants, string indexing
by character, Unicode-aware string operations, deep equality on arrays and
structs, threads, async I/O beyond `tcp_poll`, TLS (put a reverse proxy such
as Caddy or nginx in front), UDP, an HTTP library, directory operations,
setting environment variables, Go package interop, and arbitrary C FFI. `fmt` has no format specs inside `{}` (no
`{:.2}` and no `{0}`).
