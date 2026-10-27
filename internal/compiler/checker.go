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
	info     *Info
	errs     ErrorList
	mod      *Module
	fn       *Func
	scope    *scope
	loops    int
	nextVar  int
	externs  map[string]*Func
	structs  []*StructInfo
	allowMut *CallExpr
}

func CheckSource(file string, src []byte) (*Program, error) {
	f, err := Parse(file, src)
	if err != nil {
		return nil, err
	}
	if len(f.Imports) > 0 {
		return nil, ErrorList{errorf(f.Imports[0].Pos, "module imports require a file on disk; use Load")}
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
			Types:    map[Expr]Type{},
			Uses:     map[*Ident]*Var{},
			Defs:     map[*LetStmt]*Var{},
			Calls:    map[*CallExpr]*Func{},
			Builtins: map[*CallExpr]string{},
			Fields:   map[*SelectorExpr]*Field{},
			Literals: map[*StructLit]*StructInfo{},
		},
		externs: map[string]*Func{},
	}
	for _, m := range modules {
		c.collectStructs(m)
	}
	for _, m := range modules {
		c.resolveStructFields(m)
	}
	c.checkStructCycles()
	for _, m := range modules {
		c.collectFuncs(m)
	}
	for _, m := range modules {
		c.mod = m
		for _, fn := range m.Order {
			if !fn.Extern {
				c.checkFunc(fn)
			}
		}
	}
	prog := &Program{Modules: modules, Structs: c.structs, Entry: entry, Info: c.info}
	main := entry.Funcs["main"]
	switch {
	case main == nil:
		c.errorf(Pos{File: entry.Display, Line: 1, Col: 1}, "entry file has no function main")
	case main.Extern:
		c.errorf(main.Decl.NamePos, "main cannot be a c function")
	case len(main.Params) != 0:
		c.errorf(main.Decl.NamePos, "main must not take parameters (use args() to read command-line arguments)")
	case main.Result != Void && main.Result != IntType:
		c.errorf(main.Decl.Result.Pos, "main must return nothing or int, not %s", main.Result)
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

func (c *checker) resolveType(m *Module, t *TypeExpr) Type {
	if t == nil {
		return Void
	}
	if t.Key != nil {
		key := c.resolveType(m, t.Key)
		val := c.resolveType(m, t.Elem)
		if key != Invalid && key != StringType && key != IntType {
			c.errorf(t.Pos, "map keys must be string or int, not %s", key)
			return Invalid
		}
		return MapOf(key, val)
	}
	if t.Elem != nil {
		return ArrayOf(c.resolveType(m, t.Elem))
	}
	if t.Module != "" {
		dep := m.Imports[t.Module]
		if dep == nil {
			c.errorf(t.Pos, "undefined module: %s", t.Module)
			return Invalid
		}
		si := dep.Structs[t.Name]
		switch {
		case si == nil:
			c.errorf(t.Pos, "unknown type %s", t)
			return Invalid
		case !si.Pub:
			c.errorf(t.Pos, "struct %s is not public (declare it with pub struct)", t)
			return Invalid
		}
		return si.Type
	}
	if ty, ok := namedTypes[t.Name]; ok {
		return ty
	}
	if si := m.Structs[t.Name]; si != nil {
		return si.Type
	}
	c.errorf(t.Pos, "unknown type %s", t.Name)
	return Invalid
}

func (c *checker) collectStructs(m *Module) {
	m.Structs = map[string]*StructInfo{}
	for _, d := range m.File.Structs {
		if _, ok := namedTypes[d.Name]; ok {
			c.errorf(d.NamePos, "cannot use builtin type name %s as a struct name", d.Name)
			continue
		}
		if prev, ok := m.Structs[d.Name]; ok {
			c.errorf(d.NamePos, "struct %s already declared at %d:%d", d.Name, prev.Decl.NamePos.Line, prev.Decl.NamePos.Col)
			continue
		}
		si := &StructInfo{ID: len(c.structs), Name: d.Name, Pub: d.Pub, Module: m, Decl: d, FieldMap: map[string]*Field{}}
		si.CName = fmt.Sprintf("ks_s%d_%s", si.ID, d.Name)
		si.Type = &TypeDesc{Kind: KStruct, Struct: si}
		m.Structs[d.Name] = si
		m.StructOrder = append(m.StructOrder, si)
		c.structs = append(c.structs, si)
	}
}

func (c *checker) resolveStructFields(m *Module) {
	for _, si := range m.StructOrder {
		for _, fd := range si.Decl.Fields {
			if prev, ok := si.FieldMap[fd.Name]; ok {
				c.errorf(fd.Pos, "field %s already declared at %d:%d", fd.Name, prev.Pos.Line, prev.Pos.Col)
				continue
			}
			f := &Field{Name: fd.Name, Type: c.resolveType(m, fd.Type), Pos: fd.Pos}
			si.Fields = append(si.Fields, f)
			si.FieldMap[fd.Name] = f
		}
	}
}

func (c *checker) checkStructCycles() {
	for _, si := range c.structs {
		seen := map[*StructInfo]bool{}
		var reaches func(t *StructInfo) bool
		reaches = func(t *StructInfo) bool {
			for _, f := range t.Fields {
				if f.Type.Kind != KStruct {
					continue
				}
				if f.Type.Struct == si {
					return true
				}
				if !seen[f.Type.Struct] {
					seen[f.Type.Struct] = true
					if reaches(f.Type.Struct) {
						return true
					}
				}
			}
			return false
		}
		if reaches(si) {
			c.errorf(si.Decl.NamePos, "struct %s contains itself through its fields, so no value of it could be built (use an array field to break the cycle)", si.Name)
		}
	}
}

func (c *checker) collectFuncs(m *Module) {
	m.Funcs = map[string]*Func{}
	declare := func(fn *Func) {
		d := fn.Decl
		if prev, ok := m.Funcs[d.Name]; ok {
			c.errorf(d.NamePos, "function %s already declared at %d:%d", d.Name, prev.Decl.NamePos.Line, prev.Decl.NamePos.Col)
			return
		}
		if si, ok := m.Structs[d.Name]; ok {
			c.errorf(d.NamePos, "%s already declared as a struct at %d:%d", d.Name, si.Decl.NamePos.Line, si.Decl.NamePos.Col)
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
				if p.Type != IntType && p.Type != BoolType && p.Type != FloatType && p.Type != Invalid {
					c.errorf(p.Pos, "c functions may only use int, bool and float parameters, not %s", p.Type)
				}
			}
			if r := fn.Result; r != Void && r != IntType && r != BoolType && r != FloatType && r != Invalid {
				c.errorf(d.Result.Pos, "c functions may only return int, bool, float or nothing, not %s", r)
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
	fn := &Func{Name: d.Name, Decl: d, Module: m, Result: c.resolveType(m, d.Result)}
	seen := map[string]bool{}
	for _, p := range d.Params {
		if seen[p.Name] {
			c.errorf(p.Pos, "duplicate parameter %s", p.Name)
		}
		seen[p.Name] = true
		fn.Params = append(fn.Params, &Var{Name: p.Name, Type: c.resolveType(m, p.Type), Pos: p.Pos, Param: true})
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

func (c *checker) mutatingRHS(e Expr) {
	if call, ok := e.(*CallExpr); ok {
		c.allowMut = call
	}
}

func (c *checker) assignable(pos Pos, got, want Type, what string) {
	if got != Invalid && want != Invalid && got != want {
		c.errorf(pos, "%s: expected %s, found %s", what, want, got)
	}
}

func (c *checker) checkStmt(s Stmt) {
	switch s := s.(type) {
	case *LetStmt:
		c.mutatingRHS(s.Value)
		var t Type
		if s.Type != nil {
			want := c.resolveType(c.mod, s.Type)
			got := c.exprWant(s.Value, want)
			if want != Invalid && got != Invalid && want != got {
				c.errorf(s.Value.P(), "cannot initialize %s of type %s with value of type %s", s.Name, want, got)
			}
			t = want
		} else {
			t = c.exprWant(s.Value, nil)
		}
		c.allowMut = nil
		v := c.newVar(s.Name, t, s.Mutable, s.NamePos)
		c.info.Defs[s] = v
		c.declare(v)
	case *AssignStmt:
		want := c.place(s.Target, s.Target)
		c.mutatingRHS(s.Value)
		got := c.exprWant(s.Value, want)
		c.allowMut = nil
		if got != Invalid && want != Invalid && got != want {
			c.errorf(s.Value.P(), "cannot assign value of type %s to %s of type %s", got, describePlace(s.Target), want)
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
		got := c.exprWant(s.Value, want)
		if got != Invalid && want != Invalid && got != want {
			c.errorf(s.Value.P(), "cannot return value of type %s from function %s (returns %s)", got, c.fn.Name, want)
		}
	case *PrintStmt:
		for _, a := range s.Args {
			c.valueExpr(a)
		}
	case *ExprStmt:
		c.mutatingRHS(s.X)
		c.expr(s.X)
		c.allowMut = nil
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

func describePlace(e Expr) string {
	switch e := e.(type) {
	case *Ident:
		return e.Name
	case *IndexExpr:
		return describePlace(e.X) + "[...]"
	case *SelectorExpr:
		return describePlace(e.X) + "." + e.Name
	}
	return "expression"
}

func (c *checker) place(e Expr, whole Expr) Type {
	t := c.placeType(e, whole)
	c.info.Types[e] = t
	return t
}

func (c *checker) placeType(e Expr, whole Expr) Type {
	switch e := e.(type) {
	case *Ident:
		v := c.scope.lookup(e.Name)
		if v == nil {
			if c.mod.Imports[e.Name] != nil {
				c.errorf(e.Pos, "cannot assign to module %s", e.Name)
			} else {
				c.errorf(e.Pos, "undefined: %s", e.Name)
			}
			return Invalid
		}
		c.info.Uses[e] = v
		switch {
		case v.Param:
			c.errorf(e.Pos, "cannot assign to %s: %s is a parameter (copy it into a var first)", describePlace(whole), v.Name)
			return Invalid
		case !v.Mutable:
			c.errorf(e.Pos, "cannot assign to %s: %s is immutable (declared with let at %d:%d; use var to allow assignment)", describePlace(whole), v.Name, v.Pos.Line, v.Pos.Col)
			return Invalid
		}
		return v.Type
	case *IndexExpr:
		base := c.place(e.X, whole)
		idx := c.valueExpr(e.Index)
		switch base.Kind {
		case KInvalid:
			return Invalid
		case KMap:
			if idx != Invalid && base.Key != Invalid && idx != base.Key {
				c.errorf(e.Index.P(), "map key must be %s, not %s", base.Key, idx)
			}
			return base.Elem
		case KArray:
			if idx != Invalid && idx != IntType {
				c.errorf(e.Index.P(), "array index must be int, not %s", idx)
			}
			return base.Elem
		case KString:
			c.errorf(e.X.P(), "cannot assign into a string; strings are immutable")
			return Invalid
		}
		c.errorf(e.X.P(), "cannot index a value of type %s", base)
		return Invalid
	case *SelectorExpr:
		base := c.place(e.X, whole)
		if base == Invalid {
			return Invalid
		}
		return c.field(e, base)
	}
	c.errorf(e.P(), "cannot assign to this expression")
	return Invalid
}

func (c *checker) field(e *SelectorExpr, base Type) Type {
	if base.Kind != KStruct {
		hint := ""
		if base.Kind == KArray || base.Kind == KString {
			hint = fmt.Sprintf(" (use len(...) for the length of a %s)", base)
		}
		c.errorf(e.NamePos, "type %s has no field %s%s", base, e.Name, hint)
		return Invalid
	}
	f := base.Struct.FieldMap[e.Name]
	if f == nil {
		c.errorf(e.NamePos, "struct %s has no field %s", base.Struct.Name, e.Name)
		return Invalid
	}
	c.info.Fields[e] = f
	return f.Type
}

func (c *checker) condition(e Expr, what string) {
	t := c.valueExpr(e)
	if t != Invalid && t != BoolType {
		c.errorf(e.P(), "%s condition must be bool, not %s", what, t)
	}
}

func (c *checker) valueExpr(e Expr) Type {
	return c.exprWant(e, nil)
}

func (c *checker) exprWant(e Expr, want Type) Type {
	var t Type
	if lit, ok := e.(*ArrayLit); ok {
		t = c.arrayLit(lit, want)
		c.info.Types[e] = t
	} else if lit, ok := e.(*MapLit); ok {
		t = c.mapLit(lit, want)
		c.info.Types[e] = t
	} else {
		t = c.expr(e)
	}
	if t == Void {
		c.errorf(e.P(), "function call does not produce a value")
		return Invalid
	}
	return t
}

func (c *checker) arrayLit(e *ArrayLit, want Type) Type {
	var elemWant Type
	if want != nil && want.Kind == KArray {
		elemWant = want.Elem
	}
	if len(e.Elems) == 0 {
		if want != nil && want.Kind == KArray {
			return want
		}
		c.errorf(e.Pos, "cannot infer the element type of an empty array literal; add a type annotation such as let a: [int] = [];")
		return Invalid
	}
	elem := elemWant
	for i, el := range e.Elems {
		t := c.exprWant(el, elem)
		if elem == nil {
			elem = t
			continue
		}
		if t != Invalid && elem != Invalid && t != elem {
			c.errorf(el.P(), "array element %d has type %s, expected %s", i+1, t, elem)
		}
	}
	return ArrayOf(elem)
}

func (c *checker) mapLit(e *MapLit, want Type) Type {
	var keyWant, valWant Type
	if want != nil && want.Kind == KMap {
		keyWant, valWant = want.Key, want.Elem
	}
	if len(e.Entries) == 0 {
		if keyWant != nil {
			return want
		}
		c.errorf(e.Pos, "cannot infer the type of an empty map literal; add a type annotation such as let m: {string: int} = {};")
		return Invalid
	}
	var keyT, valT Type = keyWant, valWant
	for i, en := range e.Entries {
		kt := c.exprWant(en.Key, keyT)
		vt := c.exprWant(en.Value, valT)
		if keyT == nil {
			keyT = kt
		} else if kt != Invalid && keyT != Invalid && kt != keyT {
			c.errorf(en.Key.P(), "map key %d has type %s, expected %s", i+1, kt, keyT)
		}
		if valT == nil {
			valT = vt
		} else if vt != Invalid && valT != Invalid && vt != valT {
			c.errorf(en.Value.P(), "map value %d has type %s, expected %s", i+1, vt, valT)
		}
	}
	if keyT != Invalid && keyT != StringType && keyT != IntType {
		c.errorf(e.Pos, "map keys must be string or int, not %s", keyT)
		return Invalid
	}
	return MapOf(keyT, valT)
}

func (c *checker) expr(e Expr) Type {
	t := c.exprType(e)
	c.info.Types[e] = t
	return t
}

func (c *checker) moduleRef(e Expr) (*Module, string, bool) {
	id, ok := e.(*Ident)
	if !ok || c.scope.lookup(id.Name) != nil {
		return nil, "", false
	}
	m := c.mod.Imports[id.Name]
	return m, id.Name, m != nil
}

func (c *checker) exprType(e Expr) Type {
	switch e := e.(type) {
	case *IntLit:
		return IntType
	case *FloatLit:
		return FloatType
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
		case c.mod.Funcs[e.Name] != nil || builtinNames[e.Name]:
			c.errorf(e.Pos, "function %s used as a value (did you mean to call it?)", e.Name)
		case c.mod.Imports[e.Name] != nil:
			c.errorf(e.Pos, "module %s used as a value", e.Name)
		case c.mod.Structs[e.Name] != nil:
			c.errorf(e.Pos, "struct %s used as a value (write %s { ... } to create one)", e.Name, e.Name)
		default:
			c.errorf(e.Pos, "undefined: %s", e.Name)
		}
		return Invalid
	case *SelectorExpr:
		if _, alias, ok := c.moduleRef(e.X); ok {
			c.errorf(e.P(), "%s.%s used as a value (did you mean to call it?)", alias, e.Name)
			return Invalid
		}
		base := c.valueExpr(e.X)
		if base == Invalid {
			return Invalid
		}
		return c.field(e, base)
	case *IndexExpr:
		base := c.valueExpr(e.X)
		idx := c.valueExpr(e.Index)
		switch base.Kind {
		case KInvalid:
			return Invalid
		case KMap:
			if idx != Invalid && base.Key != Invalid && idx != base.Key {
				c.errorf(e.Index.P(), "map key must be %s, not %s", base.Key, idx)
			}
			return base.Elem
		case KArray:
			if idx != Invalid && idx != IntType {
				c.errorf(e.Index.P(), "array index must be int, not %s", idx)
			}
			return base.Elem
		case KString:
			c.errorf(e.X.P(), "cannot index a string; use byte_at(s, i) or substr(s, start, count)")
			return Invalid
		}
		c.errorf(e.X.P(), "cannot index a value of type %s", base)
		return Invalid
	case *ArrayLit:
		return c.arrayLit(e, nil)
	case *MapLit:
		return c.mapLit(e, nil)
	case *StructLit:
		return c.structLit(e)
	case *UnaryExpr:
		t := c.valueExpr(e.X)
		if t == Invalid {
			return Invalid
		}
		switch {
		case e.Op == Minus && t.Numeric():
			return t
		case e.Op == Bang && t == BoolType:
			return BoolType
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

func (c *checker) structLit(e *StructLit) Type {
	var si *StructInfo
	name := e.Name
	if e.Module != "" {
		name = e.Module + "." + e.Name
		dep := c.mod.Imports[e.Module]
		switch {
		case c.scope.lookup(e.Module) != nil || dep == nil:
			c.errorf(e.Pos, "undefined module: %s", e.Module)
		case dep.Structs[e.Name] == nil:
			c.errorf(e.Pos, "unknown struct %s", name)
		case !dep.Structs[e.Name].Pub:
			c.errorf(e.Pos, "struct %s is not public (declare it with pub struct)", name)
		default:
			si = dep.Structs[e.Name]
		}
	} else if si = c.mod.Structs[e.Name]; si == nil {
		c.errorf(e.Pos, "unknown struct %s", e.Name)
	}
	seen := map[string]bool{}
	for _, fi := range e.Fields {
		var want Type
		if si != nil {
			if f := si.FieldMap[fi.Name]; f != nil {
				want = f.Type
			} else {
				c.errorf(fi.Pos, "struct %s has no field %s", si.Name, fi.Name)
			}
		}
		if seen[fi.Name] {
			c.errorf(fi.Pos, "field %s given more than once", fi.Name)
		}
		seen[fi.Name] = true
		got := c.exprWant(fi.Value, want)
		if want != nil && got != Invalid && want != Invalid && got != want {
			c.errorf(fi.Value.P(), "field %s of %s must be %s, not %s", fi.Name, si.Name, want, got)
		}
	}
	if si == nil {
		return Invalid
	}
	var missing []string
	for _, f := range si.Fields {
		if !seen[f.Name] {
			missing = append(missing, f.Name)
		}
	}
	if len(missing) > 0 {
		c.errorf(e.Pos, "missing field%s in %s literal: %s", plural(len(missing)), name, strings.Join(missing, ", "))
	}
	c.info.Literals[e] = si
	return si.Type
}

func (c *checker) binary(e *BinaryExpr) Type {
	x := c.valueExpr(e.X)
	y := c.valueExpr(e.Y)
	if x == Invalid || y == Invalid {
		return Invalid
	}
	if x == y {
		switch e.Op {
		case Plus:
			if x.Numeric() || x == StringType {
				return x
			}
		case Minus, Star, Slash:
			if x.Numeric() {
				return x
			}
		case Percent:
			if x == IntType {
				return x
			}
		case Less, LessEq, Greater, GreaterEq:
			if x.Numeric() || x == StringType {
				return BoolType
			}
		case Eq, NotEq:
			if x.Comparable() {
				return BoolType
			}
			c.errorf(e.Pos, "operator %s is not defined for %s (only int, bool, float and string values can be compared)", e.Op, x)
			return Invalid
		case AndAnd, OrOr:
			if x == BoolType {
				return BoolType
			}
		}
	}
	hint := ""
	if x.Numeric() && y.Numeric() {
		hint = " (convert explicitly with float(...) or int(...))"
	}
	c.errorf(e.Pos, "operator %s is not defined for %s and %s%s", e.Op, x, y, hint)
	return Invalid
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
		} else if builtinNames[name] {
			return c.builtin(e, name)
		} else if c.mod.Imports[name] != nil {
			c.errorf(callee.Pos, "module %s cannot be called", name)
		} else if c.mod.Structs[name] != nil {
			c.errorf(callee.Pos, "struct %s cannot be called; write %s { field: value, ... }", name, name)
		} else {
			c.errorf(callee.Pos, "undefined function: %s", name)
		}
	case *SelectorExpr:
		m, alias, ok := c.moduleRef(callee.X)
		if !ok {
			if id, isIdent := callee.X.(*Ident); isIdent && c.scope.lookup(id.Name) == nil {
				c.errorf(id.Pos, "undefined: %s", id.Name)
				break
			}
			c.errorf(callee.NamePos, "%s is not a function; only functions can be called", describePlace(callee))
			break
		}
		name = alias + "." + callee.Name
		f := m.Funcs[callee.Name]
		switch {
		case f == nil:
			c.errorf(callee.P(), "undefined: %s", name)
		case f.Extern:
			c.errorf(callee.P(), "%s is a c function of module %s; import the c header directly", name, alias)
		case !f.Pub:
			c.errorf(callee.P(), "%s is not public (declare it with pub fn)", name)
		default:
			fn = f
		}
	}
	if fn == nil {
		for _, a := range e.Args {
			c.valueExpr(a)
		}
		return Invalid
	}
	c.info.Calls[e] = fn
	if len(e.Args) != len(fn.Params) {
		for _, a := range e.Args {
			c.valueExpr(a)
		}
		c.errorf(e.Pos, "%s expects %d argument%s, got %d", name, len(fn.Params), plural(len(fn.Params)), len(e.Args))
		return fn.Result
	}
	for i, a := range e.Args {
		want := fn.Params[i].Type
		got := c.exprWant(a, want)
		if got != Invalid && want != Invalid && got != want {
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
