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

var (
	randIntArr = ArrayOf(IntType)
	randStrArr = ArrayOf(StringType)
	randPair   = &TypeDesc{Kind: KStruct, Struct: &StructInfo{Name: "Pair"}}
	randPairs  = ArrayOf(randPair)
	randTypes  = []Type{IntType, BoolType, FloatType, StringType, randIntArr, randStrArr, randPair, randPairs}
)

const randPrelude = "struct Pair {\n    a: int,\n    b: string,\n    items: [int],\n}\n\n"

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

func (g *randProg) line(format string, args ...any) {
	g.sb.WriteString(strings.Repeat("    ", g.ind))
	fmt.Fprintf(&g.sb, format, args...)
	g.sb.WriteString("\n")
}

func (g *randProg) fresh(prefix string) string {
	g.counter++
	return fmt.Sprintf("%s%d", prefix, g.counter)
}

func (g *randProg) anyType() Type {
	return randTypes[g.r.Intn(len(randTypes))]
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
			if (t == nil || v.Type == t) && (!mutable || v.Mutable) {
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
	pieces := []string{"a", "kestrel", " ", ",", "\\n", "\\t", "\\\"", "\\\\", "?", "??=", "é", "%d", "%s", "0", "12"}
	var sb strings.Builder
	for i := g.r.Intn(4); i > 0; i-- {
		sb.WriteString(pieces[g.r.Intn(len(pieces))])
	}
	return "\"" + sb.String() + "\""
}

func (g *randProg) index(arr string, d int) string {
	return fmt.Sprintf("%s[abs(%s) %% max(len(%s), 1)]", arr, g.expr(IntType, d), arr)
}

func (g *randProg) leaf(t Type) string {
	if vars := g.visible(t, false); len(vars) > 0 && g.r.Intn(3) > 0 {
		return vars[g.r.Intn(len(vars))].Name
	}
	switch t {
	case IntType:
		return fmt.Sprint(g.r.Intn(200) - 100)
	case BoolType:
		return fmt.Sprint(g.r.Intn(2) == 0)
	case FloatType:
		return []string{"0.5", "1.0", "2.25", "1e3", "3.0e-2", "100.125"}[g.r.Intn(6)]
	case StringType:
		return g.stringLit()
	case randIntArr:
		return fmt.Sprintf("[%d, %d]", g.r.Intn(9), g.r.Intn(9))
	case randStrArr:
		return fmt.Sprintf("[%s]", g.stringLit())
	case randPair:
		return fmt.Sprintf("Pair { a: %d, b: %s, items: [%d] }", g.r.Intn(9), g.stringLit(), g.r.Intn(9))
	}
	return fmt.Sprintf("[%s]", g.leaf(randPair))
}

func (g *randProg) expr(t Type, depth int) string {
	if depth <= 0 || g.r.Intn(4) == 0 {
		return g.leaf(t)
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
		switch g.r.Intn(8) {
		case 0:
			return fmt.Sprintf("len(%s)", g.expr(StringType, d))
		case 1:
			return fmt.Sprintf("len(%s)", g.expr(randIntArr, d))
		case 2:
			return g.index(g.expr(randIntArr, d), d)
		case 3:
			return fmt.Sprintf("(%s).a", g.expr(randPair, d))
		case 4:
			return fmt.Sprintf("-%s", g.expr(IntType, d))
		default:
			op := []string{"+", "-", "*", "/", "%"}[g.r.Intn(5)]
			return fmt.Sprintf("(%s %s %s)", g.expr(IntType, d), op, g.expr(IntType, d))
		}
	case BoolType:
		switch g.r.Intn(6) {
		case 0:
			return fmt.Sprintf("!%s", g.expr(BoolType, d))
		case 1:
			op := []string{"&&", "||"}[g.r.Intn(2)]
			return fmt.Sprintf("(%s %s %s)", g.expr(BoolType, d), op, g.expr(BoolType, d))
		case 2:
			ty := []Type{IntType, BoolType, FloatType, StringType}[g.r.Intn(4)]
			op := []string{"==", "!="}[g.r.Intn(2)]
			return fmt.Sprintf("(%s %s %s)", g.expr(ty, d), op, g.expr(ty, d))
		case 3:
			return fmt.Sprintf("contains(%s, %s)", g.expr(StringType, d), g.expr(StringType, d))
		default:
			ty := []Type{IntType, FloatType, StringType}[g.r.Intn(3)]
			op := []string{"<", "<=", ">", ">="}[g.r.Intn(4)]
			return fmt.Sprintf("(%s %s %s)", g.expr(ty, d), op, g.expr(ty, d))
		}
	case FloatType:
		switch g.r.Intn(4) {
		case 0:
			return fmt.Sprintf("float(%s)", g.expr(IntType, d))
		case 1:
			return fmt.Sprintf("sqrt(abs(%s))", g.expr(FloatType, d))
		default:
			op := []string{"+", "-", "*", "/"}[g.r.Intn(4)]
			return fmt.Sprintf("(%s %s %s)", g.expr(FloatType, d), op, g.expr(FloatType, d))
		}
	case StringType:
		switch g.r.Intn(6) {
		case 0:
			return fmt.Sprintf("str(%s)", g.expr([]Type{IntType, FloatType, BoolType}[g.r.Intn(3)], d))
		case 1:
			return g.index(g.expr(randStrArr, d), d)
		case 2:
			return fmt.Sprintf("join(%s, %s)", g.expr(randStrArr, d), g.expr(StringType, d))
		case 3:
			return fmt.Sprintf("(%s).b", g.expr(randPair, d))
		default:
			return fmt.Sprintf("(%s + %s)", g.expr(StringType, d), g.expr(StringType, d))
		}
	case randIntArr:
		switch g.r.Intn(4) {
		case 0:
			return fmt.Sprintf("fill(%s, %d)", g.expr(IntType, d), g.r.Intn(4))
		case 1:
			return fmt.Sprintf("(%s).items", g.expr(randPair, d))
		default:
			return fmt.Sprintf("[%s, %s]", g.expr(IntType, d), g.expr(IntType, d))
		}
	case randStrArr:
		if g.r.Intn(2) == 0 {
			return fmt.Sprintf("split(%s, \",\")", g.expr(StringType, d))
		}
		return fmt.Sprintf("[%s, %s]", g.expr(StringType, d), g.expr(StringType, d))
	case randPair:
		if g.r.Intn(3) == 0 {
			return g.index(g.expr(randPairs, d), d)
		}
		return fmt.Sprintf("Pair { items: %s, b: %s, a: %s }", g.expr(randIntArr, d), g.expr(StringType, d), g.expr(IntType, d))
	}
	if g.r.Intn(2) == 0 {
		return fmt.Sprintf("fill(%s, %d)", g.expr(randPair, d), g.r.Intn(3))
	}
	return fmt.Sprintf("[%s]", g.expr(randPair, d))
}

func (g *randProg) block(depth int, result Type, loop bool) {
	g.scopes = append(g.scopes, nil)
	g.body(depth, result, loop)
	g.scopes = g.scopes[:len(g.scopes)-1]
}

func (g *randProg) body(depth int, result Type, loop bool) {
	g.ind++
	for n := g.r.Intn(6); n > 0; n-- {
		g.stmt(depth, result, loop)
	}
	g.ind--
}

func (g *randProg) place(depth int) (string, Type, bool) {
	vars := g.visible(nil, true)
	if len(vars) == 0 {
		return "", nil, false
	}
	v := vars[g.r.Intn(len(vars))]
	switch {
	case v.Type == randIntArr && g.r.Intn(2) == 0:
		return g.index(v.Name, depth), IntType, true
	case v.Type == randStrArr && g.r.Intn(2) == 0:
		return g.index(v.Name, depth), StringType, true
	case v.Type == randPair:
		switch g.r.Intn(4) {
		case 0:
			return v.Name + ".a", IntType, true
		case 1:
			return v.Name + ".b", StringType, true
		case 2:
			return g.index(v.Name+".items", depth), IntType, true
		}
		return v.Name + ".items", randIntArr, true
	case v.Type == randPairs && g.r.Intn(2) == 0:
		p := g.index(v.Name, depth)
		if g.r.Intn(2) == 0 {
			return p + ".b", StringType, true
		}
		return p + ".items", randIntArr, true
	}
	return v.Name, v.Type, true
}

func (g *randProg) stmt(depth int, result Type, loop bool) {
	choice := g.r.Intn(13)
	if depth <= 0 && choice >= 8 {
		choice = g.r.Intn(8)
	}
	switch choice {
	case 0, 1:
		t := g.anyType()
		mutable := g.r.Intn(2) == 0
		name := g.fresh("v")
		if g.r.Intn(3) == 0 {
			if outer := g.visible(t, false); len(outer) > 0 {
				cand := outer[g.r.Intn(len(outer))].Name
				clash := false
				for _, v := range g.scopes[len(g.scopes)-1] {
					clash = clash || v.Name == cand
				}
				if !clash {
					name = cand
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
	case 2, 3:
		if target, t, ok := g.place(2); ok {
			g.line("%s = %s;", target, g.expr(t, 3))
			return
		}
		fallthrough
	case 4:
		if target, t, ok := g.place(2); ok && t.Kind == KArray {
			if g.r.Intn(3) == 0 {
				name := g.fresh("v")
				g.line("let %s = pop(%s);", name, target)
				g.declare(name, t.Elem, false)
			} else {
				g.line("push(%s, %s);", target, g.expr(t.Elem, 2))
			}
			return
		}
		fallthrough
	case 5:
		if src := g.visible(nil, false); len(src) > 0 {
			v := src[g.r.Intn(len(src))]
			name := g.fresh("c")
			g.line("var %s = %s;", name, v.Name)
			g.declare(name, v.Type, true)
			return
		}
		fallthrough
	case 6, 7:
		args := make([]string, g.r.Intn(3)+1)
		for i := range args {
			args[i] = g.expr(g.anyType(), 3)
		}
		g.line("print(%s);", strings.Join(args, ", "))
	case 8, 9:
		g.line("if %s {", g.expr(BoolType, 3))
		g.block(depth-1, result, loop)
		if g.r.Intn(2) == 0 {
			g.line("} else {")
			g.block(depth-1, result, loop)
		}
		g.line("}")
	case 10:
		counter := g.fresh("i")
		g.line("var %s = 0;", counter)
		g.line("while %s < %d && %s {", counter, g.r.Intn(5)+1, g.expr(BoolType, 2))
		g.ind++
		g.line("%s = %s + 1;", counter, counter)
		g.ind--
		g.declare(counter, IntType, false)
		g.block(depth-1, result, true)
		g.line("}")
	case 11:
		if loop && g.r.Intn(2) == 0 {
			g.line("%s;", []string{"break", "continue"}[g.r.Intn(2)])
			return
		}
		g.line("{")
		g.block(depth-1, result, loop)
		g.line("}")
	case 12:
		if result == Void {
			g.line("return;")
		} else {
			g.line("return %s;", g.expr(result, 3))
		}
	}
}

func generateRandomProgram(seed int64) string {
	g := &randProg{r: rand.New(rand.NewSource(seed))}
	g.sb.WriteString(randPrelude)
	for n := g.r.Intn(4); n > 0; n-- {
		f := randFunc{name: g.fresh("f")}
		if g.r.Intn(4) > 0 {
			f.result = g.anyType()
		} else {
			f.result = Void
		}
		g.scopes = [][]*Var{nil}
		params := make([]string, g.r.Intn(4))
		for i := range params {
			t := g.anyType()
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

func randomProgramCount(t *testing.T, def int) int {
	if testing.Short() {
		def = def / 4
	}
	if n, err := strconv.Atoi(os.Getenv("KESTREL_RANDOM_PROGRAMS")); err == nil && n > 0 {
		def = n
	}
	return def
}

func checkRandomPrograms(t *testing.T, count int, sanitize bool) {
	for seed := int64(1); seed <= int64(count); seed++ {
		src := generateRandomProgram(seed)
		if _, err := CheckSource("rand.ks", []byte(src)); err != nil {
			t.Fatalf("seed %d: generated program failed to check: %v\n%s", seed, err, src)
		}
		dir := writeFiles(t, map[string]string{"main.ks": src})
		_, errOut, code := buildAndRunOpts(t, filepath.Join(dir, "main.ks"), BuildOptions{Sanitize: sanitize})
		if code != 0 && !(code == 101 && strings.HasPrefix(errOut, "kestrel: runtime error: ")) {
			t.Fatalf("seed %d: exit %d, stderr %q\n%s", seed, code, errOut, src)
		}
		if strings.Contains(errOut, "Sanitizer") {
			t.Fatalf("seed %d: sanitizer report:\n%s\n%s", seed, errOut, src)
		}
	}
}

func TestRandomProgramsCompileAndRun(t *testing.T) {
	requireCC(t)
	checkRandomPrograms(t, randomProgramCount(t, 40), false)
}

func TestRandomProgramsUnderSanitizers(t *testing.T) {
	requireSanitizers(t)
	checkRandomPrograms(t, randomProgramCount(t, 25), true)
}
