package compiler

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
)

type gen struct {
	prog       *Program
	info       *Info
	lits       map[string]string
	litDecls   strings.Builder
	printFns   map[Type]string
	printProto strings.Builder
	printDefs  strings.Builder
}

func GenerateC(prog *Program) string {
	g := &gen{prog: prog, info: prog.Info, lits: map[string]string{}, printFns: map[Type]string{}}
	var body strings.Builder
	for _, m := range prog.Modules {
		for _, fn := range m.Order {
			if !fn.Extern {
				f := &fnGen{g: g, out: &body}
				f.function(fn)
			}
		}
	}
	var sb strings.Builder
	sb.WriteString(runtimeC())
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
		sb.WriteString("\n")
		for _, h := range names {
			fmt.Fprintf(&sb, "#include <%s>\n", h)
		}
	}
	for _, si := range prog.Structs {
		g.structDef(&sb, si)
	}
	sb.WriteString("\n")
	sb.WriteString(g.litDecls.String())
	externs := map[string]bool{}
	for _, m := range prog.Modules {
		for _, fn := range m.Order {
			if fn.Extern && !externs[fn.CName] {
				externs[fn.CName] = true
				externWrapper(&sb, fn)
			}
		}
	}
	sb.WriteString(g.printProto.String())
	for _, m := range prog.Modules {
		for _, fn := range m.Order {
			if !fn.Extern {
				fmt.Fprintf(&sb, "static %s;\n", prototype(fn))
			}
		}
	}
	sb.WriteString(g.printDefs.String())
	sb.WriteString(body.String())
	if prog.Tests != nil || prog.Main == nil {
		g.testMain(&sb, prog)
		return sb.String()
	}
	sb.WriteString("\nint main(int argc, char **argv) {\n")
	sb.WriteString("\tks_argc = argc;\n\tks_argv = argv;\n")
	sb.WriteString("\tsetvbuf(stdout, NULL, _IOLBF, BUFSIZ);\n")
	sb.WriteString("\tsetvbuf(stderr, NULL, _IOLBF, BUFSIZ);\n")
	sb.WriteString("\tks_init_stack((uintptr_t)__builtin_frame_address(0));\n")
	sb.WriteString("\tks_init_seed();\n")
	if prog.Main.Result.Kind == KResult {
		fmt.Fprintf(&sb, "\tks_obj *ks_rc = %s();\n\tfflush(stdout);\n\treturn ks_main_result(ks_rc);\n", prog.Main.CName)
	} else if prog.Main.Result == IntType {
		fmt.Fprintf(&sb, "\tint64_t ks_rc = %s();\n\tfflush(stdout);\n\treturn (int)ks_rc;\n", prog.Main.CName)
	} else {
		fmt.Fprintf(&sb, "\t%s();\n\tfflush(stdout);\n\treturn 0;\n", prog.Main.CName)
	}
	sb.WriteString("}\n")
	return sb.String()
}

func cType(t Type) string {
	switch t.Kind {
	case KInt:
		return "int64_t"
	case KBool:
		return "bool"
	case KFloat:
		return "double"
	case KString, KArray, KMap, KStruct, KResult:
		return "ks_obj *"
	}
	return "void"
}

func zeroValue(t Type) string {
	switch t.Kind {
	case KBool:
		return "false"
	case KFloat:
		return "0.0"
	case KString, KArray, KMap, KStruct, KResult:
		return "NULL"
	}
	return "0"
}

func mapKind(t Type) string {
	k := "i64"
	if t.Key == StringType {
		k = "str"
	}
	return k + "_" + arrKind(t.Elem)
}

func arrKind(elem Type) string {
	switch elem.Kind {
	case KInt:
		return "i64"
	case KBool:
		return "bool"
	case KFloat:
		return "f64"
	}
	return "obj"
}

func (g *gen) structDef(sb *strings.Builder, si *StructInfo) {
	n := si.CName
	sb.WriteString("\ntypedef struct {\n\tks_obj h;\n")
	for _, f := range si.Fields {
		fmt.Fprintf(sb, "\t%s f_%s;\n", cType(f.Type), f.Name)
	}
	fmt.Fprintf(sb, "} %s;\n\n", n)
	fmt.Fprintf(sb, "KS_RT void %s_drop(ks_obj *o) {\n\t%s *s = (%s *)o;\n", n, n, n)
	for _, f := range si.Fields {
		if f.Type.Managed() {
			fmt.Fprintf(sb, "\tks_release(s->f_%s);\n", f.Name)
		}
	}
	sb.WriteString("\t(void)s;\n\tfree(o);\n}\n\n")
	fmt.Fprintf(sb, "KS_RT ks_obj *%s_clone(ks_obj *o) {\n\t%s *c = ks_alloc(sizeof *c);\n\t*c = *(%s *)o;\n\tks_init_obj(&c->h, %s_drop, %s_clone);\n", n, n, n, n, n)
	for _, f := range si.Fields {
		if f.Type.Managed() {
			fmt.Fprintf(sb, "\tks_retain(c->f_%s);\n", f.Name)
		}
	}
	sb.WriteString("\treturn &c->h;\n}\n\n")
	params := []string{}
	for _, f := range si.Fields {
		params = append(params, fmt.Sprintf("%s f_%s", cType(f.Type), f.Name))
	}
	if len(params) == 0 {
		params = []string{"void"}
	}
	fmt.Fprintf(sb, "KS_RT ks_obj *%s_new(%s) {\n\t%s *s = ks_alloc(sizeof *s);\n\tks_init_obj(&s->h, %s_drop, %s_clone);\n", n, strings.Join(params, ", "), n, n, n)
	for _, f := range si.Fields {
		fmt.Fprintf(sb, "\ts->f_%s = f_%s;\n", f.Name, f.Name)
	}
	sb.WriteString("\treturn &s->h;\n}\n")
}

func (g *gen) literal(s string) string {
	if name, ok := g.lits[s]; ok {
		return name
	}
	name := fmt.Sprintf("ks_lit%d", len(g.lits))
	g.lits[s] = name
	fmt.Fprintf(&g.litDecls, "static ks_str %s = {{-1, NULL, NULL, NULL}, %d, %s};\n", name, len(s), cString(s))
	return name
}

// Print helpers write to the C stream named by out (stdout, stderr, or the
// out parameter of a generated print function).
func printScalar(t Type, x, out string, quoted bool) string {
	switch t.Kind {
	case KInt:
		return fmt.Sprintf("ks_print_i64(%s, %s);", x, out)
	case KBool:
		return fmt.Sprintf("ks_print_bool(%s, %s);", x, out)
	case KFloat:
		return fmt.Sprintf("ks_print_f64(%s, %s);", x, out)
	case KString:
		if quoted {
			return fmt.Sprintf("ks_print_str_quoted(%s, %s);", x, out)
		}
		return fmt.Sprintf("ks_print_str(%s, %s);", x, out)
	}
	return ""
}

func (g *gen) printValue(t Type, x, out string, quoted bool) string {
	if t.Kind == KArray || t.Kind == KMap || t.Kind == KStruct || t.Kind == KResult {
		return fmt.Sprintf("%s(%s, %s);", g.printFn(t), x, out)
	}
	return printScalar(t, x, out, quoted)
}

// resField names the payload of a ks_res for a value of type t.
func resField(t Type) string {
	switch t.Kind {
	case KInt:
		return "v.i"
	case KBool:
		return "v.b"
	case KFloat:
		return "v.f"
	}
	return "v.o"
}

func (g *gen) printFn(t Type) string {
	if name, ok := g.printFns[t]; ok {
		return name
	}
	name := fmt.Sprintf("ks_print_t%d", len(g.printFns))
	g.printFns[t] = name
	fmt.Fprintf(&g.printProto, "static void %s(ks_obj *v, FILE *out);\n", name)
	var sb strings.Builder
	fmt.Fprintf(&sb, "\nstatic void %s(ks_obj *v, FILE *out) {\n\tks_stack_check();\n", name)
	if t.Kind == KResult {
		sb.WriteString("\tks_res *r = (ks_res *)v;\n\tif (r->ok) {\n")
		if t.Elem == Void {
			sb.WriteString("\t\tfputs(\"ok()\", out);\n")
		} else {
			fmt.Fprintf(&sb, "\t\tfputs(\"ok(\", out);\n\t\t%s\n\t\tfputc(')', out);\n", g.printValue(t.Elem, "r->"+resField(t.Elem), "out", true))
		}
		sb.WriteString("\t} else {\n\t\tfputs(\"err(\", out);\n\t\tks_print_str_quoted(r->msg, out);\n\t\tfputc(')', out);\n\t}\n")
	} else if t.Kind == KArray {
		k := arrKind(t.Elem)
		fmt.Fprintf(&sb, "\tks_arr_%s *a = (ks_arr_%s *)v;\n\tfputc('[', out);\n", k, k)
		sb.WriteString("\tfor (int64_t i = 0; i < a->len; i++) {\n\t\tif (i > 0) fputs(\", \", out);\n")
		fmt.Fprintf(&sb, "\t\t%s\n\t}\n\tfputc(']', out);\n", g.printValue(t.Elem, "a->data[i]", "out", true))
	} else if t.Kind == KMap {
		k := mapKind(t)
		fmt.Fprintf(&sb, "\tks_map_%s *m = (ks_map_%s *)v;\n\tfputc('{', out);\n", k, k)
		sb.WriteString("\tint64_t shown = 0;\n\tfor (int64_t i = 0; i < m->n; i++) {\n\t\tif (!m->alive[i]) continue;\n\t\tif (shown++ > 0) fputs(\", \", out);\n")
		fmt.Fprintf(&sb, "\t\t%s\n\t\tfputs(\": \", out);\n\t\t%s\n", g.printValue(t.Key, "m->keys[i]", "out", true), g.printValue(t.Elem, "m->vals[i]", "out", true))
		sb.WriteString("\t}\n\tfputc('}', out);\n")
	} else {
		si := t.Struct
		fmt.Fprintf(&sb, "\t%s *s = (%s *)v;\n\t(void)s;\n", si.CName, si.CName)
		if len(si.Fields) == 0 {
			fmt.Fprintf(&sb, "\tfputs(%s, out);\n", cString(si.Name+" {}"))
		} else {
			fmt.Fprintf(&sb, "\tfputs(%s, out);\n", cString(si.Name+" { "))
			for i, f := range si.Fields {
				prefix := f.Name + ": "
				if i > 0 {
					prefix = ", " + prefix
				}
				fmt.Fprintf(&sb, "\tfputs(%s, out);\n\t%s\n", cString(prefix), g.printValue(f.Type, "s->f_"+f.Name, "out", true))
			}
			sb.WriteString("\tfputs(\" }\", out);\n")
		}
	}
	sb.WriteString("}\n")
	g.printDefs.WriteString(sb.String())
	return name
}

func prototype(fn *Func) string {
	params := []string{}
	if fn.Recv != nil {
		if fn.Recv.Slot {
			params = append(params, "ks_obj **"+fn.Recv.CName)
		} else {
			params = append(params, fmt.Sprintf("%s %s", cType(fn.Recv.Type), fn.Recv.CName))
		}
	}
	for _, p := range fn.Params {
		params = append(params, fmt.Sprintf("%s %s", cType(p.Type), p.CName))
	}
	if len(params) == 0 {
		params = []string{"void"}
	}
	return fmt.Sprintf("%s %s(%s)", cType(fn.Result), fn.CName, strings.Join(params, ", "))
}

func externCName(fn *Func) string {
	return "ks_c_" + fn.CName
}

func externWrapper(sb *strings.Builder, fn *Func) {
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
		fmt.Fprintf(sb, "static void %s(%s) {\n\t%s;\n}\n\n", externCName(fn), sig, call)
	} else {
		fmt.Fprintf(sb, "static %s %s(%s) {\n\treturn %s;\n}\n\n", cType(fn.Result), externCName(fn), sig, call)
	}
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

func floatLiteral(v float64) string {
	s := strconv.FormatFloat(v, 'g', -1, 64)
	if !strings.ContainsAny(s, ".e") {
		s += ".0"
	}
	return s
}

func intLiteral(v int64) string {
	if v == math.MinInt64 {
		return "INT64_MIN"
	}
	return fmt.Sprintf("INT64_C(%d)", v)
}

// testMain runs each test function in a forked child, so a runtime error,
// failed assert or returned error fails only that test. KESTREL_TEST_RUN
// selects tests whose names contain it; KESTREL_TEST_TIMEOUT (seconds,
// default 60) limits each one. The child exits through exit(), so leak
// checking still runs in sanitized builds.
func (g *gen) testMain(sb *strings.Builder, prog *Program) {
	sb.WriteString("\nint main(int argc, char **argv) {\n")
	sb.WriteString("\tks_argc = argc;\n\tks_argv = argv;\n")
	sb.WriteString("\tsetvbuf(stdout, NULL, _IOLBF, BUFSIZ);\n\tsetvbuf(stderr, NULL, _IOLBF, BUFSIZ);\n")
	sb.WriteString("\tks_init_stack((uintptr_t)__builtin_frame_address(0));\n\tks_init_seed();\n")
	sb.WriteString("\tconst char *filter = getenv(\"KESTREL_TEST_RUN\");\n")
	sb.WriteString("\tconst char *tv = getenv(\"KESTREL_TEST_TIMEOUT\");\n\tunsigned timeout = tv != NULL && atoi(tv) > 0 ? (unsigned)atoi(tv) : 60;\n")
	sb.WriteString("\tint passed = 0, failed = 0;\n")
	for _, fn := range prog.Tests {
		name := cString(fn.Name)
		fmt.Fprintf(sb, "\tif (filter == NULL || strstr(%s, filter) != NULL) {\n", name)
		sb.WriteString("\t\tfflush(stdout);\n\t\tfflush(stderr);\n\t\tint64_t t0 = ks_clock_ms();\n\t\tpid_t pid = fork();\n")
		sb.WriteString("\t\tif (pid < 0) ks_panic(\"kestrel test: fork failed\");\n")
		sb.WriteString("\t\tif (pid == 0) {\n\t\t\talarm(timeout);\n")
		if fn.Result.Kind == KResult {
			fmt.Fprintf(sb, "\t\t\tint code = ks_main_result(%s());\n\t\t\tfflush(stdout);\n\t\t\texit(code);\n", fn.CName)
		} else {
			fmt.Fprintf(sb, "\t\t\t%s();\n\t\t\tfflush(stdout);\n\t\t\texit(0);\n", fn.CName)
		}
		sb.WriteString("\t\t}\n\t\tint status = 0;\n\t\twhile (waitpid(pid, &status, 0) < 0 && errno == EINTR) {}\n")
		sb.WriteString("\t\tint64_t ms = ks_clock_ms() - t0;\n")
		sb.WriteString("\t\tif (WIFEXITED(status) && WEXITSTATUS(status) == 0) {\n\t\t\tpassed++;\n")
		fmt.Fprintf(sb, "\t\t\tprintf(\"ok   %%s (%%\" PRId64 \"ms)\\n\", %s, ms);\n", name)
		sb.WriteString("\t\t} else {\n\t\t\tfailed++;\n")
		fmt.Fprintf(sb, "\t\t\tif (WIFSIGNALED(status) && WTERMSIG(status) == SIGALRM) printf(\"FAIL %%s (timed out after %%us)\\n\", %s, timeout);\n", name)
		fmt.Fprintf(sb, "\t\t\telse if (WIFSIGNALED(status)) printf(\"FAIL %%s (killed by signal %%d)\\n\", %s, WTERMSIG(status));\n", name)
		fmt.Fprintf(sb, "\t\t\telse printf(\"FAIL %%s (exit status %%d)\\n\", %s, WEXITSTATUS(status));\n", name)
		sb.WriteString("\t\t}\n\t}\n")
	}
	sb.WriteString("\tprintf(\"%d passed, %d failed\\n\", passed, failed);\n\treturn failed > 0 ? 1 : 0;\n}\n")
}
