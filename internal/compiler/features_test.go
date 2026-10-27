package compiler

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

type runResult struct {
	stdout string
	stderr string
	code   int
}

func runWith(t *testing.T, src, stdin string, sanitize bool, args ...string) runResult {
	t.Helper()
	dir := writeFiles(t, map[string]string{"main.ks": src})
	exe := filepath.Join(dir, "prog")
	if err := Build(filepath.Join(dir, "main.ks"), BuildOptions{Output: exe, Sanitize: sanitize}); err != nil {
		t.Fatalf("build: %v\n%s", err, src)
	}
	var stdout, stderr bytes.Buffer
	cmd := exec.Command(exe, args...)
	cmd.Dir = dir
	cmd.Stdin = strings.NewReader(stdin)
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	cmd.Env = append(os.Environ(), sanitizerEnv()...)
	code := 0
	if err := cmd.Run(); err != nil {
		var exit *exec.ExitError
		if !errors.As(err, &exit) {
			t.Fatal(err)
		}
		code = exit.ExitCode()
	}
	return runResult{stdout.String(), stderr.String(), code}
}

type featureCase struct {
	name string
	src  string
	want string
}

var featureCases = []featureCase{
	{"string map", `fn main() {
    var m: {string: int} = {};
    m["a"] = 1;
    m["b"] = 2;
    m["a"] = 3;
    var copy = m;
    copy["a"] = 9;
    print(m["a"], m["b"], copy["a"], has(m, "b"), has(m, "missing"), len(m));
}`, "3 2 9 true false 2\n"},
	{"int map", `fn main() {
    var m = { 1: "one", 2: "two" };
    m[2] = "TWO";
    print(m[1], m[2], len(m), len(keys(m)), len(values(m)));
}`, "one TWO 2 2 2\n"},
	{"array basics", `fn main() {
    var a = [1, 2, 3];
    push(a, 4);
    a[0] = 10;
    let last = pop(a);
    print(a, len(a), last, a[1]);
    let empty: [string] = [];
    print(empty, len(empty));
}`, "[10, 2, 3] 3 4 2\n[] 0\n"},
	{"array value semantics", `fn bump(a: [int]) -> [int] {
    var b = a;
    b[0] = b[0] + 100;
    return b;
}
fn main() {
    var a = [1, 2];
    var b = a;
    b[1] = 99;
    push(b, 7);
    let c = bump(a);
    print(a, b, c);
}`, "[1, 2] [1, 99, 7] [101, 2]\n"},
	{"nested copy on write", `fn main() {
    var grid = [[1, 2], [3, 4]];
    let snapshot = grid;
    grid[1][0] = 30;
    push(grid[0], 5);
    var row = grid[1];
    row[1] = 40;
    print(grid, snapshot, row);
}`, "[[1, 2, 5], [30, 4]] [[1, 2], [3, 4]] [30, 40]\n"},
	{"structs", `struct Item {
    name: string,
    qty: int,
    tags: [string],
}
struct Order {
    id: int,
    items: [Item],
}
fn total(o: Order) -> int {
    var sum = 0;
    var i = 0;
    while i < len(o.items) {
        sum = sum + o.items[i].qty;
        i = i + 1;
    }
    return sum;
}
fn main() {
    var o = Order { id: 1, items: [] };
    push(o.items, Item { name: "bolt", qty: 3, tags: [] });
    push(o.items, Item { qty: 2, tags: ["x"], name: "nut" });
    let before = o;
    o.items[0].qty = 10;
    push(o.items[1].tags, "y");
    print(o);
    print(before.items[0].qty, total(o), total(before));
}`, "Order { id: 1, items: [Item { name: \"bolt\", qty: 10, tags: [] }, Item { name: \"nut\", qty: 2, tags: [\"x\", \"y\"] }] }\n3 12 5\n"},
	{"floats", `fn main() {
    let x = 1.5;
    print(x * 2.0, x / 0.5, -x, 10.0, 0.1 + 0.2, 1.0 / 3.0);
    print(1e16, 1.5e-7, 123456.789, float(3), int(-2.9), int(2.9));
    print(1.0 / 0.0, -1.0 / 0.0, 2.5 < 3.0, 2.0 == 2.0, sqrt(2.0), floor(2.7), ceil(2.1), pow(3.0, 2.0));
    print(str(0.25), abs(-1.25), min(1.5, -1.5), max(1, 2));
}`, "3.0 3.0 -1.5 10.0 0.30000000000000004 0.3333333333333333\n1e+16 1.5e-07 123456.789 3.0 -2 2\ninf -inf true true 1.4142135623730951 2.0 3.0 9.0\n0.25 1.25 -1.5 2\n"},
	{"string builtins", `fn main() {
    let s = "  Hello, World  ";
    let t = trim(s);
    print(t, len(t), upper(t), lower(t));
    print(find(t, "World"), find(t, "x"), contains(t, ", "), starts_with(t, "He"), ends_with(t, "ld"));
    print(substr(t, 7, 5), byte_at(t, 0), chr(97) + chr(98));
    let parts = split("a,b,,c", ",");
    print(parts, len(parts), join(parts, "+"));
    print(parse_int("-123") * 2, is_int("12a"), is_int("-9223372036854775808"), is_int("9223372036854775808"));
    print(parse_float("1.25e2"), is_float("1."), is_float(".5"), is_float("e5"));
    print("apple" < "banana", "b" >= "abc", str(42) + str(true));
}`, "Hello, World 12 HELLO, WORLD hello, world\n7 -1 true true true\nWorld 72 ab\n[\"a\", \"b\", \"\", \"c\"] 4 a+b++c\n-246 false true false\n125.0 true true false\ntrue true 42true\n"},
	{"fill and slice", `fn main() {
    var rows = fill(fill(0, 2), 2);
    rows[0][1] = 5;
    print(rows, slice([1, 2, 3, 4, 5], 1, 3), slice([1], 1, 0));
}`, "[[0, 5], [0, 0]] [2, 3, 4] []\n"},
	{"shadowed builtin", `fn max(a: int, b: int) -> int {
    return 7;
}
fn main() {
    print(max(1, 2), min(1, 2));
}`, "7 1\n"},
	{"loops release correctly", `fn main() {
    var keep: [string] = [];
    var i = 0;
    while i < 5 {
        let s = "item" + str(i);
        i = i + 1;
        if i == 2 {
            continue;
        }
        if i == 5 {
            break;
        }
        push(keep, s);
    }
    print(keep);
}`, "[\"item0\", \"item2\", \"item3\"]\n"},
	{"returns inside nested scopes", `fn pick(names: [string], want: string) -> string {
    var i = 0;
    while i < len(names) {
        let n = names[i];
        if n == want {
            let found = "found " + n;
            return found;
        }
        i = i + 1;
    }
    return "none";
}
fn main() {
    print(pick(["a", "b", "c"], "b"), pick([], "z"));
}`, "found b none\n"},
	{"for range", `fn main() {
    var s = 0;
    for i in 0..5 { s = s + i; }
    var t = 0;
    for i in 1..=4 {
        if i == 2 { continue; }
        if i == 4 { break; }
        t = t + i;
    }
    var neg = 0;
    for i in -2..2 { neg = neg + i; }
    var one = 0;
    for i in 3..=3 { one = one + 1; }
    var none = 0;
    for i in 5..5 { none = none + 1; }
    for i in 4..2 { none = none + 1; }
    for i in 9223372036854775807..=9223372036854775807 { one = one + 1; }
    print(s, t, neg, one, none);
}`, "10 4 -2 2 0\n"},
	{"for array", `struct box { n: int }
fn main() {
    var words = ["go", "to", "the"];
    var n = 0;
    for w in words {
        if w == "to" { continue; }
        n = n + len(w);
    }
    var nums = [1, 2];
    var extra = 0;
    for x in nums {
        push(nums, x);
        extra = extra + x;
    }
    var boxes = [box { n: 7 }];
    var seen = 0;
    for b in boxes {
        var c = b;
        c.n = 1;
        seen = seen + b.n;
    }
    var empty: [int] = [];
    var hits = 0;
    for x in empty { hits = hits + x; }
    var acc = 0;
    for i in 0..3 {
        var j = 0;
        while j < 2 {
            j = j + 1;
            if j == 1 { continue; }
        }
        acc = acc + i;
    }
    print(n, extra, len(nums), seen, boxes[0].n, hits, acc);
}`, "5 3 4 7 7 0 3\n"},
	{"for return", `fn first(xs: [string]) -> string {
    for s in xs {
        if len(s) > 0 { return s; }
    }
    return "";
}
fn main() {
    print(first(["", "ab", "c"]));
}`, "ab\n"},
	{"fmt and print", `struct pair { a: int, b: string }
fn main() {
    print(fmt("{} is {}", "kestrel", 2026));
    print(fmt("braces {{ }} and {}", true));
    print(fmt("pi {}", 3.5));
    print(fmt("none"));
    print(fmt("{}", "a{b}"));
    print(fmt("{{{}}}", 7));
    var pat = "x{}";
    print(fmt(pat, false));
    var m: {string: int} = {};
    m["a"] = 1;
    var empty: {int: int} = {};
    print(1, [1, 2], pair { a: 1, b: "x" }, m, empty);
}`, "kestrel is 2026\nbraces { } and true\npi 3.5\nnone\na{b}\n{7}\nxfalse\n1 [1, 2] pair { a: 1, b: \"x\" } {\"a\": 1} {}\n"},
}

func TestFeatures(t *testing.T) {
	requireCC(t)
	for _, c := range featureCases {
		t.Run(c.name, func(t *testing.T) {
			r := runWith(t, c.src, "", false)
			if r.code != 0 || r.stdout != c.want {
				t.Errorf("exit %d, stderr %q\ngot:\n%s\nwant:\n%s", r.code, r.stderr, r.stdout, c.want)
			}
		})
	}
}

func TestFeaturesUnderSanitizers(t *testing.T) {
	requireSanitizers(t)
	for _, c := range featureCases {
		t.Run(c.name, func(t *testing.T) {
			r := runWith(t, c.src, "", true)
			if r.code != 0 || r.stdout != c.want || r.stderr != "" {
				t.Errorf("exit %d\nstderr:\n%s\ngot:\n%s", r.code, r.stderr, r.stdout)
			}
		})
	}
}

func TestExamplesUnderSanitizers(t *testing.T) {
	requireSanitizers(t)
	paths, _ := filepath.Glob(filepath.Join("..", "..", "examples", "*.ks"))
	paths = append(paths, filepath.Join("..", "..", "examples", "modules", "main.ks"))
	for _, p := range paths {
		t.Run(filepath.Base(p), func(t *testing.T) {
			_, errOut, code := buildAndRunOpts(t, p, BuildOptions{Sanitize: true}, "arg1")
			if code != 0 || errOut != "" {
				t.Errorf("exit %d\n%s", code, errOut)
			}
		})
	}
}

func TestInput(t *testing.T) {
	requireCC(t)
	src := `fn main() {
    var total = 0;
    var lines = 0;
    var line = read_line();
    while !eof() {
        if is_int(trim(line)) {
            total = total + parse_int(trim(line));
        }
        lines = lines + 1;
        line = read_line();
    }
    print(lines, total, args());
}`
	r := runWith(t, src, "1\n2\r\nx\n39", false, "-v", "two words")
	if r.code != 0 || r.stdout != "4 42 [\"-v\", \"two words\"]\n" {
		t.Errorf("exit %d, out %q, err %q", r.code, r.stdout, r.stderr)
	}
	r = runWith(t, src, "", false)
	if r.stdout != "0 0 []\n" {
		t.Errorf("empty stdin: %q", r.stdout)
	}
}

func TestFiles(t *testing.T) {
	requireCC(t)
	src := `fn main() {
    print(file_exists("data.txt"));
    print(write_file("data.txt", "line one\nline two\n"));
    print(file_exists("data.txt"));
    let lines = split(trim(read_file("data.txt")), "\n");
    print(lines, len(lines));
    print(write_file("missing-dir/x.txt", "x"));
    print(read_file("nope.txt"));
}`
	r := runWith(t, src, "", false)
	want := "false\ntrue\ntrue\n[\"line one\", \"line two\"] 2\nfalse\n"
	if r.code != 101 || r.stdout != want || !strings.Contains(r.stderr, "read_file: cannot open \"nope.txt\"") {
		t.Errorf("exit %d\nout %q\nerr %q", r.code, r.stdout, r.stderr)
	}
}

func TestNewRuntimeErrors(t *testing.T) {
	requireCC(t)
	cases := map[string]string{
		"fn main() { let a = [1, 2]; print(a[2]); }":                               "index out of range: index 2, length 2",
		"fn main() { var a = [1]; a[-1] = 0; }":                                    "index out of range: index -1, length 1",
		"fn main() { var a: [int] = []; let x = pop(a); }":                         "pop from an empty array",
		"fn main() { print(substr(\"abc\", 2, 5)); }":                              "substr out of range",
		"fn main() { print(slice([1, 2], 3, 0)); }":                                "slice out of range",
		"fn main() { print(parse_int(\"12x\")); }":                                 "parse_int: invalid integer \"12x\"",
		"fn main() { print(parse_float(\"\")); }":                                  "parse_float: invalid number",
		"fn main() { print(int(1e300)); }":                                         "int: 1e+300 cannot be converted to int",
		"fn main() { print(int(0.0 / 0.0)); }":                                     "int: nan cannot be converted to int",
		"fn main() { print(chr(256)); }":                                           "chr: 256 is not a byte value",
		"fn main() { print(byte_at(\"\", 0)); }":                                   "index out of range",
		"fn main() { print(split(\"a\", \"\")); }":                                 "split: empty separator",
		"fn main() { print(fill(1, -1)); }":                                        "fill: negative count -1",
		"fn main() { assert(1 > 2, \"math broke\"); }":                             "assertion failed: math broke",
		"fn f(n: int) -> int { return f(n + 1) + 1; }\nfn main() { print(f(0)); }": "stack overflow (recursion too deep)",
	}
	for src, want := range cases {
		r := runWith(t, src, "", false)
		if r.code != 101 || !strings.Contains(r.stderr, "kestrel: runtime error: "+want) {
			t.Errorf("%s\nexit %d, stderr %q, want %q", src, r.code, r.stderr, want)
		}
	}
}

func TestExitBuiltin(t *testing.T) {
	requireCC(t)
	r := runWith(t, "fn main() { print(\"partial\"); exit(3); print(\"never\"); }", "", false)
	if r.code != 3 || r.stdout != "partial\n" {
		t.Errorf("exit %d, out %q", r.code, r.stdout)
	}
}

func TestTCPHTTPOnce(t *testing.T) {
	requireCC(t)
	src, err := os.ReadFile(filepath.Join("..", "..", "examples", "http", "server.ks"))
	if err != nil {
		t.Fatal(err)
	}
	dir := writeFiles(t, map[string]string{"main.ks": string(src)})
	exe := filepath.Join(dir, "prog")
	if err := Build(filepath.Join(dir, "main.ks"), BuildOptions{Output: exe}); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(exe)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if cmd.ProcessState == nil && cmd.Process != nil {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		}
	}()
	if f, ok := stdout.(*os.File); ok {
		_ = f.SetDeadline(time.Now().Add(5 * time.Second))
	}
	line, err := bufio.NewReader(stdout).ReadString('\n')
	if err != nil {
		t.Fatalf("port: %v stderr %s", err, stderr.String())
	}
	conn, err := net.DialTimeout("tcp", "127.0.0.1:"+strings.TrimSpace(line), 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	if _, err := io.WriteString(conn, "GET / HTTP/1.1\r\nHost: localhost\r\n\r\n"); err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(conn)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "HTTP/1.1 200 OK") || !strings.HasSuffix(string(body), "ok") {
		t.Fatalf("response %q", body)
	}
	_ = cmd.Wait()
}

func TestMemoryStaysBounded(t *testing.T) {
	requireCC(t)
	if runtime.GOOS != "linux" {
		t.Skip("uses ulimit -v")
	}
	src := `struct Node {
    label: string,
    kids: [Node],
}
fn main() {
    var i = 0;
    var keep = "";
    while i < 200000 {
        var tree = Node { label: "root" + str(i), kids: [] };
        push(tree.kids, Node { label: "leaf", kids: fill(Node { label: "x", kids: [] }, 50) });
        let copy = tree;
        tree.kids[0].label = "changed";
        let words = split("a,b,c,d,e,f,g,h", ",");
        keep = join(words, "") + copy.kids[0].label;
        i = i + 1;
    }
    print(keep);
}`
	dir := writeFiles(t, map[string]string{"main.ks": src})
	exe := filepath.Join(dir, "prog")
	if err := Build(filepath.Join(dir, "main.ks"), BuildOptions{Output: exe}); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command("sh", "-c", "ulimit -v 65536 && exec \"$0\"", exe).CombinedOutput()
	if err != nil || string(out) != "abcdefghleaf\n" {
		t.Errorf("program under a 64 MiB address space limit: %v %q", err, out)
	}
}
