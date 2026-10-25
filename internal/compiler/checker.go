package compiler

import (
	"fmt"
	"strings"
)

type scope struct {
	parent *scope
	vars   map[string]*Var
}

func (s *scope) lookup(name string) *Var {
	for ; s != nil; s = s.parent {
		if v, ok := s.vars[name]; ok {
			return v
		}
	}
	return nil
}

type checker struct {
	info    *Info
	errs    ErrorList
	mod     *Module
	fn      *Func
	scope   *scope
	loops   int
	nextVar int
	externs map[string]*Func
}

func CheckSource(file string, src []byte) (*Program, error) {
	f, err := Parse(file, src)
	if err != nil {
		return nil, err
	}
	if len(f.Imports) > 0 {
		return nil, errorf(f.Imports[0].Pos, "module imports require a file on disk; use Load")
	}
	m := &Module{Path: file, Display: file, File: f, Imports: map[string]*Module{}}
	for _, ci := range f.CImports {
		if err := validateHeader(ci.Header); err != nil {
			return nil, ErrorList{errorf(ci.HdrPos, "%v", err)}
		}
		m.Headers = append(m.Headers, ci.Header)
	}
	return Check([]*Module{m}, m)
}

func Check(modules []*Module, entry *Module) (*Program, error) {
	c := &checker{
		info: &Info{
			Types:   map[Expr]Type{},
			Uses:    map[*Ident]*Var{},
			Defs:    map[*LetStmt]*Var{},
			Calls:   map[*CallExpr]*Func{},
			Assigns: map[*AssignStmt]*Var{},
		},
		externs: map[string]*Func{},
	}
	for _, m := range modules {
		c.collect(m)
	}
	for _, m := range modules {
		c.mod = m
		for _, fn := range m.Order {
			if !fn.Extern {
				c.checkFunc(fn)
			}
		}
	}
	prog := &Program{Modules: modules, Entry: entry, Info: c.info}
	main := entry.Funcs["main"]
	switch {
	case main == nil:
		c.errs = append(c.errs, errorf(Pos{File: entry.Display, Line: 1, Col: 1}, "entry file has no function main"))
	case main.Extern:
		c.errs = append(c.errs, errorf(main.Decl.NamePos, "main cannot be a c function"))
	case len(main.Params) != 0:
		c.errs = append(c.errs, errorf(main.Decl.NamePos, "main must not take parameters"))
	case main.Result != Void && main.Result != IntType:
		c.errs = append(c.errs, errorf(main.Decl.Result.Pos, "main must return nothing or int, not %s", main.Result))
	}
	prog.Main = main
	if len(c.errs) > 0 {
		return nil, c.errs.sorted()
	}
	return prog, nil
}

func (c *checker) errorf(pos Pos, format string, args ...any) {
	c.errs = append(c.errs, errorf(pos, format, args...))
}

func (c *checker) resolveType(t *TypeExpr) Type {
	if t == nil {
		return Void
	}
	if ty, ok := namedTypes[t.Name]; ok {
		return ty
	}
	c.errorf(t.Pos, "unknown type %s (expected int, bool or string)", t.Name)
	return Invalid
}

func (c *checker) collect(m *Module) {
	m.Funcs = map[string]*Func{}
	declare := func(fn *Func) {
		d := fn.Decl
		if _, ok := builtins[d.Name]; ok {
			c.errorf(d.NamePos, "cannot redefine builtin function %s", d.Name)
			return
		}
		if prev, ok := m.Funcs[d.Name]; ok {
			c.errorf(d.NamePos, "function %s already declared at %d:%d", d.Name, prev.Decl.NamePos.Line, prev.Decl.NamePos.Col)
			return
		}
		m.Funcs[d.Name] = fn
		m.Order = append(m.Order, fn)
	}
	for _, ci := range m.File.CImports {
		for _, d := range ci.Externs {
			fn := c.signature(m, d)
			fn.Extern = true
			fn.CName = d.Name
			if strings.HasPrefix(d.Name, "ks_") || strings.HasPrefix(d.Name, "_") || d.Name == "main" {
				c.errorf(d.NamePos, "c function name %s is reserved", d.Name)
				continue
			}
			for _, p := range fn.Params {
				if p.Type == StringType {
					c.errorf(p.Pos, "c functions may only use int and bool parameters")
				}
			}
			if fn.Result == StringType {
				c.errorf(d.Result.Pos, "c functions may only return int, bool or nothing")
			}
			if prev, ok := c.externs[d.Name]; ok && !sameSignature(prev, fn) {
				c.errorf(d.NamePos, "c function %s declared with a different signature in %s", d.Name, prev.Module.Display)
				continue
			}
			c.externs[d.Name] = fn
			declare(fn)
		}
	}
	for _, d := range m.File.Funcs {
		fn := c.signature(m, d)
		fn.CName = fmt.Sprintf("ks_f%d_%s", m.ID, d.Name)
		fn.Pub = d.Pub
		declare(fn)
	}
}

func sameSignature(a, b *Func) bool {
	if a.Result != b.Result || len(a.Params) != len(b.Params) {
		return false
	}
	for i := range a.Params {
		if a.Params[i].Type != b.Params[i].Type {
			return false
		}
	}
	return true
}

func (c *checker) signature(m *Module, d *FuncDecl) *Func {
	fn := &Func{Name: d.Name, Decl: d, Module: m, Result: c.resolveType(d.Result)}
	seen := map[string]bool{}
	for _, p := range d.Params {
		if seen[p.Name] {
			c.errorf(p.Pos, "duplicate parameter %s", p.Name)
		}
		seen[p.Name] = true
		fn.Params = append(fn.Params, &Var{Name: p.Name, Type: c.resolveType(p.Type), Pos: p.Pos})
	}
	return fn
}

func (c *checker) newVar(name string, t Type, mutable bool, pos Pos) *Var {
	c.nextVar++
	return &Var{Name: name, CName: fmt.Sprintf("ks_v%d_%s", c.nextVar, name), Type: t, Mutable: mutable, Pos: pos}
}

func (c *checker) declare(v *Var) {
	if prev, ok := c.scope.vars[v.Name]; ok {
		c.errorf(v.Pos, "%s already declared in this scope at %d:%d", v.Name, prev.Pos.Line, prev.Pos.Col)
		return
	}
	c.scope.vars[v.Name] = v
}

func (c *checker) push() {
	c.scope = &scope{parent: c.scope, vars: map[string]*Var{}}
}

func (c *checker) pop() {
	c.scope = c.scope.parent
}

func (c *checker) checkFunc(fn *Func) {
	c.fn = fn
	c.scope = nil
	c.loops = 0
	c.push()
	for _, p := range fn.Params {
		c.nextVar++
		p.CName = fmt.Sprintf("ks_v%d_%s", c.nextVar, p.Name)
		c.declare(p)
	}
	c.checkStmts(fn.Decl.Body.Stmts)
	c.pop()
	if fn.Result != Void && fn.Result != Invalid && !terminates(fn.Decl.Body) {
		c.errorf(fn.Decl.Body.End, "missing return at end of function %s (returns %s)", fn.Name, fn.Result)
	}
}

func terminates(b *Block) bool {
	if len(b.Stmts) == 0 {
		return false
	}
	switch s := b.Stmts[len(b.Stmts)-1].(type) {
	case *ReturnStmt:
		return true
	case *BlockStmt:
		return terminates(s.Block)
	case *IfStmt:
		return ifTerminates(s)
	}
	return false
}

func ifTerminates(s *IfStmt) bool {
	if !terminates(s.Then) {
		return false
	}
	switch e := s.Else.(type) {
	case *BlockStmt:
		return terminates(e.Block)
	case *IfStmt:
		return ifTerminates(e)
	}
	return false
}

func (c *checker) checkBlock(b *Block) {
	c.push()
	c.checkStmts(b.Stmts)
	c.pop()
}

func (c *checker) checkStmts(stmts []Stmt) {
	for _, s := range stmts {
		c.checkStmt(s)
	}
}

func (c *checker) checkStmt(s Stmt) {
	switch s := s.(type) {
	case *LetStmt:
		t := c.valueExpr(s.Value)
		if s.Type != nil {
			want := c.resolveType(s.Type)
			if want != Invalid && t != Invalid && want != t {
				c.errorf(s.Value.P(), "cannot initialize %s of type %s with value of type %s", s.Name, want, t)
			}
			t = want
		}
		v := c.newVar(s.Name, t, s.Mutable, s.NamePos)
		c.info.Defs[s] = v
		c.declare(v)
	case *AssignStmt:
		t := c.valueExpr(s.Value)
		v := c.scope.lookup(s.Name)
		if v != nil {
			c.info.Assigns[s] = v
		}
		switch {
		case v == nil:
			c.errorf(s.Pos, "undefined: %s", s.Name)
		case !v.Mutable:
			c.errorf(s.Pos, "cannot assign to %s: it is immutable (declared with let at %d:%d; use var to allow assignment)", s.Name, v.Pos.Line, v.Pos.Col)
		case t != Invalid && v.Type != Invalid && t != v.Type:
			c.errorf(s.Value.P(), "cannot assign value of type %s to %s of type %s", t, s.Name, v.Type)
		}
	case *IfStmt:
		c.condition(s.Cond, "if")
		c.checkBlock(s.Then)
		if s.Else != nil {
			c.checkStmt(s.Else)
		}
	case *WhileStmt:
		c.condition(s.Cond, "while")
		c.loops++
		c.checkBlock(s.Body)
		c.loops--
	case *ReturnStmt:
		want := c.fn.Result
		if s.Value == nil {
			if want != Void && want != Invalid {
				c.errorf(s.Pos, "missing return value (function %s returns %s)", c.fn.Name, want)
			}
			return
		}
		if want == Void {
			c.expr(s.Value)
			c.errorf(s.Value.P(), "function %s does not return a value", c.fn.Name)
			return
		}
		t := c.valueExpr(s.Value)
		if t != Invalid && want != Invalid && t != want {
			c.errorf(s.Value.P(), "cannot return value of type %s from function %s (returns %s)", t, c.fn.Name, want)
		}
	case *PrintStmt:
		for _, a := range s.Args {
			c.valueExpr(a)
		}
	case *ExprStmt:
		c.expr(s.X)
	case *BreakStmt:
		if c.loops == 0 {
			c.errorf(s.Pos, "break outside of a while loop")
		}
	case *ContinueStmt:
		if c.loops == 0 {
			c.errorf(s.Pos, "continue outside of a while loop")
		}
	case *BlockStmt:
		c.checkBlock(s.Block)
	default:
		panic(fmt.Sprintf("unexpected statement %T", s))
	}
}

func (c *checker) condition(e Expr, what string) {
	t := c.valueExpr(e)
	if t != Invalid && t != BoolType {
		c.errorf(e.P(), "%s condition must be bool, not %s", what, t)
	}
}

func (c *checker) valueExpr(e Expr) Type {
	t := c.expr(e)
	if t == Void {
		c.errorf(e.P(), "function call does not produce a value")
		return Invalid
	}
	return t
}

func (c *checker) expr(e Expr) Type {
	t := c.exprType(e)
	c.info.Types[e] = t
	return t
}

func (c *checker) exprType(e Expr) Type {
	switch e := e.(type) {
	case *IntLit:
		return IntType
	case *StringLit:
		return StringType
	case *BoolLit:
		return BoolType
	case *Ident:
		if v := c.scope.lookup(e.Name); v != nil {
			c.info.Uses[e] = v
			return v.Type
		}
		switch {
		case c.mod.Funcs[e.Name] != nil || builtins[e.Name] != nil:
			c.errorf(e.Pos, "function %s used as a value (did you mean to call it?)", e.Name)
		case c.mod.Imports[e.Name] != nil:
			c.errorf(e.Pos, "module %s used as a value", e.Name)
		default:
			c.errorf(e.Pos, "undefined: %s", e.Name)
		}
		return Invalid
	case *SelectorExpr:
		c.errorf(e.Pos, "%s.%s used as a value (did you mean to call it?)", e.X.Name, e.Name)
		return Invalid
	case *UnaryExpr:
		t := c.valueExpr(e.X)
		if t == Invalid {
			return Invalid
		}
		switch e.Op {
		case Minus:
			if t == IntType {
				return IntType
			}
		case Bang:
			if t == BoolType {
				return BoolType
			}
		}
		c.errorf(e.Pos, "operator %s is not defined for %s", e.Op, t)
		return Invalid
	case *BinaryExpr:
		return c.binary(e)
	case *CallExpr:
		return c.call(e)
	}
	panic(fmt.Sprintf("unexpected expression %T", e))
}

func (c *checker) binary(e *BinaryExpr) Type {
	x := c.valueExpr(e.X)
	y := c.valueExpr(e.Y)
	if x == Invalid || y == Invalid {
		return Invalid
	}
	mismatch := func() Type {
		c.errorf(e.Pos, "operator %s is not defined for %s and %s", e.Op, x, y)
		return Invalid
	}
	switch e.Op {
	case Plus:
		if x == y && (x == IntType || x == StringType) {
			return x
		}
	case Minus, Star, Slash, Percent:
		if x == IntType && y == IntType {
			return IntType
		}
	case Less, LessEq, Greater, GreaterEq:
		if x == IntType && y == IntType {
			return BoolType
		}
	case Eq, NotEq:
		if x == y {
			return BoolType
		}
	case AndAnd, OrOr:
		if x == BoolType && y == BoolType {
			return BoolType
		}
	}
	return mismatch()
}

func (c *checker) call(e *CallExpr) Type {
	var fn *Func
	var name string
	switch callee := e.Fn.(type) {
	case *Ident:
		name = callee.Name
		if v := c.scope.lookup(name); v != nil {
			c.errorf(callee.Pos, "%s is a variable of type %s, not a function", name, v.Type)
		} else if f := c.mod.Funcs[name]; f != nil {
			fn = f
		} else if f := builtins[name]; f != nil {
			fn = f
		} else if c.mod.Imports[name] != nil {
			c.errorf(callee.Pos, "module %s cannot be called", name)
		} else {
			c.errorf(callee.Pos, "undefined function: %s", name)
		}
	case *SelectorExpr:
		name = callee.X.Name + "." + callee.Name
		m := c.mod.Imports[callee.X.Name]
		if m == nil {
			c.errorf(callee.Pos, "undefined module: %s", callee.X.Name)
			break
		}
		f := m.Funcs[callee.Name]
		switch {
		case f == nil:
			c.errorf(callee.Pos, "undefined: %s", name)
		case f.Extern:
			c.errorf(callee.Pos, "%s is a c function of module %s; import the c header directly", name, callee.X.Name)
		case !f.Pub:
			c.errorf(callee.Pos, "%s is not public (declare it with pub fn)", name)
		default:
			fn = f
		}
	}
	for _, a := range e.Args {
		c.valueExpr(a)
	}
	if fn == nil {
		return Invalid
	}
	c.info.Calls[e] = fn
	if len(e.Args) != len(fn.Params) {
		c.errorf(e.Pos, "%s expects %d argument%s, got %d", name, len(fn.Params), plural(len(fn.Params)), len(e.Args))
		return fn.Result
	}
	for i, a := range e.Args {
		got := c.info.Types[a]
		want := fn.Params[i].Type
		if got != Invalid && got != Void && want != Invalid && got != want {
			c.errorf(a.P(), "argument %d of %s must be %s, not %s", i+1, name, want, got)
		}
	}
	return fn.Result
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}
