package compiler

import "fmt"

type builtinSig struct {
	params []Type
	result Type
	cname  string
}

var stringArray = ArrayOf(StringType)

var fixedBuiltins = map[string]builtinSig{
	"float":       {[]Type{IntType}, FloatType, "(double)"},
	"int":         {[]Type{FloatType}, IntType, "ks_float_to_int"},
	"sqrt":        {[]Type{FloatType}, FloatType, "sqrt"},
	"floor":       {[]Type{FloatType}, FloatType, "floor"},
	"ceil":        {[]Type{FloatType}, FloatType, "ceil"},
	"pow":         {[]Type{FloatType, FloatType}, FloatType, "pow"},
	"substr":      {[]Type{StringType, IntType, IntType}, StringType, "ks_substr"},
	"find":        {[]Type{StringType, StringType}, IntType, "ks_find"},
	"contains":    {[]Type{StringType, StringType}, BoolType, "ks_contains"},
	"starts_with": {[]Type{StringType, StringType}, BoolType, "ks_starts_with"},
	"ends_with":   {[]Type{StringType, StringType}, BoolType, "ks_ends_with"},
	"split":       {[]Type{StringType, StringType}, stringArray, "ks_split"},
	"join":        {[]Type{stringArray, StringType}, StringType, "ks_join"},
	"trim":        {[]Type{StringType}, StringType, "ks_trim"},
	"upper":       {[]Type{StringType}, StringType, "ks_upper"},
	"lower":       {[]Type{StringType}, StringType, "ks_lower"},
	"byte_at":     {[]Type{StringType, IntType}, IntType, "ks_byte_at"},
	"chr":         {[]Type{IntType}, StringType, "ks_chr"},
	"parse_int":   {[]Type{StringType}, IntType, "ks_parse_int"},
	"is_int":      {[]Type{StringType}, BoolType, "ks_is_int"},
	"parse_float": {[]Type{StringType}, FloatType, "ks_parse_float"},
	"is_float":    {[]Type{StringType}, BoolType, "ks_is_float"},
	"read_line":   {nil, StringType, "ks_read_line"},
	"eof":         {nil, BoolType, "ks_eof"},
	"args":        {nil, stringArray, "ks_args"},
	"read_file":   {[]Type{StringType}, StringType, "ks_read_file"},
	"write_file":  {[]Type{StringType, StringType}, BoolType, "ks_write_file"},
	"file_exists": {[]Type{StringType}, BoolType, "ks_file_exists"},
	"tcp_listen":  {[]Type{StringType, IntType}, IntType, "ks_tcp_listen"},
	"tcp_port":    {[]Type{IntType}, IntType, "ks_tcp_port"},
	"tcp_accept":  {[]Type{IntType}, IntType, "ks_tcp_accept"},
	"tcp_connect": {[]Type{StringType, IntType}, IntType, "ks_tcp_connect"},
	"tcp_read":    {[]Type{IntType, IntType}, StringType, "ks_tcp_read"},
	"tcp_write":   {[]Type{IntType, StringType}, IntType, "ks_tcp_write"},
	"tcp_close":   {[]Type{IntType}, Void, "ks_tcp_close"},
	"exit":        {[]Type{IntType}, Void, "ks_exit"},
	"assert":      {[]Type{BoolType, StringType}, Void, "ks_assert"},
}

var builtinNames = map[string]bool{
	"len": true, "str": true, "abs": true, "min": true, "max": true,
	"push": true, "pop": true, "fill": true, "slice": true,
	"has": true, "keys": true, "values": true, "delete": true,
	"fmt": true,
}

func init() {
	for name := range fixedBuiltins {
		builtinNames[name] = true
	}
}

func (c *checker) builtin(e *CallExpr, name string) Type {
	c.info.Builtins[e] = name
	argc := func(n int) bool {
		if len(e.Args) == n {
			return true
		}
		for _, a := range e.Args {
			c.valueExpr(a)
		}
		c.errorf(e.Pos, "%s expects %d argument%s, got %d", name, n, plural(n), len(e.Args))
		return false
	}
	if sig, ok := fixedBuiltins[name]; ok {
		if !argc(len(sig.params)) {
			return sig.result
		}
		for i, a := range e.Args {
			want := sig.params[i]
			got := c.exprWant(a, want)
			if got != Invalid && got != want {
				c.errorf(a.P(), "argument %d of %s must be %s, not %s", i+1, name, want, got)
			}
		}
		return sig.result
	}
	switch name {
	case "fmt":
		return c.builtinFmt(e)
	case "len":
		if !argc(1) {
			return IntType
		}
		t := c.valueExpr(e.Args[0])
		if t != Invalid && t != StringType && t.Kind != KArray && t.Kind != KMap {
			c.errorf(e.Args[0].P(), "len expects a string, an array or a map, not %s", t)
		}
		return IntType
	case "str":
		if !argc(1) {
			return StringType
		}
		t := c.valueExpr(e.Args[0])
		if t != Invalid && !t.Comparable() {
			c.errorf(e.Args[0].P(), "str expects int, float, bool or string, not %s (use print to show arrays and structs)", t)
		}
		return StringType
	case "abs":
		if !argc(1) {
			return Invalid
		}
		t := c.valueExpr(e.Args[0])
		if t != Invalid && !t.Numeric() {
			c.errorf(e.Args[0].P(), "abs expects int or float, not %s", t)
			return Invalid
		}
		return t
	case "min", "max":
		if !argc(2) {
			return Invalid
		}
		x := c.valueExpr(e.Args[0])
		y := c.valueExpr(e.Args[1])
		if x == Invalid || y == Invalid {
			return Invalid
		}
		if x != y || !x.Numeric() {
			c.errorf(e.Pos, "%s expects two ints or two floats, not %s and %s", name, x, y)
			return Invalid
		}
		return x
	case "push", "pop":
		n := 2
		if name == "pop" {
			n = 1
		}
		if c.allowMut != e {
			c.errorf(e.Pos, "%s changes its array argument, so it may only be used as a statement or as the whole value of a let, var or assignment", name)
		}
		if !argc(n) {
			return Invalid
		}
		if !isPlace(e.Args[0]) {
			c.valueExpr(e.Args[0])
			c.errorf(e.Args[0].P(), "the first argument of %s must be a var, field or array element", name)
			return Invalid
		}
		t := c.place(e.Args[0], e.Args[0])
		if t != Invalid && t.Kind != KArray {
			c.errorf(e.Args[0].P(), "%s expects an array, not %s", name, t)
			t = Invalid
		}
		if name == "pop" {
			if t == Invalid {
				return Invalid
			}
			return t.Elem
		}
		var want Type
		if t != Invalid {
			want = t.Elem
		}
		got := c.exprWant(e.Args[1], want)
		if want != nil && got != Invalid && got != want {
			c.errorf(e.Args[1].P(), "cannot push %s onto %s", got, t)
		}
		return Void
	case "fill":
		if !argc(2) {
			return Invalid
		}
		t := c.valueExpr(e.Args[0])
		n := c.valueExpr(e.Args[1])
		if n != Invalid && n != IntType {
			c.errorf(e.Args[1].P(), "argument 2 of fill must be int, not %s", n)
		}
		return ArrayOf(t)
	case "slice":
		if !argc(3) {
			return Invalid
		}
		t := c.valueExpr(e.Args[0])
		for i, a := range e.Args[1:] {
			if got := c.valueExpr(a); got != Invalid && got != IntType {
				c.errorf(a.P(), "argument %d of slice must be int, not %s", i+2, got)
			}
		}
		if t != Invalid && t.Kind != KArray {
			c.errorf(e.Args[0].P(), "slice expects an array, not %s (use substr for strings)", t)
			return Invalid
		}
		return t
	case "delete":
		if c.allowMut != e {
			c.errorf(e.Pos, "delete changes its map argument, so it may only be used as a statement or as the whole value of a let, var or assignment")
		}
		if !argc(2) {
			return BoolType
		}
		if !isPlace(e.Args[0]) {
			c.valueExpr(e.Args[0])
			c.errorf(e.Args[0].P(), "the first argument of delete must be a var, field or element")
			c.valueExpr(e.Args[1])
			return BoolType
		}
		t := c.place(e.Args[0], e.Args[0])
		if t != Invalid && t.Kind != KMap {
			c.errorf(e.Args[0].P(), "delete expects a map, not %s", t)
			c.valueExpr(e.Args[1])
			return BoolType
		}
		kt := c.exprWant(e.Args[1], mapKey(t))
		if t != Invalid && kt != Invalid && kt != t.Key {
			c.errorf(e.Args[1].P(), "map key must be %s, not %s", t.Key, kt)
		}
		return BoolType
	case "has":
		if !argc(2) {
			return BoolType
		}
		t := c.valueExpr(e.Args[0])
		if t != Invalid && t.Kind != KMap {
			c.errorf(e.Args[0].P(), "has expects a map, not %s", t)
			c.valueExpr(e.Args[1])
			return BoolType
		}
		kt := c.exprWant(e.Args[1], mapKey(t))
		if t != Invalid && kt != Invalid && kt != t.Key {
			c.errorf(e.Args[1].P(), "map key must be %s, not %s", t.Key, kt)
		}
		return BoolType
	case "keys":
		if !argc(1) {
			return Invalid
		}
		t := c.valueExpr(e.Args[0])
		if t != Invalid && t.Kind != KMap {
			c.errorf(e.Args[0].P(), "keys expects a map, not %s", t)
			return Invalid
		}
		if t == Invalid {
			return Invalid
		}
		return ArrayOf(t.Key)
	case "values":
		if !argc(1) {
			return Invalid
		}
		t := c.valueExpr(e.Args[0])
		if t != Invalid && t.Kind != KMap {
			c.errorf(e.Args[0].P(), "values expects a map, not %s", t)
			return Invalid
		}
		if t == Invalid {
			return Invalid
		}
		return ArrayOf(t.Elem)
	}
	panic("unknown builtin " + name)
}

func mapKey(t Type) Type {
	if t != nil && t.Kind == KMap {
		return t.Key
	}
	return nil
}

func (c *checker) builtinFmt(e *CallExpr) Type {
	c.info.Builtins[e] = "fmt"
	if len(e.Args) == 0 {
		c.errorf(e.Pos, "fmt expects a format string and one value for each {}")
		return StringType
	}
	got := c.exprWant(e.Args[0], StringType)
	if got != Invalid && got != StringType {
		c.errorf(e.Args[0].P(), "argument 1 of fmt must be string, not %s", got)
	}
	holes := -1
	if lit, ok := e.Args[0].(*StringLit); ok {
		n, msg := formatHoles(lit.Value)
		if msg != "" {
			c.errorf(lit.Pos, "fmt format: %s", msg)
		} else {
			holes = n
		}
	}
	for i, a := range e.Args[1:] {
		t := c.valueExpr(a)
		if t != Invalid && !t.Comparable() {
			c.errorf(a.P(), "argument %d of fmt must be int, float, bool or string, not %s (print shows arrays, maps and structs)", i+2, t)
		}
	}
	nargs := len(e.Args) - 1
	if holes >= 0 && holes != nargs {
		c.errorf(e.Pos, "fmt format has %d {} placeholder%s, got %d value%s", holes, plural(holes), nargs, plural(nargs))
	}
	return StringType
}

// formatHoles counts {} placeholders. {{ and }} are literal braces.
// The returned message is empty when the format is valid. Byte positions
// start at 1.
func formatHoles(s string) (int, string) {
	n := 0
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '{':
			if i+1 < len(s) && s[i+1] == '{' {
				i++
				continue
			}
			if i+1 < len(s) && s[i+1] == '}' {
				n++
				i++
				continue
			}
			return 0, fmt.Sprintf("unclosed or unsupported '{' at byte %d (only {} is a placeholder, and {{ is a literal brace)", i+1)
		case '}':
			if i+1 < len(s) && s[i+1] == '}' {
				i++
				continue
			}
			return 0, fmt.Sprintf("lone '}' at byte %d (write }} for a literal brace)", i+1)
		}
	}
	return n, ""
}
