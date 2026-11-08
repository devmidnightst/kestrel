package compiler

import "fmt"

type builtinSig struct {
	params []Type
	result Type
	cname  string
}

var stringArray = ArrayOf(StringType)

var intArray = ArrayOf(IntType)

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

	"try_read_file":   {[]Type{StringType}, ResultOf(StringType), "ks_try_read_file"},
	"try_write_file":  {[]Type{StringType, StringType}, ResultOf(Void), "ks_try_write_file"},
	"try_parse_int":   {[]Type{StringType}, ResultOf(IntType), "ks_try_parse_int"},
	"try_parse_float": {[]Type{StringType}, ResultOf(FloatType), "ks_try_parse_float"},

	"getenv":             {[]Type{StringType}, StringType, "ks_getenv"},
	"has_env":            {[]Type{StringType}, BoolType, "ks_has_env"},
	"time_ms":            {nil, IntType, "ks_time_ms"},
	"clock_ms":           {nil, IntType, "ks_clock_ms"},
	"sleep_ms":           {[]Type{IntType}, Void, "ks_sleep_ms"},
	"pid":                {nil, IntType, "ks_pid"},
	"trap_shutdown":      {nil, Void, "ks_trap_shutdown"},
	"shutdown_requested": {nil, BoolType, "ks_shutdown_requested"},
	"fork_workers":       {[]Type{IntType}, IntType, "ks_fork_workers"},
	"cpu_count":          {nil, IntType, "ks_cpu_count"},

	"random_bytes":     {[]Type{IntType}, StringType, "ks_random_bytes"},
	"random_int":       {[]Type{IntType, IntType}, IntType, "ks_random_int"},
	"sha256":           {[]Type{StringType}, StringType, "ks_sha256_str"},
	"hmac_sha256":      {[]Type{StringType, StringType}, StringType, "ks_hmac_sha256"},
	"pbkdf2_sha256":    {[]Type{StringType, StringType, IntType, IntType}, StringType, "ks_pbkdf2_sha256"},
	"constant_time_eq": {[]Type{StringType, StringType}, BoolType, "ks_constant_time_eq"},
	"hex":              {[]Type{StringType}, StringType, "ks_hex"},
	"unhex":            {[]Type{StringType}, ResultOf(StringType), "ks_unhex"},
	"base64":           {[]Type{StringType}, StringType, "ks_base64"},
	"base64url":        {[]Type{StringType}, StringType, "ks_base64url"},
	"unbase64":         {[]Type{StringType}, ResultOf(StringType), "ks_unbase64"},
	"url_encode":       {[]Type{StringType}, StringType, "ks_url_encode"},
	"url_decode":       {[]Type{StringType}, ResultOf(StringType), "ks_url_decode"},

	"json_valid":  {[]Type{StringType}, BoolType, "ks_json_valid"},
	"json_type":   {[]Type{StringType, StringType}, StringType, "ks_json_type"},
	"json_string": {[]Type{StringType, StringType}, ResultOf(StringType), "ks_json_string"},
	"json_int":    {[]Type{StringType, StringType}, ResultOf(IntType), "ks_json_int"},
	"json_float":  {[]Type{StringType, StringType}, ResultOf(FloatType), "ks_json_float"},
	"json_bool":   {[]Type{StringType, StringType}, ResultOf(BoolType), "ks_json_bool"},
	"json_len":    {[]Type{StringType, StringType}, ResultOf(IntType), "ks_json_len"},
	"json_keys":   {[]Type{StringType, StringType}, ResultOf(stringArray), "ks_json_keys"},
	"json_raw":    {[]Type{StringType, StringType}, ResultOf(StringType), "ks_json_raw"},
	"json_member": {[]Type{StringType, StringType}, ResultOf(StringType), "ks_json_member"},
	"json_quote":  {[]Type{StringType}, StringType, "ks_json_quote"},

	"tcp_listen_shared":   {[]Type{StringType, IntType}, IntType, "ks_tcp_listen_shared"},
	"tcp_poll":            {[]Type{intArray, IntType}, intArray, "ks_tcp_poll"},
	"tcp_set_timeout":     {[]Type{IntType, IntType}, BoolType, "ks_tcp_set_timeout"},
	"tcp_peer":            {[]Type{IntType}, StringType, "ks_tcp_peer"},
	"tcp_try_accept":      {[]Type{IntType}, IntType, "ks_tcp_try_accept"},
	"tcp_send":            {[]Type{IntType, StringType}, IntType, "ks_tcp_send"},
	"tcp_wait":            {[]Type{intArray, intArray, IntType}, ArrayOf(intArray), "ks_tcp_wait"},
	"tcp_connect_timeout": {[]Type{StringType, IntType, IntType}, IntType, "ks_tcp_connect_timeout"},
}

var builtinNames = map[string]bool{
	"len": true, "str": true, "abs": true, "min": true, "max": true,
	"push": true, "pop": true, "fill": true, "slice": true,
	"has": true, "keys": true, "values": true, "delete": true,
	"fmt": true,
	"ok":  true, "err": true, "is_ok": true, "is_err": true,
	"error": true, "unwrap": true, "unwrap_or": true,
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
	case "is_ok", "is_err", "error", "unwrap":
		if !argc(1) {
			if name == "error" {
				return StringType
			}
			if name == "unwrap" {
				return Invalid
			}
			return BoolType
		}
		t := c.resultArg(e.Args[0], name)
		switch name {
		case "error":
			return StringType
		case "unwrap":
			if t == nil {
				return Invalid
			}
			return t.Elem
		}
		return BoolType
	case "unwrap_or":
		if !argc(2) {
			return Invalid
		}
		t := c.resultArg(e.Args[0], name)
		if t == nil {
			c.valueExpr(e.Args[1])
			return Invalid
		}
		if t.Elem == Void {
			c.valueExpr(e.Args[1])
			c.errorf(e.Args[0].P(), "unwrap_or needs a result with a value, not %s", t)
			return Invalid
		}
		got := c.exprWant(e.Args[1], t.Elem)
		if got != Invalid && got != t.Elem {
			c.errorf(e.Args[1].P(), "the fallback of unwrap_or must be %s, not %s", t.Elem, got)
		}
		return t.Elem
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

// resultArg checks the result argument of is_ok, is_err, error, unwrap and
// unwrap_or. It returns nil when the argument is not a result.
func (c *checker) resultArg(a Expr, name string) Type {
	t := c.valueExpr(a)
	if t == Invalid {
		return nil
	}
	if t.Kind != KResult {
		c.errorf(a.P(), "%s expects a result (!T), not %s", name, t)
		return nil
	}
	return t
}

// resultCtor checks ok(v), ok() and err(message). The result type comes from
// the value for ok, and from the expected type (a return, annotation,
// parameter or field) for err.
func (c *checker) resultCtor(e *CallExpr, name string, want Type) Type {
	c.info.Builtins[e] = name
	var wantRes Type
	if want != nil && want != Invalid && want.Kind == KResult {
		wantRes = want
	}
	if name == "err" {
		if len(e.Args) != 1 {
			for _, a := range e.Args {
				c.valueExpr(a)
			}
			c.errorf(e.Pos, "err expects 1 argument (the error message), got %d", len(e.Args))
			if wantRes != nil {
				return wantRes
			}
			return Invalid
		}
		got := c.exprWant(e.Args[0], StringType)
		if got != Invalid && got != StringType {
			c.errorf(e.Args[0].P(), "the message of err must be string, not %s", got)
		}
		if wantRes == nil {
			c.errorf(e.Pos, "cannot infer the result type of err(...); use it where a result type is expected, such as a return in a function returning !T, or annotate it: let r: !int = err(...);")
			return Invalid
		}
		return wantRes
	}
	switch len(e.Args) {
	case 0:
		return ResultOf(Void)
	case 1:
	default:
		for _, a := range e.Args {
			c.valueExpr(a)
		}
		c.errorf(e.Pos, "ok expects at most 1 argument, got %d", len(e.Args))
		return Invalid
	}
	var elemWant Type
	if wantRes != nil && wantRes.Elem != Void {
		elemWant = wantRes.Elem
	}
	t := c.exprWant(e.Args[0], elemWant)
	if t == Invalid {
		if wantRes != nil {
			return wantRes
		}
		return Invalid
	}
	if t.Kind == KResult {
		c.errorf(e.Args[0].P(), "ok of a result would nest result types (%s); return it directly", t)
		return Invalid
	}
	return ResultOf(t)
}
