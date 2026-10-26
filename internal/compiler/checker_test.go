package compiler

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func checkErrors(t *testing.T, src string) []string {
	t.Helper()
	_, err := CheckSource("c.ks", []byte(src))
	if err == nil {
		return nil
	}
	var list ErrorList
	if !errors.As(err, &list) {
		t.Fatalf("expected type errors, got %v", err)
	}
	out := make([]string, len(list))
	for i, e := range list {
		out[i] = e.Error()
	}
	return out
}

func expectError(t *testing.T, src, want string) {
	t.Helper()
	errs := checkErrors(t, src)
	for _, e := range errs {
		if strings.Contains(e, want) {
			return
		}
	}
	t.Errorf("expected error containing %q\nsource:\n%s\ngot: %q", want, src, errs)
}

func expectOK(t *testing.T, src string) {
	t.Helper()
	if errs := checkErrors(t, src); len(errs) > 0 {
		t.Errorf("unexpected errors for\n%s\n%s", src, strings.Join(errs, "\n"))
	}
}

func TestTypeErrors(t *testing.T) {
	cases := []struct {
		src  string
		want string
	}{
		{"fn main() { let x: int = \"s\"; }", "c.ks:1:26: error: cannot initialize x of type int with value of type string"},
		{"fn main() { let x = 1 + true; }", "c.ks:1:23: error: operator + is not defined for int and bool"},
		{"fn main() { let x = \"a\" - \"b\"; }", "operator - is not defined for string and string"},
		{"fn main() { let x = 1 < \"b\"; }", "operator < is not defined for int and string"},
		{"fn main() { let x = 1 == true; }", "operator == is not defined for int and bool"},
		{"fn main() { let x = 1 && 2; }", "operator && is not defined for int and int"},
		{"fn main() { let x = !1; }", "operator ! is not defined for int"},
		{"fn main() { let x = -true; }", "operator - is not defined for bool"},
		{"fn main() { if 1 { } }", "c.ks:1:16: error: if condition must be bool, not int"},
		{"fn main() { while \"x\" { } }", "while condition must be bool, not string"},
		{"fn f() -> int { return true; }", "cannot return value of type bool from function f (returns int)"},
		{"fn f() { return 1; } fn main() {}", "function f does not return a value"},
		{"fn f() -> int { return; } fn main() {}", "missing return value (function f returns int)"},
		{"fn f(a: int) -> int { if a > 0 { return 1; } }\nfn main() {}", "c.ks:1:46: error: missing return at end of function f (returns int)"},
		{"fn f(a: int) {} fn main() { f(\"x\"); }", "argument 1 of f must be int, not string"},
		{"fn f(a: int) {} fn main() { f(); }", "f expects 1 argument, got 0"},
		{"fn f() {} fn main() { let x = f(); }", "function call does not produce a value"},
		{"fn f() {} fn main() { print(f()); }", "function call does not produce a value"},
		{"fn main() { let x: double = 1; }", "unknown type double"},
		{"fn main() { let x: float = 1; }", "cannot initialize x of type float with value of type int"},
		{"fn main() { nope(); }", "undefined function: nope"},
		{"fn main() { let x = y; }", "undefined: y"},
		{"fn f() {} fn main() { let x = f; }", "function f used as a value"},
		{"fn main() { let f = 1; f(); }", "f is a variable of type int, not a function"},
		{"fn f() {} fn f() {} fn main() {}", "function f already declared at 1:4"},
		{"fn f(a: int, a: int) {} fn main() {}", "duplicate parameter a"},
		{"fn main() { let n = len(5); }", "len expects a string or an array, not int"},
		{"fn main() { break; }", "break outside of a while loop"},
		{"fn main() { continue; }", "continue outside of a while loop"},
		{"fn other() {}", "entry file has no function main"},
		{"fn main(a: int) {}", "main must not take parameters"},
		{"fn main() -> string { return \"\"; }", "main must return nothing or int, not string"},
		{"fn main() { m.f(); }", "c.ks:1:13: error: undefined: m"},
		{"import c \"stdlib.h\" { fn getenv(name: string) -> int; }\nfn main() {}", "c functions may only use int, bool and float parameters, not string"},
		{"import c \"../etc/x.h\" { fn f() -> int; }\nfn main() {}", "invalid c header name \"../etc/x.h\""},
		{"import c \"stdlib.h\" { fn ks_add(a: int) -> int; }\nfn main() {}", "c function name ks_add is reserved"},
	}
	for _, c := range cases {
		expectError(t, c.src, c.want)
	}
}

func TestMultipleErrorsAreReportedInOrder(t *testing.T) {
	errs := checkErrors(t, "fn main() {\n  let a: int = true;\n  let b = a + \"x\";\n  undefinedcall();\n}")
	if len(errs) != 3 {
		t.Fatalf("got %d errors: %q", len(errs), errs)
	}
	for i, line := range []string{"c.ks:2:", "c.ks:3:", "c.ks:4:"} {
		if !strings.HasPrefix(errs[i], line) {
			t.Errorf("error %d = %q, want prefix %q", i, errs[i], line)
		}
	}
}

func TestInvalidOperandsDoNotCascade(t *testing.T) {
	errs := checkErrors(t, "fn main() { let x = (y + 1) * 2 < 3 && true; print(x + 1); }")
	if len(errs) != 1 || !strings.Contains(errs[0], "undefined: y") {
		t.Errorf("expected a single undefined error, got %q", errs)
	}
}

func TestScoping(t *testing.T) {
	expectError(t, "fn main() { let x = 1; x = 2; }", "cannot assign to x: x is immutable (declared with let at 1:17; use var to allow assignment)")
	expectError(t, "fn main() { { let inner = 1; } print(inner); }", "undefined: inner")
	expectError(t, "fn main() { if true { var z = 1; } z = 2; }", "undefined: z")
	expectError(t, "fn main() { while false { let w = 1; } print(w); }", "undefined: w")
	expectError(t, "fn main() { let x = 1; let x = 2; }", "x already declared in this scope at 1:17")
	expectError(t, "fn f(a: int) { let a = 2; } fn main() {}", "a already declared in this scope")
	expectError(t, "fn main() { var s = \"a\"; s = 1; }", "cannot assign value of type int to s of type string")
	expectError(t, "fn main() { let x = x; }", "undefined: x")
	expectError(t, "fn f() { print(secret); } fn main() { let secret = 1; f(); }", "undefined: secret")
	expectError(t, "fn main() { undeclared = 1; }", "undefined: undeclared")
	expectOK(t, "fn main() { let x = 1; { let x = \"shadow\"; print(x); } print(x + 1); }")
	expectOK(t, "fn main() { var x = 1; { x = x + 1; } print(x); }")
	expectOK(t, "fn main() { let x = 1; { let x = x + 1; print(x); } }")
	expectOK(t, "fn f(a: int) -> int { if a > 0 { return 1; } else { return 2; } } fn main() { print(f(1)); }")
	expectOK(t, "fn f(a: int) -> int { if a > 0 { return 1; } else if a < 0 { return -1; } else { return 0; } } fn main() { print(f(1)); }")
	expectOK(t, "fn later() -> int { return earlier(); } fn earlier() -> int { return 1; } fn main() { print(later()); }")
}

func TestScopedVariablesGetDistinctNames(t *testing.T) {
	prog, err := CheckSource("c.ks", []byte("fn main() { let x = 1; { let x = 2; print(x); } print(x); }"))
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, v := range prog.Info.Defs {
		if names[v.CName] {
			t.Errorf("duplicate c name %s", v.CName)
		}
		names[v.CName] = true
	}
	if len(names) != 2 {
		t.Errorf("expected 2 distinct bindings, got %v", names)
	}
}

func writeFiles(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, content := range files {
		p := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestImports(t *testing.T) {
	dir := writeFiles(t, map[string]string{
		"proj/main.ks":     "import \"lib/a.ks\";\nimport \"lib/b.ks\" as bee;\nfn main() { print(a.one() + bee.two()); }",
		"proj/lib/a.ks":    "import \"b.ks\";\npub fn one() -> int { return b.two() - 1; }",
		"proj/lib/b.ks":    "pub fn two() -> int { return helper(); }\nfn helper() -> int { return 2; }",
		"proj/private.ks":  "import \"lib/b.ks\";\nfn main() { print(b.helper()); }",
		"proj/escape.ks":   "import \"../outside.ks\";\nfn main() {}",
		"proj/abs.ks":      "import \"/etc/passwd.ks\";\nfn main() {}",
		"proj/missing.ks":  "import \"nope.ks\";\nfn main() {}",
		"proj/ext.ks":      "import \"lib/b.txt\";\nfn main() {}",
		"proj/cycle.ks":    "import \"c1.ks\";\nfn main() {}",
		"proj/c1.ks":       "import \"c2.ks\";\npub fn f() {}",
		"proj/c2.ks":       "import \"c1.ks\";\npub fn g() {}",
		"proj/dup.ks":      "import \"lib/b.ks\";\nimport \"lib/a.ks\" as b;\nfn main() {}",
		"proj/badname.ks":  "import \"my-mod.ks\";\nfn main() {}",
		"proj/my-mod.ks":   "pub fn f() {}",
		"proj/nofn.ks":     "import \"lib/b.ks\";\nfn main() { b.three(); }",
		"proj/modvalue.ks": "import \"lib/b.ks\";\nfn main() { let x = b; }",
		"outside.ks":       "pub fn f() {}",
	})
	proj := filepath.Join(dir, "proj")
	if _, err := Load(filepath.Join(proj, "main.ks")); err != nil {
		t.Fatalf("valid import program failed: %v", err)
	}
	bad := map[string]string{
		"private.ks":  "b.helper is not public (declare it with pub fn)",
		"escape.ks":   "import path \"../outside.ks\" escapes the project directory",
		"abs.ks":      "import path \"/etc/passwd.ks\" must be relative",
		"missing.ks":  "cannot find module \"nope.ks\"",
		"ext.ks":      "import path \"lib/b.txt\" must name a .ks file",
		"cycle.ks":    "import cycle:",
		"dup.ks":      "module name b is imported more than once",
		"badname.ks":  "module name \"my-mod\" is not a valid identifier; add `as name`",
		"nofn.ks":     "undefined: b.three",
		"modvalue.ks": "module b used as a value",
	}
	for file, want := range bad {
		_, err := Load(filepath.Join(proj, file))
		if err == nil {
			t.Errorf("%s: expected error containing %q", file, want)
			continue
		}
		if !strings.Contains(err.Error(), want) {
			t.Errorf("%s: got %q, want %q", file, err.Error(), want)
		}
	}
}

func TestImportErrorsPointAtImportedFile(t *testing.T) {
	dir := writeFiles(t, map[string]string{
		"main.ks":  "import \"lib/m.ks\";\nfn main() { m.f(); }",
		"lib/m.ks": "pub fn f() {\n  let x: int = \"oops\";\n}",
	})
	_, err := Load(filepath.Join(dir, "main.ks"))
	if err == nil || !strings.Contains(err.Error(), filepath.Join("lib", "m.ks")+":2:16: error: cannot initialize x") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestImportSymlinkEscape(t *testing.T) {
	outer := writeFiles(t, map[string]string{"m.ks": "pub fn f() {}"})
	dir := writeFiles(t, map[string]string{"main.ks": "import \"link/m.ks\";\nfn main() { m.f(); }"})
	if err := os.Symlink(outer, filepath.Join(dir, "link")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	_, err := Load(filepath.Join(dir, "main.ks"))
	if err == nil || !strings.Contains(err.Error(), "escapes the project directory") {
		t.Errorf("expected symlink escape to be rejected, got %v", err)
	}
}

func TestNewTypeErrors(t *testing.T) {
	cases := []struct {
		src  string
		want string
	}{
		{"fn main() { let a = []; }", "cannot infer the element type of an empty array literal"},
		{"fn main() { let a = [1, \"x\"]; }", "array element 2 has type string, expected int"},
		{"fn main() { let a = [1]; a[0] = 2; }", "cannot assign to a[...]: a is immutable"},
		{"fn main() { let a = [1]; push(a, 2); }", "cannot assign to a: a is immutable"},
		{"fn f(a: [int]) { a[0] = 1; } fn main() {}", "a is a parameter"},
		{"fn main() { let s = \"abc\"; print(s[0]); }", "cannot index a string; use byte_at(s, i) or substr(s, start, count)"},
		{"fn main() { var s = \"abc\"; s[0] = \"x\"; }", "cannot assign into a string; strings are immutable"},
		{"fn main() { let a = [1]; print(a[true]); }", "array index must be int, not bool"},
		{"fn main() { let x = 5; print(x[0]); }", "cannot index a value of type int"},
		{"fn main() { print(1 + 1.0); }", "operator + is not defined for int and float (convert explicitly with float(...) or int(...))"},
		{"fn main() { print(5 % 2.0); }", "operator % is not defined for int and float"},
		{"fn main() { print(5.0 % 2.0); }", "operator % is not defined for float and float"},
		{"fn main() { print([1] == [1]); }", "operator == is not defined for [int] (only int, bool, float and string values can be compared)"},
		{"fn main() { var a = [1]; print(pop(a) + 1); }", "pop changes its array argument"},
		{"fn main() { var a = [1]; print(len(a), push(a, 2)); }", "push changes its array argument"},
		{"fn main() { var x = 1; push(x, 2); }", "push expects an array, not int"},
		{"fn main() { var a = [1]; push(a, \"s\"); }", "cannot push string onto [int]"},
		{"fn main() { push([1], 2); }", "the first argument of push must be a var, field or array element"},
		{"fn main() { print(len(5)); }", "len expects a string or an array, not int"},
		{"fn main() { print(str([1])); }", "str expects int, float, bool or string, not [int]"},
		{"fn main() { print(min(1, 2.0)); }", "min expects two ints or two floats, not int and float"},
		{"fn main() { print(slice(\"ab\", 0, 1)); }", "slice expects an array, not string (use substr for strings)"},
		{"fn main() { print(sqrt(4)); }", "argument 1 of sqrt must be float, not int"},
		{"fn main() { let x: [int] = [1.5]; }", "array element 1 has type float, expected int"},
		{"struct P { x: int } fn main() { let p = P { x: 1, y: 2 }; }", "struct P has no field y"},
		{"struct P { x: int, y: int } fn main() { let p = P { x: 1 }; }", "missing field in P literal: y"},
		{"struct P { x: int } fn main() { let p = P { x: 1, x: 2 }; }", "field x given more than once"},
		{"struct P { x: int } fn main() { let p = P { x: \"s\" }; }", "field x of P must be int, not string"},
		{"struct P { x: int } fn main() { let p = P { x: 1 }; print(p.z); }", "struct P has no field z"},
		{"fn main() { let a = [1]; print(a.len); }", "type [int] has no field len (use len(...) for the length of a [int])"},
		{"fn main() { let p = Q { x: 1 }; }", "unknown struct Q"},
		{"struct P { x: int, x: int } fn main() {}", "field x already declared"},
		{"struct P { x: Missing } fn main() {}", "unknown type Missing"},
		{"struct P { x: int } struct P { y: int } fn main() {}", "struct P already declared"},
		{"struct int { x: int } fn main() {}", "cannot use builtin type name int as a struct name"},
		{"struct A { b: B } struct B { a: A } fn main() {}", "struct A contains itself through its fields"},
		{"struct A { kids: [A] } fn main() { let a = A { kids: [] }; print(a); }", ""},
		{"struct P { x: int } fn P() {} fn main() {}", "P already declared as a struct"},
		{"struct P { x: int } fn main() { print(P); }", "struct P used as a value"},
		{"struct P { x: int } fn main() { let p = P { x: 1 }; p.x = 2; }", "cannot assign to p.x: p is immutable"},
		{"struct P { x: int } fn main() { let p = P { x: 1 }; p.x(); }", "p.x is not a function"},
		{"fn main() { exit(\"x\"); }", "argument 1 of exit must be int, not string"},
		{"import c \"math.h\" { fn cbrt(x: float) -> float; }\nfn main() { print(cbrt(8.0)); }", ""},
		{"import c \"stdlib.h\" { fn f(s: [int]); }\nfn main() {}", "c functions may only use int, bool and float parameters, not [int]"},
	}
	for _, c := range cases {
		if c.want == "" {
			expectOK(t, c.src)
		} else {
			expectError(t, c.src, c.want)
		}
	}
}

func TestModuleStructs(t *testing.T) {
	dir := writeFiles(t, map[string]string{
		"main.ks":     "import \"geo.ks\";\nfn main() { let p = geo.Point { x: 1.0, y: 2.0 }; let q: geo.Point = geo.origin(); print(geo.dist(p, q)); }",
		"geo.ks":      "pub struct Point { x: float, y: float }\nstruct Hidden { a: int }\npub fn origin() -> Point { return Point { x: 0.0, y: 0.0 }; }\npub fn dist(a: Point, b: Point) -> float { return sqrt((a.x - b.x) * (a.x - b.x) + (a.y - b.y) * (a.y - b.y)); }",
		"private.ks":  "import \"geo.ks\";\nfn main() { let h = geo.Hidden { a: 1 }; }",
		"privtype.ks": "import \"geo.ks\";\nfn f(h: geo.Hidden) {}\nfn main() {}",
		"nomod.ks":    "fn main() { let h = nope.Thing { a: 1 }; }",
	})
	if _, err := Load(filepath.Join(dir, "main.ks")); err != nil {
		t.Fatalf("valid module struct program failed: %v", err)
	}
	for file, want := range map[string]string{
		"private.ks":  "struct geo.Hidden is not public (declare it with pub struct)",
		"privtype.ks": "struct geo.Hidden is not public",
		"nomod.ks":    "undefined module: nope",
	} {
		_, err := Load(filepath.Join(dir, file))
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: got %v, want %q", file, err, want)
		}
	}
}
