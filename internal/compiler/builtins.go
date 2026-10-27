package compiler

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
	case "len":
		if !argc(1) {
			return IntType
		}
		t := c.valueExpr(e.Args[0])
		if t != Invalid && t != StringType && t.Kind != KArray {
			c.errorf(e.Args[0].P(), "len expects a string or an array, not %s", t)
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
	}
	panic("unknown builtin " + name)
}
