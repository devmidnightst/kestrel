package compiler

import (
	"fmt"
	"sort"
	"strings"
)

type gen struct {
	prog *Program
	sb   strings.Builder
	ind  int
}

func GenerateC(prog *Program) string {
	g := &gen{prog: prog}
	g.sb.WriteString(runtimeC)
	headers := map[string]bool{}
	for _, m := range prog.Modules {
		for _, h := range m.Headers {
			headers[h] = true
		}
	}
	if len(headers) > 0 {
		names := make([]string, 0, len(headers))
		for h := range headers {
			names = append(names, h)
		}
		sort.Strings(names)
		g.sb.WriteString("\n")
		for _, h := range names {
			fmt.Fprintf(&g.sb, "#include <%s>\n", h)
		}
	}
	g.sb.WriteString("\n")
	externs := map[string]bool{}
	for _, m := range prog.Modules {
		for _, fn := range m.Order {
			if fn.Extern && !externs[fn.CName] {
				externs[fn.CName] = true
				g.externWrapper(fn)
			}
		}
	}
	for _, m := range prog.Modules {
		for _, fn := range m.Order {
			if !fn.Extern {
				fmt.Fprintf(&g.sb, "static %s;\n", g.prototype(fn))
			}
		}
	}
	for _, m := range prog.Modules {
		for _, fn := range m.Order {
			if !fn.Extern {
				g.function(fn)
			}
		}
	}
	g.sb.WriteString("\nint main(void) {\n")
	if prog.Main.Result == IntType {
		fmt.Fprintf(&g.sb, "\tint64_t ks_rc = %s();\n\tfflush(stdout);\n\treturn (int)ks_rc;\n", prog.Main.CName)
	} else {
		fmt.Fprintf(&g.sb, "\t%s();\n\tfflush(stdout);\n\treturn 0;\n", prog.Main.CName)
	}
	g.sb.WriteString("}\n")
	return g.sb.String()
}

func cType(t Type) string {
	switch t {
	case IntType:
		return "int64_t"
	case BoolType:
		return "bool"
	case StringType:
		return "const char *"
	}
	return "void"
}

func externCName(fn *Func) string {
	return "ks_c_" + fn.CName
}

func (g *gen) prototype(fn *Func) string {
	params := make([]string, len(fn.Params))
	for i, p := range fn.Params {
		params[i] = fmt.Sprintf("%s %s", cType(p.Type), p.CName)
	}
	if len(params) == 0 {
		params = []string{"void"}
	}
	return fmt.Sprintf("%s %s(%s)", cType(fn.Result), fn.CName, strings.Join(params, ", "))
}

func (g *gen) externWrapper(fn *Func) {
	params := make([]string, len(fn.Params))
	args := make([]string, len(fn.Params))
	for i, p := range fn.Params {
		params[i] = fmt.Sprintf("%s a%d", cType(p.Type), i)
		args[i] = fmt.Sprintf("a%d", i)
	}
	sig := "void"
	if len(params) > 0 {
		sig = strings.Join(params, ", ")
	}
	call := fmt.Sprintf("%s(%s)", fn.CName, strings.Join(args, ", "))
	if fn.Result == Void {
		fmt.Fprintf(&g.sb, "static void %s(%s) {\n\t%s;\n}\n\n", externCName(fn), sig, call)
	} else {
		fmt.Fprintf(&g.sb, "static %s %s(%s) {\n\treturn %s;\n}\n\n", cType(fn.Result), externCName(fn), sig, call)
	}
}

func (g *gen) line(format string, args ...any) {
	g.sb.WriteString(strings.Repeat("\t", g.ind))
	fmt.Fprintf(&g.sb, format, args...)
	g.sb.WriteString("\n")
}

func (g *gen) function(fn *Func) {
	g.sb.WriteString("\n")
	fmt.Fprintf(&g.sb, "static %s {\n", g.prototype(fn))
	g.ind = 1
	for _, p := range fn.Params {
		g.line("(void)%s;", p.CName)
	}
	g.stmts(fn.Decl.Body.Stmts)
	g.ind = 0
	g.sb.WriteString("}\n")
}

func (g *gen) stmts(list []Stmt) {
	for _, s := range list {
		g.stmt(s)
	}
}

func (g *gen) block(b *Block) {
	g.ind++
	g.stmts(b.Stmts)
	g.ind--
}

func (g *gen) stmt(s Stmt) {
	info := g.prog.Info
	switch s := s.(type) {
	case *LetStmt:
		v := info.Defs[s]
		g.line("%s %s = %s;", cType(v.Type), v.CName, g.expr(s.Value))
		g.line("(void)%s;", v.CName)
	case *AssignStmt:
		g.line("%s = %s;", g.lookupAssign(s), g.expr(s.Value))
	case *IfStmt:
		g.ifChain(s, false)
	case *WhileStmt:
		g.line("while (%s) {", g.cond(s.Cond))
		g.block(s.Body)
		g.line("}")
	case *ReturnStmt:
		if s.Value == nil {
			g.line("return;")
		} else {
			g.line("return %s;", g.expr(s.Value))
		}
	case *PrintStmt:
		for i, a := range s.Args {
			if i > 0 {
				g.line("putchar(' ');")
			}
			switch info.Types[a] {
			case IntType:
				g.line("ks_print_int(%s);", g.expr(a))
			case BoolType:
				g.line("ks_print_bool(%s);", g.expr(a))
			case StringType:
				g.line("ks_print_string(%s);", g.expr(a))
			}
		}
		g.line("putchar('\\n');")
	case *ExprStmt:
		g.line("%s;", g.expr(s.X))
	case *BreakStmt:
		g.line("break;")
	case *ContinueStmt:
		g.line("continue;")
	case *BlockStmt:
		g.line("{")
		g.block(s.Block)
		g.line("}")
	default:
		panic(fmt.Sprintf("unexpected statement %T", s))
	}
}

func (g *gen) lookupAssign(s *AssignStmt) string {
	return g.prog.Info.Assigns[s].CName
}

func (g *gen) ifChain(s *IfStmt, chained bool) {
	if chained {
		g.sb.WriteString(fmt.Sprintf("if (%s) {\n", g.cond(s.Cond)))
	} else {
		g.line("if (%s) {", g.cond(s.Cond))
	}
	g.block(s.Then)
	switch e := s.Else.(type) {
	case nil:
		g.line("}")
	case *IfStmt:
		g.sb.WriteString(strings.Repeat("\t", g.ind) + "} else ")
		g.ifChain(e, true)
	case *BlockStmt:
		g.line("} else {")
		g.block(e.Block)
		g.line("}")
	}
}

var cBinaryOps = map[Kind]string{
	Less:      "<",
	LessEq:    "<=",
	Greater:   ">",
	GreaterEq: ">=",
	AndAnd:    "&&",
	OrOr:      "||",
}

var checkedOps = map[Kind]string{
	Plus:    "ks_add",
	Minus:   "ks_sub",
	Star:    "ks_mul",
	Slash:   "ks_div",
	Percent: "ks_mod",
}

func (g *gen) expr(e Expr) string {
	info := g.prog.Info
	switch e := e.(type) {
	case *IntLit:
		return fmt.Sprintf("INT64_C(%d)", e.Value)
	case *StringLit:
		return cString(e.Value)
	case *BoolLit:
		if e.Value {
			return "true"
		}
		return "false"
	case *Ident:
		return info.Uses[e].CName
	case *UnaryExpr:
		if e.Op == Minus {
			return fmt.Sprintf("ks_neg(%s)", g.expr(e.X))
		}
		return fmt.Sprintf("(!%s)", g.expr(e.X))
	case *BinaryExpr:
		x, y := g.expr(e.X), g.expr(e.Y)
		operand := info.Types[e.X]
		switch e.Op {
		case Plus:
			if operand == StringType {
				return fmt.Sprintf("ks_concat(%s, %s)", x, y)
			}
		case Eq, NotEq:
			neg := ""
			if e.Op == NotEq {
				neg = "!"
			}
			if operand == StringType {
				return fmt.Sprintf("(%sks_streq(%s, %s))", neg, x, y)
			}
			if neg == "" {
				return fmt.Sprintf("(%s == %s)", x, y)
			}
			return fmt.Sprintf("(%s != %s)", x, y)
		}
		if fn, ok := checkedOps[e.Op]; ok {
			return fmt.Sprintf("%s(%s, %s)", fn, x, y)
		}
		return fmt.Sprintf("(%s %s %s)", x, cBinaryOps[e.Op], y)
	case *CallExpr:
		fn := info.Calls[e]
		args := make([]string, len(e.Args))
		for i, a := range e.Args {
			args[i] = g.expr(a)
		}
		name := fn.CName
		if fn.Extern {
			name = externCName(fn)
		}
		return fmt.Sprintf("%s(%s)", name, strings.Join(args, ", "))
	}
	panic(fmt.Sprintf("unexpected expression %T", e))
}

func cString(s string) string {
	var sb strings.Builder
	sb.WriteByte('"')
	for i := 0; i < len(s); i++ {
		b := s[i]
		switch {
		case b == '"' || b == '\\':
			sb.WriteByte('\\')
			sb.WriteByte(b)
		case b == '\n':
			sb.WriteString("\\n")
		case b == '\t':
			sb.WriteString("\\t")
		case b == '\r':
			sb.WriteString("\\r")
		case b == '?':
			sb.WriteString("\\?")
		case b < 0x20 || b >= 0x7f:
			fmt.Fprintf(&sb, "\\%03o", b)
		default:
			sb.WriteByte(b)
		}
	}
	sb.WriteByte('"')
	return sb.String()
}

func (g *gen) cond(e Expr) string {
	s := g.expr(e)
	if len(s) < 2 || s[0] != '(' || s[len(s)-1] != ')' {
		return s
	}
	depth := 0
	inString := false
	for i := 0; i < len(s); i++ {
		switch {
		case inString && s[i] == '\\':
			i++
		case s[i] == '"':
			inString = !inString
		case inString:
		case s[i] == '(':
			depth++
		case s[i] == ')':
			depth--
			if depth == 0 && i != len(s)-1 {
				return s
			}
		}
	}
	return s[1 : len(s)-1]
}
