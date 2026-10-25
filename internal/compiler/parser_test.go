package compiler

import (
	"fmt"
	"strings"
	"testing"
)

func exprString(e Expr) string {
	switch e := e.(type) {
	case *IntLit:
		return e.Text
	case *StringLit:
		return fmt.Sprintf("%q", e.Value)
	case *BoolLit:
		return fmt.Sprint(e.Value)
	case *Ident:
		return e.Name
	case *UnaryExpr:
		return fmt.Sprintf("(%s%s)", e.Op, exprString(e.X))
	case *BinaryExpr:
		return fmt.Sprintf("(%s %s %s)", exprString(e.X), e.Op, exprString(e.Y))
	case *SelectorExpr:
		return e.X.Name + "." + e.Name
	case *CallExpr:
		args := make([]string, len(e.Args))
		for i, a := range e.Args {
			args[i] = exprString(a)
		}
		return fmt.Sprintf("%s(%s)", exprString(e.Fn), strings.Join(args, ", "))
	}
	return "?"
}

func parseExprForTest(t *testing.T, src string) Expr {
	t.Helper()
	f, err := Parse("p.ks", []byte("fn main() { let v = "+src+"; }"))
	if err != nil {
		t.Fatalf("%s: %v", src, err)
	}
	return f.Funcs[0].Body.Stmts[0].(*LetStmt).Value
}

func TestParsePrecedence(t *testing.T) {
	cases := map[string]string{
		"1 + 2 * 3":                 "(1 + (2 * 3))",
		"(1 + 2) * 3":               "((1 + 2) * 3)",
		"1 - 2 - 3":                 "((1 - 2) - 3)",
		"a < b == c > d":            "((a < b) == (c > d))",
		"a || b && c":               "(a || (b && c))",
		"!a && -b < 3":              "((!a) && ((-b) < 3))",
		"--x":                       "(-(-x))",
		"f(1, g(2) + 3) % 4":        "(f(1, (g(2) + 3)) % 4)",
		"m.add(1, 2) * 2":           "(m.add(1, 2) * 2)",
		"\"a\" + \"b\" != \"ab\"":   "((\"a\" + \"b\") != \"ab\")",
		"1 + 2 <= 3 * 4 || !true":   "(((1 + 2) <= (3 * 4)) || (!true))",
		"x / y / z":                 "((x / y) / z)",
		"a == b && c != d || false": "(((a == b) && (c != d)) || false)",
	}
	for src, want := range cases {
		if got := exprString(parseExprForTest(t, src)); got != want {
			t.Errorf("%s: got %s, want %s", src, got, want)
		}
	}
}

func TestParseProgramStructure(t *testing.T) {
	src := `import "lib/util.ks";
import "other.ks" as o;
import c "stdlib.h" {
    fn llabs(n: int) -> int;
}

pub fn add(a: int, b: int) -> int {
    return a + b;
}

fn main() {
    var i = 0;
    while i < 3 {
        if i == 1 {
            print("one");
        } else if i == 2 {
            continue;
        } else {
            print(i, true);
        }
        i = i + 1;
        { let inner: string = "x"; }
    }
    add(1, 2);
    return;
}
`
	f, err := Parse("s.ks", []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	if len(f.Imports) != 2 || f.Imports[0].Path != "lib/util.ks" || f.Imports[0].Alias != "" || f.Imports[1].Alias != "o" {
		t.Fatalf("unexpected imports: %+v", f.Imports)
	}
	if len(f.CImports) != 1 || f.CImports[0].Header != "stdlib.h" || len(f.CImports[0].Externs) != 1 || f.CImports[0].Externs[0].Name != "llabs" {
		t.Fatalf("unexpected c imports: %+v", f.CImports)
	}
	if len(f.Funcs) != 2 {
		t.Fatalf("got %d funcs", len(f.Funcs))
	}
	add := f.Funcs[0]
	if !add.Pub || add.Name != "add" || len(add.Params) != 2 || add.Result.Name != "int" || add.Params[1].Type.Name != "int" {
		t.Fatalf("bad add signature: %+v", add)
	}
	main := f.Funcs[1]
	if main.Pub || main.Result != nil || len(main.Body.Stmts) != 4 {
		t.Fatalf("bad main: %+v", main)
	}
	decl := main.Body.Stmts[0].(*LetStmt)
	if !decl.Mutable || decl.Name != "i" || decl.NamePos.Line != 12 || decl.NamePos.Col != 9 {
		t.Errorf("bad var decl: %+v", decl)
	}
	loop := main.Body.Stmts[1].(*WhileStmt)
	ifs := loop.Body.Stmts[0].(*IfStmt)
	elif, ok := ifs.Else.(*IfStmt)
	if !ok {
		t.Fatalf("else if not parsed as nested if: %T", ifs.Else)
	}
	if _, ok := elif.Else.(*BlockStmt); !ok {
		t.Fatalf("final else not a block: %T", elif.Else)
	}
	if _, ok := elif.Then.Stmts[0].(*ContinueStmt); !ok {
		t.Errorf("expected continue")
	}
	if p := elif.Else.(*BlockStmt).Block.Stmts[0].(*PrintStmt); len(p.Args) != 2 {
		t.Errorf("print args: %d", len(p.Args))
	}
	if a := loop.Body.Stmts[1].(*AssignStmt); a.Name != "i" {
		t.Errorf("bad assignment %+v", a)
	}
	if _, ok := loop.Body.Stmts[2].(*BlockStmt); !ok {
		t.Errorf("expected nested block")
	}
	if _, ok := main.Body.Stmts[2].(*ExprStmt); !ok {
		t.Errorf("expected call statement")
	}
	if r := main.Body.Stmts[3].(*ReturnStmt); r.Value != nil {
		t.Errorf("expected bare return")
	}
}

func TestParseErrors(t *testing.T) {
	cases := []struct {
		src  string
		want string
	}{
		{"fn main() {\n  let x = 1 +;\n}", "p.ks:2:14: error: expected expression, found \";\""},
		{"fn main() {\n  let x = 1\n}", "p.ks:3:1: error: expected \";\" after variable declaration, found \"}\""},
		{"fn main() {\n  var x;\n}", "p.ks:2:8: error: expected \"=\" in variable declaration (variables must be initialized)"},
		{"fn main() {\n  1 + 2;\n}", "p.ks:2:3: error: expression result is unused"},
		{"fn main() {\n  f() = 3;\n}", "p.ks:2:7: error: only a plain variable name can be assigned to"},
		{"fn main() {\n  print(1;\n}", "p.ks:2:10: error: expected \")\" to close argument list"},
		{"fn main() {\n  if x { }", "p.ks:2:11: error: unexpected end of file, expected \"}\" to close block opened at 1:11"},
		{"let x = 1;", "p.ks:1:1: error: expected function declaration, found \"let\""},
		{"fn main() {}\nimport \"a.ks\";", "p.ks:2:1: error: imports must appear before all function declarations"},
		{"fn f(a int) {}", "p.ks:1:8: error: expected \":\" after parameter name"},
		{"fn main() { let v = (1 + 2)(3); }", "p.ks:1:28: error: only named functions can be called"},
		{"import lib;", "p.ks:1:8: error: expected module path string after import, found identifier \"lib\""},
		{"import c \"stdio.h\" { fn puts(); ", "p.ks:1:33: error: unterminated c import block"},
	}
	for _, c := range cases {
		_, err := Parse("p.ks", []byte(c.src))
		if err == nil {
			t.Errorf("%q: expected error", c.src)
			continue
		}
		if !strings.Contains(err.Error(), c.want) {
			t.Errorf("%q:\n got  %q\n want %q", c.src, err.Error(), c.want)
		}
	}
}
