# Kestrel Language Specification (v0.1, MVP)

This document describes what the Kestrel compiler in this repository actually
implements. Anything not listed here is not supported. Kestrel is an
experimental MVP: it is not production ready and makes no performance promises.

## Source files

- Source files use the `.ks` extension and must be valid UTF-8.
- Whitespace (space, tab, CR, LF) separates tokens. `//` starts a comment that
  runs to the end of the line. There are no block comments.
- Diagnostics have the form `file.ks:LINE:COL: error: message`. Columns count
  Unicode code points, starting at 1.
- The lexer and parser stop at the first syntax error. The type checker reports
  every type error it finds, sorted by position. The `kestrel` command prints at most
  50 errors.
- Limits: a source file may be at most 8 MiB, and blocks, `if`/`else if`
  chains and expressions may nest at most 256 levels deep.

## Lexical elements

- Identifiers: `[A-Za-z_][A-Za-z0-9_]*` (ASCII only).
- Keywords: `fn pub let var if else while return print true false import as
  break continue`.
- Integer literals: decimal digits only, `0` to `9223372036854775807`. `-5` is
  unary minus applied to `5`. As a special case, `-9223372036854775808` is
  accepted when the minus sign directly precedes the literal.
- String literals: double quoted, single line. Escapes: `\n \t \r \\ \"`.
  Raw control characters (other than tab) and NUL are rejected.
- Operators and punctuation: `+ - * / % = == != < <= > >= && || ! ( ) { } , ; : -> .`

## Types

| type     | meaning                                                   |
|----------|-----------------------------------------------------------|
| `int`    | signed 64-bit integer                                     |
| `bool`   | `true` or `false`                                         |
| `string` | immutable, NUL-terminated byte string                     |

Functions without `-> type` return nothing. There are no implicit conversions.

## Program structure

```
file      = { import } { function } .
import    = "import" STRING [ "as" IDENT ] ";"
          | "import" "c" STRING "{" { "fn" signature ";" } "}" .
function  = [ "pub" ] "fn" signature block .
signature = IDENT "(" [ param { "," param } ] ")" [ "->" type ] .
param     = IDENT ":" type .
type      = "int" | "bool" | "string" .
```

Imports must appear before function declarations. There are no global
variables, constants, structs, arrays, or user-defined types. Functions may be
called before they are declared and may be recursive.

The entry file must define `fn main()` with no parameters, returning either
nothing (exit status 0) or `int` (used as the process exit status, truncated by
the operating system).

## Statements

```
block     = "{" { statement } "}" .
statement = ("let" | "var") IDENT [ ":" type ] "=" expr ";"
          | IDENT "=" expr ";"
          | "if" expr block [ "else" ( if_stmt | block ) ]
          | "while" expr block
          | "return" [ expr ] ";"
          | "print" "(" [ expr { "," expr } ] ")" ";"
          | "break" ";" | "continue" ";"
          | call ";"
          | block .
```

- `let` declares an immutable binding; `var` declares a mutable one. Every
  binding must be initialized. The type annotation is optional and inferred
  from the initializer when omitted.
- Scopes are lexical. Each block (function body, `if`/`else` branch, `while`
  body, bare `{ }`) opens a new scope. A name may shadow a name from an outer
  scope but may not be declared twice in the same scope. Parameters share the
  function body's scope. The initializer of `let x = ...` is evaluated before
  `x` is in scope, so `let x = x + 1;` refers to an outer `x`.
- Only `var` bindings can be assigned. Assignment is a statement, not an
  expression, and its target must be a plain variable name.
- `if` and `while` conditions must have type `bool`. Braces are required;
  parentheses around the condition are optional.
- `break` and `continue` are only valid inside `while`.
- A function with a result type must end in a `return` on every path. The
  check is syntactic: the last statement of the body must be a `return`, a
  block that ends in a `return`, or an `if`/`else` chain whose branches all
  end in `return`. A `while true` loop does not satisfy it.
- Only function calls may be used as expression statements.
- `print` writes its arguments separated by single spaces, followed by a
  newline, to standard output. It accepts `int`, `bool`, and `string` values.
  Standard output is line buffered, so completed lines are not lost if the
  program later crashes.

## Expressions

Precedence, from lowest to highest (all binary operators are left associative):

| level | operators            | operand types                           | result   |
|-------|----------------------|-----------------------------------------|----------|
| 1     | `\|\|`               | bool                                    | bool     |
| 2     | `&&`                 | bool                                    | bool     |
| 3     | `==` `!=`            | two operands of the same type           | bool     |
| 4     | `<` `<=` `>` `>=`    | int                                     | bool     |
| 5     | `+` `-`              | int (`+` also concatenates strings)     | int/string |
| 6     | `*` `/` `%`          | int                                     | int      |
| 7     | unary `-` `!`        | int / bool                              | int/bool |
| 8     | calls, `module.fn()` |                                         |          |

- `&&` and `||` short-circuit.
- String `==`/`!=` compare contents.
- Integer `+ - * /` and unary `-` are checked: overflow aborts the program.
  `/` truncates toward zero; `%` takes the sign of the dividend. Division or
  remainder by zero aborts the program.
- Runtime errors print `kestrel: runtime error: <reason>` to standard error,
  flush standard output first, and exit with status 101.
- There is no stack overflow detection. Unbounded recursion usually ends with
  the operating system killing the program (for example `SIGSEGV`).
  `kestrel run` reports a program killed by a signal and exits with status
  128 + signal number.

### Builtin functions

| function             | description                                 |
|----------------------|---------------------------------------------|
| `len(s: string) -> int` | number of bytes in `s` (not characters)  |
| `str(n: int) -> string` | decimal representation of `n`            |

Builtin names cannot be redefined. `print` is a statement, not a function.

## Modules

```
import "lib/math.ks";            // callable as math.name(...)
import "lib/strings.ks" as s;    // callable as s.name(...)
```

- The path must be a relative path ending in `.ks`, using `/` as separator.
  It is resolved relative to the directory of the importing file.
- The project root is the directory containing the entry file. After resolving
  symbolic links, every imported file must be inside the project root.
  Absolute paths, paths that escape the root, backslashes, and NUL bytes are
  rejected.
- The module name defaults to the file name without `.ks` and must be a valid
  identifier; otherwise use `as`.
- Only functions declared `pub fn` can be called from other modules. Module
  functions are always called qualified (`name.fn()`); there is no
  unqualified import.
- Each module is loaded once even if imported several times. Import cycles are
  reported as errors.
- There is no package manager, no search path, and no standard library beyond
  the builtins above.

## C header imports

```
import c "stdlib.h" {
    fn llabs(n: int) -> int;
}
```

This is a deliberately narrow mechanism, not a general foreign function
interface.

- The header name must match `[A-Za-z0-9_][A-Za-z0-9_./-]*\.h` with no `.` or
  `..` path segments. It is emitted as `#include <name>`, so only headers on the
  C compiler's system include path can be used. No extra include paths or
  libraries are passed (except `-lm`).
- Each declared function may only use `int` and `bool` parameters and may
  return `int`, `bool`, or nothing. Kestrel calls it through a generated C
  wrapper, so the C compiler checks the call against the real prototype in the
  header. A missing declaration, or a pointer/integer mismatch, fails the build
  with a C compiler error. Narrowing numeric conversions (for example passing a
  64-bit `int` to a C `int` parameter) are *not* detected and follow C rules.
- Declared C functions are callable unqualified in the importing file only.
  Names starting with `_` or `ks_`, and `main`, are reserved.
- Strings, pointers, structs, callbacks, variadic functions, macros, and
  linking against libraries other than libc and libm are not supported.

## Compilation model

The compiler (written in Go, standard library only) lexes, parses, and type
checks all modules, generates a single C11 translation unit, and invokes a C
compiler to produce a native executable. The C compiler is chosen from the
`--cc` flag, then the `CC` environment variable, then the first of `cc`, `gcc`,
`clang` found on `PATH`. The generated code relies on the
`__builtin_*_overflow` intrinsics, which GCC and Clang provide.

## Not supported

Floating point numbers, characters, arrays/lists, maps, structs, enums,
generics, closures and first-class functions, methods, global variables,
constants, `for` loops, ternary expressions, compound assignment (`+=`),
string indexing or slicing, input (stdin, command-line arguments, files),
string escapes beyond those listed, error handling, garbage collection (string
concatenation and `str` allocate memory that is never freed), concurrency, Go
package interop, and arbitrary C FFI.
