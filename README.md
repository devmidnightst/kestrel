# kestrel

kestrel is a small, statically typed toy programming language. the compiler is
written in go (standard library only), translates kestrel to c, and calls an
installed c compiler (cc, gcc or clang) to produce a native executable.

this is an mvp for learning and experimenting. it is not production ready and
makes no performance promises.

## requirements

- go 1.22 or newer
- a c compiler on your path: cc, gcc or clang

## install

```sh
go build -o kestrel ./cmd/kestrel
```

## usage

```sh
kestrel build hello.ks -o hello   # compile to a native executable
kestrel run hello.ks              # compile to a temp dir and run it
kestrel check hello.ks            # parse and type check only
kestrel build hello.ks --emit-c hello.c   # also keep the generated c
```

`kestrel run` exits with the program's exit status. compile errors look like
`hello.ks:3:5: error: undefined: x`.

## a taste

```
import "lib/mathx.ks";

fn fact(n: int) -> int {
    if n <= 1 {
        return 1;
    }
    return n * fact(n - 1);
}

fn main() {
    let name = "kestrel";
    var i = 1;
    while i <= 5 {
        print(name, i, fact(i));
        i = i + 1;
    }
    print("gcd:", mathx.gcd(84, 36));
}
```

## what exists

- types: `int` (signed 64-bit), `bool`, `string`
- `let` (immutable) and `var` (mutable) bindings with lexical block scope
- arithmetic, comparisons, `&&` `||` `!`, parentheses, function calls
- functions with typed parameters and return types, recursion
- `if` / `else if` / `else`, `while`, `break`, `continue`, `return`, `print`
- builtins `len(string) -> int` and `str(int) -> string`
- string concatenation with `+` and comparison with `==` / `!=`
- checked integer overflow and division by zero (the program aborts with
  exit status 101)
- module imports: `import "lib/x.ks";` or `import "lib/x.ks" as y;`. paths are
  relative, must stay inside the entry file's directory (symlinks included),
  and only `pub fn` functions are visible to importers
- a narrow c header import for functions that take and return only `int` and
  `bool`:

  ```
  import c "stdlib.h" {
      fn llabs(n: int) -> int;
  }
  ```

## what does not exist

no floats, arrays, structs, maps, globals, closures, generics, `for` loops,
input, or garbage collection (strings built at runtime are never freed). the c
import is not a general ffi: no strings, pointers, structs, callbacks,
variadic functions, or extra libraries. there is no go package interop. see
[docs/spec.md](docs/spec.md) for the full, honest list.

## examples

```sh
kestrel run examples/variables.ks
kestrel run examples/functions.ks
kestrel run examples/branching.ks
kestrel run examples/loops.ks
kestrel run examples/modules/main.ks
kestrel run examples/cheader.ks
```

## development

```sh
gofmt -l .
go vet ./...
go test ./...
```

tests that compile native code are skipped when no c compiler is found.

## layout

- `cmd/kestrel`: command line entry point
- `internal/cli`: argument handling for build, run and check
- `internal/compiler`: lexer, parser, type checker, module loader, c code
  generator and build driver
- `examples`: sample programs
- `docs/spec.md`: language specification
