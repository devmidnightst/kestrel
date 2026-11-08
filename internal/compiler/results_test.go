package compiler

import (
	"strings"
	"testing"
)

func TestResultTypeErrors(t *testing.T) {
	cases := []struct {
		src  string
		want string
	}{
		{"fn g() -> !int { return ok(1); }\nfn f() -> int { let x = try g(); return x; }\nfn main() {}", "try needs a function that returns a result type (!T), but f returns int"},
		{"fn g() -> !int { return ok(1); }\nfn main() { try g(); }", "try needs a function that returns a result type (!T), but main returns nothing"},
		{"fn h() -> !int { return ok(1); }\nfn g() -> !int { let x = 1 + try h(); return ok(x); }\nfn main() {}", "try must be the whole value of a let, var, assignment, return or call statement"},
		{"fn h() -> !int { return ok(1); }\nfn g() -> !int { print(try h()); return ok(1); }\nfn main() {}", "try must be the whole value"},
		{"fn g() -> !int { let x = try 5; return ok(x); }\nfn main() {}", "try expects a result (!T), not int"},
		{"fn n() {}\nfn g() -> !int { try n(); return ok(1); }\nfn main() {}", "try expects a result (!T), but this call returns nothing"},
		{"fn h() -> ! { return ok(); }\nfn g() -> ! { let x = try h(); return ok(); }\nfn main() {}", "try of a ! result does not produce a value"},
		{"fn main() { let r = err(\"x\"); }", "cannot infer the result type of err(...)"},
		{"fn main() { let r: !int = err(1); }", "the message of err must be string, not int"},
		{"fn main() { let r: !int = ok(\"s\"); }", "cannot initialize r of type !int with value of type !string"},
		{"fn main() { let r = ok(1, 2); }", "ok expects at most 1 argument, got 2"},
		{"fn main() { let r: !!int = ok(1); }", "result types cannot be nested"},
		{"fn main() { let r = ok(ok(1)); }", "ok of a result would nest result types"},
		{"fn main() { let r = ok(1); print(r == r); }", "operator == is not defined for !int"},
		{"fn main() { let r = ok(); print(unwrap_or(r, 1)); }", "unwrap_or needs a result with a value, not !"},
		{"fn main() { let r = ok(1); print(unwrap_or(r, \"s\")); }", "the fallback of unwrap_or must be int, not string"},
		{"fn main() { print(is_ok(5)); }", "is_ok expects a result (!T), not int"},
		{"fn main() { print(str(ok(1))); }", "str expects int, float, bool or string, not !int"},
		{"fn main() { print(fmt(\"{}\", ok(1))); }", "argument 2 of fmt must be int, float, bool or string, not !int"},
		{"fn main() -> !int { return ok(1); }", "main must return nothing, int or !, not !int"},
		{"fn g() -> !int { return ok(1); }\nfn main() { let x: int = g(); }", "cannot initialize x of type int with value of type !int"},
	}
	for _, c := range cases {
		expectError(t, c.src, c.want)
	}
}

func TestResultTypesCheck(t *testing.T) {
	expectOK(t, `struct conf { port: int, tags: [string], last: !string }
fn port(s: string) -> !int {
    let n = try try_parse_int(s);
    if n < 0 { return err("negative"); }
    return ok(n);
}
fn tags() -> ![string] { return ok([]); }
fn table() -> !{string: int} { return ok({}); }
fn nothing() -> ! { return ok(); }
fn takes(r: !int) -> int { return unwrap_or(r, 0); }
fn all() -> !conf {
    var t = try tags();
    t = try tags();
    try nothing();
    let m = try table();
    let p = try port("1");
    let c = conf { port: p + len(m), tags: t, last: err("none") };
    return ok(c);
}
fn main() -> ! {
    var rs: [!int] = [ok(1), err("x")];
    push(rs, port("2"));
    let m: {string: !float} = {"a": try_parse_float("1.5")};
    print(rs, m, takes(err("y")), takes(ok(3)));
    return nothing();
}`)
}

func TestParseResultTypes(t *testing.T) {
	for _, src := range []string{
		"fn f() -> ! { return ok(); }\nfn main() {}",
		"fn f(m: !{string: int}) -> !{string: int} { return m; }\nfn main() {}",
		"fn f() -> ![int] { return ok([1]); }\nfn main() {}",
		"fn main() { let r: ! = ok(); let s: ![!int] = ok([ok(1)]); }",
		"struct s { a: !, b: !int }\nfn main() {}",
	} {
		if _, err := Parse("p.ks", []byte(src)); err != nil {
			t.Errorf("parse %q: %v", src, err)
		}
	}
}

var resultCases = []featureCase{
	{"result propagation", `fn parse_port(s: string) -> !int {
    let n = try try_parse_int(s);
    if n < 1 || n > 65535 {
        return err(fmt("port {} out of range", n));
    }
    return ok(n);
}

fn sum_ports(list: [string]) -> !int {
    var total = 0;
    let keep = ["held", "while", "looping"];
    for s in list {
        var i = 0;
        while i < 1 {
            let p = try parse_port(s);
            total = total + p;
            i = i + 1;
        }
    }
    print(len(keep));
    return ok(total);
}

fn main() {
    print(sum_ports(["80", "443"]));
    print(sum_ports(["80", "x", "443"]));
    print(sum_ports(["0"]));
}`, "3\nok(523)\nerr(\"invalid integer \\\"x\\\"\")\nerr(\"port 0 out of range\")\n"},
	{"result values", `struct item { name: string, price: !float }
fn half(x: int) -> !int {
    if x % 2 != 0 { return err(fmt("{} is odd", x)); }
    return ok(x / 2);
}
fn nothing(fail: bool) -> ! {
    if fail { return err("failed"); }
    return ok();
}
fn main() {
    let a = half(10);
    let b = half(7);
    print(a, b, is_ok(a), is_err(b), error(a) == "", error(b));
    print(unwrap(a), unwrap_or(b, -1));
    var rs: [!int] = [];
    for i in 0..4 { push(rs, half(i)); }
    print(rs);
    let m: {string: ![string]} = {"words": ok(split("a b", " ")), "none": err("missing")};
    print(m["words"], unwrap_or(m["none"], ["fallback"]));
    let it = item { name: "rope", price: try_parse_float("4.5") };
    let copy = it;
    print(it, unwrap(copy.price) * 2.0);
    print(nothing(false), nothing(true));
    unwrap(nothing(false));
    let s: !string = ok("text");
    print(s, unwrap(s), try_parse_float("1e3"), try_parse_float("1e"));
}`, "ok(5) err(\"7 is odd\") true true true 7 is odd\n5 -1\n[ok(0), err(\"1 is odd\"), ok(1), err(\"3 is odd\")]\nok([\"a\", \"b\"]) [\"fallback\"]\nitem { name: \"rope\", price: ok(4.5) } 9.0\nok() err(\"failed\")\nok(\"text\") text ok(1000.0) err(\"invalid number \\\"1e\\\"\")\n"},
	{"result files", `fn copy(from: string, to: string) -> ! {
    let data = try try_read_file(from);
    try try_write_file(to, data + "!");
    return ok();
}
fn main() -> ! {
    try copy("main.ks", "copy.txt");
    let back = try try_read_file("copy.txt");
    print(ends_with(back, "}!"));
    let missing = try_read_file("no-such-file.txt");
    print(is_err(missing), contains(error(missing), "no-such-file.txt"));
    print(copy("no-such-file.txt", "x.txt"));
    return ok();
}`, "true\ntrue true\nerr(\"cannot open \\\"no-such-file.txt\\\": No such file or directory\")\n"},
	{"result try in many positions", `struct box { v: [string] }
fn word(s: string) -> !string {
    if s == "" { return err("empty"); }
    return ok(s);
}
fn build(parts: [string]) -> !box {
    var b = box { v: [] };
    var m: {string: string} = {};
    for p in parts {
        let w = try word(p);
        push(b.v, w);
        m[w] = try word(p + "!");
        b.v[0] = try word(b.v[0]);
    }
    return ok(b);
}
fn main() {
    print(build(["a", "b"]));
    print(build(["a", "", "c"]));
}`, "ok(box { v: [\"a\", \"b\"] })\nerr(\"empty\")\n"},
}

func TestResults(t *testing.T) {
	requireCC(t)
	for _, c := range resultCases {
		t.Run(c.name, func(t *testing.T) {
			r := runWith(t, c.src, "", false)
			if r.code != 0 || r.stdout != c.want {
				t.Errorf("exit %d, stderr %q\ngot:\n%s\nwant:\n%s", r.code, r.stderr, r.stdout, c.want)
			}
		})
	}
}

func TestResultsUnderSanitizers(t *testing.T) {
	requireSanitizers(t)
	for _, c := range resultCases {
		t.Run(c.name, func(t *testing.T) {
			r := runWith(t, c.src, "", true)
			if r.code != 0 || r.stdout != c.want || r.stderr != "" {
				t.Errorf("exit %d\nstderr:\n%s\ngot:\n%s", r.code, r.stderr, r.stdout)
			}
		})
	}
}

func TestResultRuntimeErrors(t *testing.T) {
	requireCC(t)
	r := runWith(t, "fn main() { let r: !int = err(\"boom\"); print(\"before\"); print(unwrap(r)); }", "", false)
	if r.code != 101 || r.stdout != "before\n" || !strings.Contains(r.stderr, "runtime error: unwrap of an error: boom") {
		t.Errorf("unwrap: exit %d, out %q, stderr %q", r.code, r.stdout, r.stderr)
	}
	src := `fn load() -> !int { return err("bad config"); }
fn main() -> ! {
    print("starting");
    let n = try load();
    print(n);
    return ok();
}`
	for _, sanitize := range []bool{false, true} {
		if sanitize {
			requireSanitizers(t)
		}
		r = runWith(t, src, "", sanitize)
		if r.code != 1 || r.stdout != "starting\n" || r.stderr != "kestrel: error: bad config\n" {
			t.Errorf("main error (sanitize %v): exit %d, out %q, stderr %q", sanitize, r.code, r.stdout, r.stderr)
		}
	}
	r = runWith(t, "fn main() -> ! { print(\"fine\"); return ok(); }", "", false)
	if r.code != 0 || r.stdout != "fine\n" || r.stderr != "" {
		t.Errorf("main ok: exit %d, out %q, stderr %q", r.code, r.stdout, r.stderr)
	}
}
