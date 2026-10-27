package compiler

import (
	"fmt"
	"strings"
)

type genScope struct {
	vars []string
	loop bool
	cont string
}

type fnGen struct {
	g      *gen
	out    *strings.Builder
	ind    int
	ntmp   int
	scopes []*genScope
	frames [][]string
}

func (f *fnGen) line(format string, args ...any) {
	f.out.WriteString(strings.Repeat("\t", f.ind))
	fmt.Fprintf(f.out, format, args...)
	f.out.WriteString("\n")
}

func (f *fnGen) temp(ctype, init string) string {
	f.ntmp++
	name := fmt.Sprintf("ks_t%d", f.ntmp)
	f.line("%s %s = %s;", ctype, name, init)
	return name
}

func (f *fnGen) pushFrame() {
	f.frames = append(f.frames, nil)
}

func (f *fnGen) popFrame() {
	top := f.frames[len(f.frames)-1]
	for i := len(top) - 1; i >= 0; i-- {
		f.line("ks_release(%s);", top[i])
	}
	f.frames = f.frames[:len(f.frames)-1]
}

func (f *fnGen) releaseLater(name string) {
	f.frames[len(f.frames)-1] = append(f.frames[len(f.frames)-1], name)
}

func (f *fnGen) releaseScope(s *genScope) {
	for i := len(s.vars) - 1; i >= 0; i-- {
		f.line("ks_release(%s);", s.vars[i])
	}
}

func (f *fnGen) function(fn *Func) {
	fmt.Fprintf(f.out, "\nstatic %s {\n", prototype(fn))
	f.ind = 1
	f.line("ks_stack_check();")
	if fn.Recv != nil {
		f.line("(void)%s;", fn.Recv.CName)
	}
	for _, p := range fn.Params {
		f.line("(void)%s;", p.CName)
	}
	f.block(fn.Decl.Body, false)
	if fn.Result != Void {
		f.line("ks_panic(\"missing return\");")
	}
	f.ind = 0
	f.out.WriteString("}\n")
}

func (f *fnGen) block(b *Block, loop bool) {
	f.blockGo(b, loop, "", nil)
}

func (f *fnGen) blockGo(b *Block, loop bool, cont string, init func(*genScope)) {
	s := &genScope{loop: loop, cont: cont}
	f.scopes = append(f.scopes, s)
	if init != nil {
		init(s)
	}
	for _, st := range b.Stmts {
		f.stmt(st)
	}
	f.releaseScope(s)
	f.scopes = f.scopes[:len(f.scopes)-1]
}

func (f *fnGen) open() {
	f.line("{")
	f.ind++
	f.pushFrame()
}

func (f *fnGen) close() {
	f.popFrame()
	f.ind--
	f.line("}")
}

func (f *fnGen) stmt(s Stmt) {
	info := f.g.info
	switch s := s.(type) {
	case *LetStmt:
		v := info.Defs[s]
		f.line("%s %s = %s;", cType(v.Type), v.CName, zeroValue(v.Type))
		f.open()
		f.line("%s = %s;", v.CName, f.evalOwned(s.Value))
		f.close()
		if v.Type.Managed() {
			sc := f.scopes[len(f.scopes)-1]
			sc.vars = append(sc.vars, v.CName)
		}
	case *AssignStmt:
		f.open()
		f.assign(s)
		f.close()
	case *ExprStmt:
		f.open()
		x, owned := f.evalRaw(s.X)
		if t := info.Types[s.X]; owned && t.Managed() {
			f.releaseLater(x)
		} else if x != "" && t != Void {
			f.line("(void)%s;", x)
		}
		f.close()
	case *PrintStmt:
		f.open()
		for i, a := range s.Args {
			x := f.eval(a)
			if i > 0 {
				f.line("putchar(' ');")
			}
			f.line("%s", f.g.printValue(info.Types[a], x, false))
		}
		f.line("putchar('\\n');")
		f.close()
	case *IfStmt:
		f.line("{")
		f.ind++
		cond := f.condition(s.Cond)
		f.line("if (%s) {", cond)
		f.ind++
		f.block(s.Then, false)
		f.ind--
		if s.Else != nil {
			f.line("} else {")
			f.ind++
			if b, ok := s.Else.(*BlockStmt); ok {
				f.block(b.Block, false)
			} else {
				f.stmt(s.Else)
			}
			f.ind--
		}
		f.line("}")
		f.ind--
		f.line("}")
	case *WhileStmt:
		f.line("while (1) {")
		f.ind++
		cond := f.condition(s.Cond)
		f.line("if (!%s) break;", cond)
		f.block(s.Body, true)
		f.ind--
		f.line("}")
	case *ForStmt:
		f.forStmt(s)
	case *ReturnStmt:
		f.open()
		if s.Value == nil {
			f.popFrame()
			f.releaseAll(0)
			f.line("return;")
			f.pushFrame()
		} else {
			t := info.Types[s.Value]
			r := f.temp(cType(t), f.evalOwned(s.Value))
			f.popFrame()
			f.releaseAll(0)
			f.line("return %s;", r)
			f.pushFrame()
		}
		f.close()
	case *BreakStmt:
		f.releaseToLoop()
		f.line("break;")
	case *ContinueStmt:
		f.releaseToLoop()
		if lab := f.continueLabel(); lab != "" {
			f.line("goto %s;", lab)
		} else {
			f.line("continue;")
		}
	case *BlockStmt:
		f.line("{")
		f.ind++
		f.block(s.Block, false)
		f.ind--
		f.line("}")
	default:
		panic(fmt.Sprintf("unexpected statement %T", s))
	}
}

func (f *fnGen) continueLabel() string {
	for i := len(f.scopes) - 1; i >= 0; i-- {
		if f.scopes[i].loop {
			return f.scopes[i].cont
		}
	}
	return ""
}

func (f *fnGen) label(prefix string) string {
	f.ntmp++
	return fmt.Sprintf("%s%d", prefix, f.ntmp)
}

// evalKept evaluates e and keeps a scalar copy after temporary values are released.
func (f *fnGen) evalKept(e Expr) string {
	f.pushFrame()
	v := f.temp(cType(f.g.info.Types[e]), f.eval(e))
	f.popFrame()
	return v
}

// evalOwnedKept evaluates e as an owned object that the caller releases.
func (f *fnGen) evalOwnedKept(e Expr) string {
	f.pushFrame()
	v := f.temp("ks_obj *", f.evalOwned(e))
	f.popFrame()
	return v
}

func (f *fnGen) forMap(s *ForStmt, outer *genScope, lab string) {
	info := f.g.info
	mp := f.evalOwnedKept(s.Seq)
	outer.vars = append(outer.vars, mp)
	mt := info.Types[s.Seq]
	kind := mapKind(mt)
	m := f.temp("ks_map_"+kind+" *", fmt.Sprintf("(ks_map_%s *)%s", kind, mp))
	idx := f.temp("int64_t", "0")
	f.line("while (1) {")
	f.ind++
	f.line("while (%s < %s->cap && %s->used[%s] != 1) %s++;", idx, m, m, idx, idx)
	f.line("if (!(%s < %s->cap)) break;", idx, m)
	f.blockGo(s.Body, true, lab, func(sc *genScope) {
		v := info.Loops[s]
		get := fmt.Sprintf("%s->keys[%s]", m, idx)
		if v.Type.Managed() {
			f.line("%s %s = ks_retain(%s);", cType(v.Type), v.CName, get)
			sc.vars = append(sc.vars, v.CName)
		} else {
			f.line("%s %s = %s;", cType(v.Type), v.CName, get)
		}
	})
	f.line("%s:", lab)
	f.line("%s = ks_add(%s, 1);", idx, idx)
	f.ind--
	f.line("}")
}

func (f *fnGen) forStmt(s *ForStmt) {
	info := f.g.info
	f.line("{")
	f.ind++
	outer := &genScope{}
	f.scopes = append(f.scopes, outer)
	lab := f.label("ks_fc")
	if s.Kind == ForArray && info.Types[s.Seq].Kind == KMap {
		f.forMap(s, outer, lab)
	} else if s.Kind == ForArray {
		arr := f.evalOwnedKept(s.Seq)
		outer.vars = append(outer.vars, arr)
		elem := info.Types[s.Seq].Elem
		k := arrKind(elem)
		n := f.temp("int64_t", fmt.Sprintf("ks_arr_%s_len(%s)", k, arr))
		idx := f.temp("int64_t", "0")
		f.line("while (1) {")
		f.ind++
		f.line("if (!(%s < %s)) break;", idx, n)
		f.blockGo(s.Body, true, lab, func(sc *genScope) {
			v := info.Loops[s]
			get := fmt.Sprintf("ks_arr_%s_get(%s, %s)", k, arr, idx)
			if v.Type.Managed() {
				f.line("%s %s = ks_retain(%s);", cType(v.Type), v.CName, get)
				sc.vars = append(sc.vars, v.CName)
			} else {
				f.line("%s %s = %s;", cType(v.Type), v.CName, get)
			}
		})
		f.line("%s:", lab)
		f.line("%s = ks_add(%s, 1);", idx, idx)
		f.ind--
		f.line("}")
	} else {
		start := f.evalKept(s.Start)
		end := f.evalKept(s.End)
		v := info.Loops[s]
		f.line("int64_t %s = %s;", v.CName, start)
		op := "<"
		if s.Kind == ForInclusive {
			op = "<="
		}
		f.line("while (1) {")
		f.ind++
		f.line("if (!(%s %s %s)) break;", v.CName, op, end)
		f.blockGo(s.Body, true, lab, nil)
		f.line("%s:", lab)
		if s.Kind == ForInclusive {
			f.line("if (%s == %s) break;", v.CName, end)
		}
		f.line("%s = ks_add(%s, 1);", v.CName, v.CName)
		f.ind--
		f.line("}")
	}
	f.releaseScope(outer)
	f.scopes = f.scopes[:len(f.scopes)-1]
	f.ind--
	f.line("}")
}

func (f *fnGen) condition(e Expr) string {
	f.pushFrame()
	c := f.eval(e)
	cv := f.temp("bool", c)
	f.popFrame()
	return cv
}

func (f *fnGen) releaseAll(from int) {
	for i := len(f.scopes) - 1; i >= from; i-- {
		f.releaseScope(f.scopes[i])
	}
}

func (f *fnGen) releaseToLoop() {
	for i := len(f.scopes) - 1; i >= 0; i-- {
		f.releaseScope(f.scopes[i])
		if f.scopes[i].loop {
			return
		}
	}
}

func (f *fnGen) eval(e Expr) string {
	x, owned := f.evalRaw(e)
	if owned && f.g.info.Types[e].Managed() {
		f.releaseLater(x)
	}
	return x
}

func (f *fnGen) evalOwned(e Expr) string {
	x, owned := f.evalRaw(e)
	if !owned && f.g.info.Types[e].Managed() {
		return fmt.Sprintf("ks_retain(%s)", x)
	}
	return x
}

func (f *fnGen) evalOwnedTemp(e Expr) string {
	t := f.g.info.Types[e]
	return f.temp(cType(t), f.evalOwned(e))
}

var checkedOps = map[Kind]string{
	Plus:    "ks_add",
	Minus:   "ks_sub",
	Star:    "ks_mul",
	Slash:   "ks_div",
	Percent: "ks_mod",
}

var cOps = map[Kind]string{
	Plus:      "+",
	Minus:     "-",
	Star:      "*",
	Slash:     "/",
	Less:      "<",
	LessEq:    "<=",
	Greater:   ">",
	GreaterEq: ">=",
	Eq:        "==",
	NotEq:     "!=",
}

func (f *fnGen) evalRaw(e Expr) (string, bool) {
	info := f.g.info
	t := info.Types[e]
	switch e := e.(type) {
	case *IntLit:
		return intLiteral(e.Value), false
	case *FloatLit:
		return floatLiteral(e.Value), false
	case *BoolLit:
		if e.Value {
			return "true", false
		}
		return "false", false
	case *StringLit:
		return "(&" + f.g.literal(e.Value) + ".h)", false
	case *Ident:
		v := info.Uses[e]
		if v.Slot {
			return "(*" + v.CName + ")", false
		}
		return v.CName, false
	case *UnaryExpr:
		x := f.eval(e.X)
		switch {
		case e.Op == Bang:
			return f.temp("bool", "!"+x), false
		case t == IntType:
			return f.temp("int64_t", fmt.Sprintf("ks_neg(%s)", x)), false
		}
		return f.temp("double", "-"+x), false
	case *BinaryExpr:
		return f.binary(e, t)
	case *IndexExpr:
		baseT := info.Types[e.X]
		a := f.eval(e.X)
		i := f.eval(e.Index)
		if baseT.Kind == KMap {
			return f.temp(cType(t), fmt.Sprintf("ks_map_%s_get(%s, %s)", mapKind(baseT), a, i)), false
		}
		return f.temp(cType(t), fmt.Sprintf("ks_arr_%s_get(%s, %s)", arrKind(t), a, i)), false
	case *SelectorExpr:
		s := f.eval(e.X)
		si := info.Types[e.X].Struct
		return f.temp(cType(t), fmt.Sprintf("((%s *)%s)->f_%s", si.CName, s, e.Name)), false
	case *ArrayLit:
		k := arrKind(t.Elem)
		arr := f.temp("ks_obj *", fmt.Sprintf("ks_arr_%s_new(%d)", k, len(e.Elems)))
		for _, el := range e.Elems {
			v := f.evalOwnedTemp(el)
			f.line("ks_arr_%s_push(%s, %s);", k, arr, v)
		}
		return arr, true
	case *MapLit:
		k := mapKind(t)
		m := f.temp("ks_obj *", fmt.Sprintf("ks_map_%s_new(%d)", k, len(e.Entries)))
		for _, en := range e.Entries {
			kk := f.eval(en.Key)
			vv := f.evalOwnedTemp(en.Value)
			f.line("ks_map_%s_set(%s, %s, %s);", k, m, kk, vv)
		}
		return m, true
	case *StructLit:
		si := info.Literals[e]
		vals := map[string]string{}
		for _, fi := range e.Fields {
			vals[fi.Name] = f.evalOwnedTemp(fi.Value)
		}
		args := make([]string, len(si.Fields))
		for i, fd := range si.Fields {
			args[i] = vals[fd.Name]
		}
		return f.temp("ks_obj *", fmt.Sprintf("%s_new(%s)", si.CName, strings.Join(args, ", "))), true
	case *CallExpr:
		if name, ok := info.Builtins[e]; ok {
			return f.builtin(e, name, t)
		}
		fn := info.Calls[e]
		if fn.Recv != nil {
			return f.methodCall(e, fn)
		}
		args := make([]string, len(e.Args))
		for i, a := range e.Args {
			args[i] = f.eval(a)
		}
		name := fn.CName
		if fn.Extern {
			name = externCName(fn)
		}
		call := fmt.Sprintf("%s(%s)", name, strings.Join(args, ", "))
		if fn.Result == Void {
			f.line("%s;", call)
			return "", false
		}
		return f.temp(cType(fn.Result), call), fn.Result.Managed()
	}
	panic(fmt.Sprintf("unexpected expression %T", e))
}

func (f *fnGen) binary(e *BinaryExpr, t Type) (string, bool) {
	if e.Op == AndAnd || e.Op == OrOr {
		l := f.eval(e.X)
		r := f.temp("bool", l)
		if e.Op == AndAnd {
			f.line("if (%s) {", r)
		} else {
			f.line("if (!%s) {", r)
		}
		f.ind++
		f.pushFrame()
		y := f.eval(e.Y)
		f.line("%s = %s;", r, y)
		f.popFrame()
		f.ind--
		f.line("}")
		return r, false
	}
	operand := f.g.info.Types[e.X]
	x := f.eval(e.X)
	y := f.eval(e.Y)
	switch operand.Kind {
	case KString:
		switch e.Op {
		case Plus:
			return f.temp("ks_obj *", fmt.Sprintf("ks_str_concat(%s, %s)", x, y)), true
		case Eq:
			return f.temp("bool", fmt.Sprintf("ks_str_eq(%s, %s)", x, y)), false
		case NotEq:
			return f.temp("bool", fmt.Sprintf("!ks_str_eq(%s, %s)", x, y)), false
		}
		return f.temp("bool", fmt.Sprintf("ks_str_cmp(%s, %s) %s 0", x, y, cOps[e.Op])), false
	case KInt:
		if fn, ok := checkedOps[e.Op]; ok {
			return f.temp("int64_t", fmt.Sprintf("%s(%s, %s)", fn, x, y)), false
		}
	}
	return f.temp(cType(t), fmt.Sprintf("%s %s %s", x, cOps[e.Op], y)), false
}

type placeStep struct {
	expr Expr
	idx  string
}

func (f *fnGen) prepPlace(target Expr) (*Var, []placeStep) {
	var steps []placeStep
	cur := target
	for {
		switch e := cur.(type) {
		case *Ident:
			for i, j := 0, len(steps)-1; i < j; i, j = i+1, j-1 {
				steps[i], steps[j] = steps[j], steps[i]
			}
			for i := range steps {
				if ix, ok := steps[i].expr.(*IndexExpr); ok {
					it := f.g.info.Types[ix.Index]
					steps[i].idx = f.temp(cType(it), f.eval(ix.Index))
				}
			}
			return f.g.info.Uses[e], steps
		case *IndexExpr:
			steps = append(steps, placeStep{expr: e})
			cur = e.X
		case *SelectorExpr:
			steps = append(steps, placeStep{expr: e})
			cur = e.X
		default:
			panic(fmt.Sprintf("unexpected place %T", cur))
		}
	}
}

func (f *fnGen) walkPlace(root *Var, steps []placeStep) string {
	info := f.g.info
	ptr := "&" + root.CName
	if root.Slot {
		ptr = root.CName
	}
	for _, st := range steps {
		switch e := st.expr.(type) {
		case *IndexExpr:
			u := f.temp("ks_obj *", fmt.Sprintf("ks_unique(%s)", ptr))
			elem := info.Types[e]
			baseT := info.Types[e.X]
			if baseT.Kind == KMap {
				ptr = f.temp(cType(elem)+"*", fmt.Sprintf("ks_map_%s_slot(%s, %s)", mapKind(baseT), u, st.idx))
			} else {
				ptr = f.temp(cType(elem)+"*", fmt.Sprintf("ks_arr_%s_slot(%s, %s)", arrKind(elem), u, st.idx))
			}
		case *SelectorExpr:
			u := f.temp("ks_obj *", fmt.Sprintf("ks_unique(%s)", ptr))
			si := info.Types[e.X].Struct
			ptr = f.temp(cType(info.Types[e])+"*", fmt.Sprintf("&((%s *)%s)->f_%s", si.CName, u, e.Name))
		}
	}
	return ptr
}

func (f *fnGen) assign(s *AssignStmt) {
	if ix, ok := s.Target.(*IndexExpr); ok && f.g.info.Types[ix.X] != nil && f.g.info.Types[ix.X].Kind == KMap {
		key := f.eval(ix.Index)
		val := f.evalOwnedTemp(s.Value)
		root, steps := f.prepPlace(ix.X)
		ptr := f.walkPlace(root, steps)
		mt := f.g.info.Types[ix.X]
		u := f.temp("ks_obj *", fmt.Sprintf("ks_unique(%s)", ptr))
		f.line("ks_map_%s_set(%s, %s, %s);", mapKind(mt), u, key, val)
		return
	}
	root, steps := f.prepPlace(s.Target)
	t := f.g.info.Types[s.Target]
	rv := f.evalOwnedTemp(s.Value)
	ptr := f.walkPlace(root, steps)
	if t.Managed() {
		f.line("ks_release(*%s);", ptr)
	}
	f.line("*%s = %s;", ptr, rv)
}

func (f *fnGen) methodCall(e *CallExpr, fn *Func) (string, bool) {
	sel := e.Fn.(*SelectorExpr)
	var recv string
	if fn.RecvMutable {
		root, steps := f.prepPlace(sel.X)
		recv = f.walkPlace(root, steps)
	} else {
		recv = f.eval(sel.X)
	}
	args := make([]string, 0, len(e.Args)+1)
	args = append(args, recv)
	for _, a := range e.Args {
		args = append(args, f.eval(a))
	}
	call := fmt.Sprintf("%s(%s)", fn.CName, strings.Join(args, ", "))
	if fn.Result == Void {
		f.line("%s;", call)
		return "", false
	}
	return f.temp(cType(fn.Result), call), fn.Result.Managed()
}

func (f *fnGen) stringify(e Expr) string {
	switch f.g.info.Types[e].Kind {
	case KInt:
		s := f.temp("ks_obj *", fmt.Sprintf("ks_str_int(%s)", f.eval(e)))
		f.releaseLater(s)
		return s
	case KFloat:
		s := f.temp("ks_obj *", fmt.Sprintf("ks_str_float(%s)", f.eval(e)))
		f.releaseLater(s)
		return s
	case KBool:
		s := f.temp("ks_obj *", fmt.Sprintf("ks_str_bool(%s)", f.eval(e)))
		f.releaseLater(s)
		return s
	default:
		return f.eval(e)
	}
}

func (f *fnGen) fmtCall(e *CallExpr) (string, bool) {
	format := f.eval(e.Args[0])
	parts := make([]string, len(e.Args)-1)
	for i, a := range e.Args[1:] {
		parts[i] = f.stringify(a)
	}
	call := fmt.Sprintf("ks_fmt(%s, 0, NULL)", format)
	if len(parts) > 0 {
		call = fmt.Sprintf("ks_fmt(%s, %d, (ks_obj *[]){%s})", format, len(parts), strings.Join(parts, ", "))
	}
	return f.temp("ks_obj *", call), true
}

func (f *fnGen) builtin(e *CallExpr, name string, t Type) (string, bool) {
	info := f.g.info
	switch name {
	case "fmt":
		return f.fmtCall(e)
	case "delete":
		root, steps := f.prepPlace(e.Args[0])
		key := f.eval(e.Args[1])
		ptr := f.walkPlace(root, steps)
		mt := info.Types[e.Args[0]]
		u := f.temp("ks_obj *", fmt.Sprintf("ks_unique(%s)", ptr))
		return f.temp("bool", fmt.Sprintf("ks_map_%s_delete(%s, %s)", mapKind(mt), u, key)), false
	case "push":
		root, steps := f.prepPlace(e.Args[0])
		arrT := info.Types[e.Args[0]]
		v := f.evalOwnedTemp(e.Args[1])
		ptr := f.walkPlace(root, steps)
		f.line("ks_arr_%s_push(ks_unique(%s), %s);", arrKind(arrT.Elem), ptr, v)
		return "", false
	case "pop":
		root, steps := f.prepPlace(e.Args[0])
		ptr := f.walkPlace(root, steps)
		return f.temp(cType(t), fmt.Sprintf("ks_arr_%s_pop(ks_unique(%s))", arrKind(t), ptr)), t.Managed()
	case "fill":
		v := f.evalOwnedTemp(e.Args[0])
		n := f.eval(e.Args[1])
		return f.temp("ks_obj *", fmt.Sprintf("ks_arr_%s_fill(%s, %s)", arrKind(t.Elem), v, n)), true
	}
	args := make([]string, len(e.Args))
	for i, a := range e.Args {
		args[i] = f.eval(a)
	}
	call := func(fn string) string {
		return fmt.Sprintf("%s(%s)", fn, strings.Join(args, ", "))
	}
	var argT Type
	if len(e.Args) > 0 {
		argT = info.Types[e.Args[0]]
	}
	switch name {
	case "len":
		if argT == StringType {
			return f.temp("int64_t", call("ks_str_len")), false
		}
		if argT.Kind == KMap {
			return f.temp("int64_t", call("ks_map_"+mapKind(argT)+"_len")), false
		}
		return f.temp("int64_t", call("ks_arr_"+arrKind(argT.Elem)+"_len")), false
	case "str":
		switch argT.Kind {
		case KInt:
			return f.temp("ks_obj *", call("ks_str_int")), true
		case KFloat:
			return f.temp("ks_obj *", call("ks_str_float")), true
		case KBool:
			return f.temp("ks_obj *", call("ks_str_bool")), true
		}
		return args[0], false
	case "abs":
		if t == IntType {
			return f.temp("int64_t", call("ks_abs_i64")), false
		}
		return f.temp("double", call("fabs")), false
	case "min", "max":
		suffix := "_f64"
		if t == IntType {
			suffix = "_i64"
		}
		return f.temp(cType(t), call("ks_"+name+suffix)), false
	case "slice":
		return f.temp("ks_obj *", call("ks_arr_"+arrKind(t.Elem)+"_slice")), true
	case "has":
		return f.temp("bool", call("ks_map_"+mapKind(argT)+"_has")), false
	case "keys":
		return f.temp("ks_obj *", call("ks_map_"+mapKind(argT)+"_keys")), true
	case "values":
		return f.temp("ks_obj *", call("ks_map_"+mapKind(argT)+"_values")), true
	}
	sig := fixedBuiltins[name]
	if sig.result == Void {
		f.line("%s;", call(sig.cname))
		return "", false
	}
	return f.temp(cType(sig.result), call(sig.cname)), sig.result.Managed()
}
