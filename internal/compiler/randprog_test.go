package compiler

import (
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

type randFunc struct {
	name   string
	params []Type
	result Type
}

type randProg struct {
	r       *rand.Rand
	sb      strings.Builder
	funcs   []randFunc
	scopes  [][]*Var
	counter int
	ind     int
}

var randTypes = []Type{IntType, BoolType, StringType}

func (g *randProg) line(format string, args ...any) {
	g.sb.WriteString(strings.Repeat("    ", g.ind))
	fmt.Fprintf(&g.sb, format, args...)
	g.sb.WriteString("\n")
}

func (g *randProg) fresh(prefix string) string {
	g.counter++
	return fmt.Sprintf("%s%d", prefix, g.counter)
}

func (g *randProg) visible(t Type, mutable bool) []*Var {
	var out []*Var
	seen := map[string]bool{}
	for i := len(g.scopes) - 1; i >= 0; i-- {
		for j := len(g.scopes[i]) - 1; j >= 0; j-- {
			v := g.scopes[i][j]
			if seen[v.Name] {
				continue
			}
			seen[v.Name] = true
			if v.Type == t && (!mutable || v.Mutable) {
				out = append(out, v)
			}
		}
	}
	return out
}

func (g *randProg) declare(name string, t Type, mutable bool) {
	g.scopes[len(g.scopes)-1] = append(g.scopes[len(g.scopes)-1], &Var{Name: name, Type: t, Mutable: mutable})
}

func (g *randProg) stringLit() string {
	pieces := []string{"a", "kestrel", " ", "\\n", "\\t", "\\\"", "\\\\", "?", "??=", "é", "%d", "%s", "0"}
	var sb strings.Builder
	for i := g.r.Intn(4); i > 0; i-- {
		sb.WriteString(pieces[g.r.Intn(len(pieces))])
	}
	return "\"" + sb.String() + "\""
}

func (g *randProg) expr(t Type, depth int) string {
	if depth <= 0 || g.r.Intn(4) == 0 {
		if vars := g.visible(t, false); len(vars) > 0 && g.r.Intn(3) > 0 {
			return vars[g.r.Intn(len(vars))].Name
		}
		switch t {
		case IntType:
			return fmt.Sprint(g.r.Intn(200) - 100)
		case BoolType:
			return fmt.Sprint(g.r.Intn(2) == 0)
		default:
			return g.stringLit()
		}
	}
	d := depth - 1
	if g.r.Intn(6) == 0 {
		var candidates []randFunc
		for _, f := range g.funcs {
			if f.result == t {
				candidates = append(candidates, f)
			}
		}
		if len(candidates) > 0 {
			f := candidates[g.r.Intn(len(candidates))]
			args := make([]string, len(f.params))
			for i, p := range f.params {
				args[i] = g.expr(p, d)
			}
			return fmt.Sprintf("%s(%s)", f.name, strings.Join(args, ", "))
		}
	}
	switch t {
	case IntType:
		switch g.r.Intn(5) {
		case 0:
			return fmt.Sprintf("len(%s)", g.expr(StringType, d))
		case 1:
			return fmt.Sprintf("-%s", g.expr(IntType, d))
		default:
			op := []string{"+", "-", "*", "/", "%"}[g.r.Intn(5)]
			return fmt.Sprintf("(%s %s %s)", g.expr(IntType, d), op, g.expr(IntType, d))
		}
	case BoolType:
		switch g.r.Intn(5) {
		case 0:
			return fmt.Sprintf("!%s", g.expr(BoolType, d))
		case 1:
			op := []string{"&&", "||"}[g.r.Intn(2)]
			return fmt.Sprintf("(%s %s %s)", g.expr(BoolType, d), op, g.expr(BoolType, d))
		case 2:
			ty := randTypes[g.r.Intn(3)]
			op := []string{"==", "!="}[g.r.Intn(2)]
			return fmt.Sprintf("(%s %s %s)", g.expr(ty, d), op, g.expr(ty, d))
		default:
			op := []string{"<", "<=", ">", ">="}[g.r.Intn(4)]
			return fmt.Sprintf("(%s %s %s)", g.expr(IntType, d), op, g.expr(IntType, d))
		}
	default:
		if g.r.Intn(3) == 0 {
			return fmt.Sprintf("str(%s)", g.expr(IntType, d))
		}
		return fmt.Sprintf("(%s + %s)", g.expr(StringType, d), g.expr(StringType, d))
	}
}

func (g *randProg) block(depth int, result Type, loop bool) {
	g.scopes = append(g.scopes, nil)
	g.body(depth, result, loop)
	g.scopes = g.scopes[:len(g.scopes)-1]
}

func (g *randProg) body(depth int, result Type, loop bool) {
	g.ind++
	for n := g.r.Intn(5); n > 0; n-- {
		g.stmt(depth, result, loop)
	}
	g.ind--
}

func (g *randProg) stmt(depth int, result Type, loop bool) {
	choice := g.r.Intn(10)
	if depth <= 0 && choice >= 5 {
		choice = g.r.Intn(5)
	}
	switch choice {
	case 0, 1:
		t := randTypes[g.r.Intn(3)]
		mutable := g.r.Intn(2) == 0
		name := g.fresh("v")
		if g.r.Intn(4) == 0 {
			if outer := g.visible(t, false); len(outer) > 0 {
				name = outer[g.r.Intn(len(outer))].Name
				for _, v := range g.scopes[len(g.scopes)-1] {
					if v.Name == name {
						name = g.fresh("v")
						break
					}
				}
			}
		}
		kw := "let"
		if mutable {
			kw = "var"
		}
		value := g.expr(t, 3)
		if g.r.Intn(2) == 0 {
			g.line("%s %s: %s = %s;", kw, name, t, value)
		} else {
			g.line("%s %s = %s;", kw, name, value)
		}
		g.declare(name, t, mutable)
	case 2:
		t := randTypes[g.r.Intn(3)]
		if vars := g.visible(t, true); len(vars) > 0 {
			v := vars[g.r.Intn(len(vars))]
			g.line("%s = %s;", v.Name, g.expr(t, 3))
			return
		}
		fallthrough
	case 3, 4:
		args := make([]string, g.r.Intn(3)+1)
		for i := range args {
			args[i] = g.expr(randTypes[g.r.Intn(3)], 3)
		}
		g.line("print(%s);", strings.Join(args, ", "))
	case 5, 6:
		g.line("if %s {", g.expr(BoolType, 3))
		g.block(depth-1, result, loop)
		if g.r.Intn(2) == 0 {
			g.line("} else {")
			g.block(depth-1, result, loop)
		}
		g.line("}")
	case 7:
		counter := g.fresh("i")
		g.line("var %s = 0;", counter)
		g.line("while %s < %d && %s {", counter, g.r.Intn(5)+1, g.expr(BoolType, 2))
		g.ind++
		g.line("%s = %s + 1;", counter, counter)
		g.ind--
		g.block(depth-1, result, true)
		g.line("}")
	case 8:
		if loop && g.r.Intn(2) == 0 {
			g.line("%s;", []string{"break", "continue"}[g.r.Intn(2)])
			return
		}
		g.line("{")
		g.block(depth-1, result, loop)
		g.line("}")
	case 9:
		if result == Void {
			g.line("return;")
		} else {
			g.line("return %s;", g.expr(result, 3))
		}
	}
}

func generateRandomProgram(seed int64) string {
	g := &randProg{r: rand.New(rand.NewSource(seed))}
	for n := g.r.Intn(4); n > 0; n-- {
		f := randFunc{name: g.fresh("f"), result: append([]Type{Void}, randTypes...)[g.r.Intn(4)]}
		g.scopes = [][]*Var{nil}
		params := make([]string, g.r.Intn(4))
		for i := range params {
			t := randTypes[g.r.Intn(3)]
			name := g.fresh("p")
			params[i] = fmt.Sprintf("%s: %s", name, t)
			f.params = append(f.params, t)
			g.declare(name, t, false)
		}
		sig := fmt.Sprintf("fn %s(%s)", f.name, strings.Join(params, ", "))
		if f.result != Void {
			sig += " -> " + f.result.String()
		}
		g.line("%s {", sig)
		g.body(3, f.result, false)
		g.ind++
		if f.result != Void {
			g.line("return %s;", g.expr(f.result, 2))
		}
		g.ind--
		g.line("}")
		g.funcs = append(g.funcs, f)
	}
	g.scopes = [][]*Var{nil}
	g.line("fn main() {")
	g.body(4, Void, false)
	g.line("}")
	return g.sb.String()
}

func TestRandomProgramsCompileAndRun(t *testing.T) {
	requireCC(t)
	count := 40
	if testing.Short() {
		count = 10
	}
	if n, err := strconv.Atoi(os.Getenv("KESTREL_RANDOM_PROGRAMS")); err == nil && n > 0 {
		count = n
	}
	for seed := int64(1); seed <= int64(count); seed++ {
		src := generateRandomProgram(seed)
		if _, err := CheckSource("rand.ks", []byte(src)); err != nil {
			t.Fatalf("seed %d: generated program failed to check: %v\n%s", seed, err, src)
		}
		dir := writeFiles(t, map[string]string{"main.ks": src})
		_, errOut, code := buildAndRun(t, filepath.Join(dir, "main.ks"))
		if code != 0 && !(code == 101 && strings.HasPrefix(errOut, "kestrel: runtime error: ")) {
			t.Fatalf("seed %d: exit %d, stderr %q\n%s", seed, code, errOut, src)
		}
	}
}
