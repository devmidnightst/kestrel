package compiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestRuntimeErrorLocations(t *testing.T) {
	requireCC(t)
	cases := []struct{ src, want string }{
		{`struct box { v: [int] }
fn (self: box) at(i: int) -> int {
    return self.v[i];
}
fn main() {
    let b = box { v: [1, 2] };
    print(b.at(5));
}`, "    at main.ks:3 in box.at\n"},
		{`fn helper() -> int { return 1; }
fn main() {
    let xs = [1];
    let y = helper() + xs[3];
}`, "    at main.ks:4 in main\n"},
		{`fn main() {
    var i = 0;
    while 10 / i > 0 {
        i = i + 1;
    }
}`, "    at main.ks:3 in main\n"},
	}
	for _, c := range cases {
		r := runWith(t, c.src, "", false)
		if r.code != 101 || !strings.HasSuffix(r.stderr, c.want) || !strings.HasPrefix(r.stderr, "kestrel: runtime error: ") {
			t.Errorf("exit %d, stderr %q, want suffix %q", r.code, r.stderr, c.want)
		}
	}
}

func TestNonBlockingSockets(t *testing.T) {
	requireCC(t)
	src := `fn main() {
    let s = tcp_listen("127.0.0.1", 0);
    let p = tcp_port(s);
    print(tcp_try_accept(s));
    let c = tcp_connect_timeout("127.0.0.1", p, 2000);
    let w = tcp_wait([s], [], 2000);
    print(len(w), len(w[0]), len(w[1]));
    let a = tcp_try_accept(s);
    print(a >= 0, tcp_try_accept(s));
    // The client never reads, so the server's send buffer fills up and
    // tcp_send starts returning 0 instead of blocking.
    let chunk = join(fill("x", 65536), "");
    var total = 0;
    var zero = false;
    var rounds = 0;
    while !zero && rounds < 100000 {
        let n = tcp_send(a, chunk);
        if n == 0 {
            zero = true;
        } else {
            total = total + n;
        }
        rounds = rounds + 1;
    }
    print(zero, total > 0);
    let ready = tcp_wait([], [a], 50);
    print(len(ready[1]));
    print(tcp_send(a, ""), tcp_send(-1, "x"));
    tcp_close(c);
    tcp_close(a);
    tcp_close(s);
    let closed = tcp_listen("127.0.0.1", 0);
    let cp = tcp_port(closed);
    tcp_close(closed);
    print(tcp_connect_timeout("127.0.0.1", cp, 500));
}`
	for _, sanitize := range []bool{false, true} {
		if sanitize {
			requireSanitizers(t)
		}
		r := runWith(t, src, "", sanitize)
		want := "-1\n2 1 0\ntrue -1\ntrue true\n0\n0 -1\n-1\n"
		if r.code != 0 || r.stdout != want || r.stderr != "" {
			t.Errorf("sanitize %v: exit %d\nstdout %q\nwant   %q\nstderr %q", sanitize, r.code, r.stdout, want, r.stderr)
		}
	}
}

func TestForkWorkersRestartsCrashes(t *testing.T) {
	requireCC(t)
	// Worker 1 crashes on its first run (no marker file yet), then is
	// restarted, finds the marker and exits cleanly. Worker 0 exits at once.
	src := `fn main() {
    let w = fork_workers(2);
    if w == 1 {
        if !file_exists("crashed.txt") {
            write_file("crashed.txt", "x");
            let empty: [int] = [];
            print(empty[0]);
        }
        print("worker 1 recovered");
        return;
    }
    print("worker 0 done");
}`
	r := runWith(t, src, "", false)
	if r.code != 0 || !strings.Contains(r.stdout, "worker 0 done\n") || !strings.Contains(r.stdout, "worker 1 recovered\n") ||
		!strings.Contains(r.stderr, "index out of range") || !strings.Contains(r.stderr, "kestrel: worker 1 (pid ") ||
		!strings.Contains(r.stderr, "exited with status 101; restarting") {
		t.Errorf("exit %d\nstdout %q\nstderr %q", r.code, r.stdout, r.stderr)
	}
	if cpu := runWith(t, "fn main() { print(cpu_count() >= 1); }", "", false); cpu.stdout != "true\n" {
		t.Errorf("cpu_count: %q", cpu.stdout)
	}
	bad := runWith(t, "fn main() { let w = fork_workers(0); }", "", false)
	if bad.code != 101 || !strings.Contains(bad.stderr, "worker count 0 must be 1 to 1024") {
		t.Errorf("fork_workers(0): %d %q", bad.code, bad.stderr)
	}
}

// TestMapInsertionOrderStress runs random sets, deletes and copies on a
// string map and an int map, and checks the final keys, values and
// iteration order against an ordered-map model.
func TestMapInsertionOrderStress(t *testing.T) {
	requireCC(t)
	src := `fn main() {
    var s: {string: int} = {};
    var m: {int: string} = {};
    var copy: {string: int} = {};
    var x = 12345;
    for step in 0..20000 {
        x = (x * 1103515245 + 12345) % 2147483648;
        let k = x % 700;
        let op = (x / 700) % 10;
        if op < 6 {
            s["k" + str(k)] = step;
            m[k] = str(step);
        } else if op < 9 {
            delete(s, "k" + str(k));
            delete(m, k);
        } else {
            copy = s;
            s["k" + str(k)] = -step;
        }
    }
    print(len(s), len(m), len(copy));
    print(join(keys(s), ","));
    var vs: [string] = [];
    for v in values(s) { push(vs, str(v)); }
    print(join(vs, ","));
    var ms: [string] = [];
    for k in m { push(ms, str(k) + "=" + m[k]); }
    print(join(ms, ","));
    print(join(keys(copy), ","));
}`
	type entry struct {
		key string
		val int
	}
	type omap struct{ list []entry }
	set := func(o *omap, k string, v int) {
		for i := range o.list {
			if o.list[i].key == k {
				o.list[i].val = v
				return
			}
		}
		o.list = append(o.list, entry{k, v})
	}
	del := func(o *omap, k string) {
		for i := range o.list {
			if o.list[i].key == k {
				o.list = append(o.list[:i:i], o.list[i+1:]...)
				return
			}
		}
	}
	var s, m, cp omap
	x := 12345
	for step := 0; step < 20000; step++ {
		x = (x*1103515245 + 12345) % 2147483648
		k := x % 700
		op := (x / 700) % 10
		ks := "k" + itoa(k)
		switch {
		case op < 6:
			set(&s, ks, step)
			set(&m, itoa(k), step)
		case op < 9:
			del(&s, ks)
			del(&m, itoa(k))
		default:
			cp = omap{append([]entry{}, s.list...)}
			set(&s, ks, -step)
		}
	}
	var keysS, valsS, pairsM, keysC []string
	for _, e := range s.list {
		keysS = append(keysS, e.key)
		valsS = append(valsS, itoa(e.val))
	}
	for _, e := range m.list {
		pairsM = append(pairsM, e.key+"="+itoa(e.val))
	}
	for _, e := range cp.list {
		keysC = append(keysC, e.key)
	}
	want := itoa(len(s.list)) + " " + itoa(len(m.list)) + " " + itoa(len(cp.list)) + "\n" +
		strings.Join(keysS, ",") + "\n" + strings.Join(valsS, ",") + "\n" + strings.Join(pairsM, ",") + "\n" + strings.Join(keysC, ",") + "\n"
	for _, sanitize := range []bool{false, true} {
		if sanitize {
			requireSanitizers(t)
		}
		r := runWith(t, src, "", sanitize)
		if r.code != 0 || r.stdout != want || r.stderr != "" {
			t.Errorf("sanitize %v: exit %d, stderr %q\ngot  %.300q\nwant %.300q", sanitize, r.code, r.stderr, r.stdout, want)
		}
	}
	print := runWith(t, `fn main() { var m = {"z": 1, "a": 2, "m": 3}; delete(m, "a"); m["b"] = 4; m["z"] = 5; print(m, keys(m)); }`, "", false)
	if print.stdout != "{\"z\": 5, \"m\": 3, \"b\": 4} [\"z\", \"m\", \"b\"]\n" {
		t.Errorf("print order: %q", print.stdout)
	}
}

func itoa(n int) string {
	return strconv.Itoa(n)
}

func TestCryptoAndEncoding(t *testing.T) {
	requireCC(t)
	src := `fn main() -> ! {
    print(hex(sha256("abc")));
    print(hex(sha256(join(fill("a", 1000000), ""))));
    let x = "x";
    print(hex(sha256(join(fill(x, 63), ""))), hex(sha256(join(fill(x, 64), ""))));
    print(hex(sha256(join(fill(x, 55), ""))), hex(sha256(join(fill(x, 56), ""))));
    print(hex(hmac_sha256("Jefe", "what do ya want for nothing?")));
    print(hex(hmac_sha256(join(fill("k", 100), ""), "msg")));
    print(hex(pbkdf2_sha256("password", "salt", 1, 32)));
    print(hex(pbkdf2_sha256("password", "salt", 4096, 50)));
    let raw = try unhex("fbff0068656c6c6f21");
    print(base64(raw), base64url(raw), base64("ab"), base64(""));
    print(unwrap(unbase64("+/8AaGVsbG8h")) == raw, unwrap(unbase64("-_8AaGVsbG8h")) == raw, unwrap(unbase64("YWI=")) == "ab", unwrap(unbase64("YWI")) == "ab");
    print(unbase64("YW=I"), unbase64("Y"), unhex("abc"), unhex("zz"));
    print(url_encode("a b&c=d/é~"), unwrap(url_decode("a+b%26c%3Dd%2F%C3%A9~")), url_decode("%4"), url_decode("%zz"));
    print(constant_time_eq("secret", "secret"), constant_time_eq("secret", "secreT"), constant_time_eq("a", "ab"));
    let r1 = random_bytes(32);
    let r2 = random_bytes(32);
    print(len(r1), r1 != r2, len(random_bytes(0)));
    var lo = 100;
    var hi = 0;
    for i in 0..2000 {
        let v = random_int(1, 6);
        lo = min(lo, v);
        hi = max(hi, v);
    }
    print(lo, hi, random_int(7, 7), random_int(-9223372036854775807 - 1, 9223372036854775807) == 0 || true);
    return ok();
}`
	want := "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad\n" +
		"cdc76e5c9914fb9281a1c7e284d73e67f1809a48a497200e046d39ccc7112cd0\n" +
		"75220b47218278e656f2013bb8f0c455a25eaf01e86c64924e9d48d89776d6f2 7ce100971f64e7001e8fe5a51973ecdfe1ced42befe7ee8d5fd6219506b5393c\n" +
		"d5e285683cd4efc02d021a5c62014694958901005d6f71e89e0989fac77e4072 04c26261370ee7541549d16dee320c723e3fd14671e66a099afe0a377c16888e\n" +
		"5bdcc146bf60754e6a042426089575c75a003f089d2739839dec58b964ec3843\n" +
		"bd56a1782c2830e8abc6ed866a57a1230661e650b84c62f7ee3accc5fa5af491\n" +
		"120fb6cffcf8b32c43e7225256c4f837a86548c92ccc35480805987cb70be17b\n" +
		"c5e478d59288c841aa530db6845c4c8d962893a001ce4e11a4963873aa98134af7ad98c1b458ce3fd74ca35beba3cda7b8d1\n" +
		"+/8AaGVsbG8h -_8AaGVsbG8h YWI= \n" +
		"true true true true\n" +
		"err(\"unbase64: invalid character at byte 2\") err(\"unbase64: invalid length\") err(\"unhex: odd length 3\") err(\"unhex: invalid hex digit at byte 0\")\n" +
		"a%20b%26c%3Dd%2F%C3%A9~ a b&c=d/é~ err(\"url_decode: invalid escape at byte 0\") err(\"url_decode: invalid escape at byte 0\")\n" +
		"true false false\n" +
		"32 true 0\n" +
		"1 6 7 true\n"
	for _, sanitize := range []bool{false, true} {
		if sanitize {
			requireSanitizers(t)
		}
		r := runWith(t, src, "", sanitize)
		if r.code != 0 || r.stdout != want || r.stderr != "" {
			t.Errorf("sanitize %v: exit %d, stderr %q\ngot:\n%s\nwant:\n%s", sanitize, r.code, r.stderr, r.stdout, want)
		}
	}
	bad := runWith(t, "fn main() { print(random_int(5, 1)); }", "", false)
	if bad.code != 101 || !strings.Contains(bad.stderr, "random_int: low 5 is greater than high 1") {
		t.Errorf("random_int range: %d %q", bad.code, bad.stderr)
	}
}

func TestJSON(t *testing.T) {
	requireCC(t)
	src := `fn main() -> ! {
    let doc = "{\"user\": {\"name\": \"N\\u00e9 \\\"q\\\" \\ud83d\\ude00\", \"age\": 41, \"admin\": false, \"tags\": [\"a\", \"b\", {\"deep\": [1.5e2, -0, null]}]},\n \"a.b\": 1, \"big\": 9223372036854775808, \"dup\": 1, \"dup\": 2, \"\": \"empty\"}";
    print(json_valid(doc), json_valid("{\"a\":}"), json_valid("[1,]"), json_valid("01"), json_valid("\"\\x\""), json_valid(" [ ] "));
    let name = try json_string(doc, "user.name");
    print(name, len(name));
    print(json_int(doc, "user.age"), json_bool(doc, "user.admin"), json_float(doc, "user.tags.2.deep.0"));
    print(json_len(doc, "user.tags"), json_len(doc, "user"), json_keys(doc, "user"), json_keys(doc, ""));
    print(json_type(doc, "user.tags.2.deep.2"), json_type(doc, "user.tags.2.deep.1"), json_type(doc, "user.tags"), json_type(doc, "nope"), json_type("{bad", ""));
    print(json_raw(doc, "user.tags.2"), json_member(doc, "a.b"), json_int(doc, "dup"), json_string(doc, "."));
    print(json_int(doc, "big"), json_int(doc, "user.tags.2.deep.0"), json_string(doc, "user.age"));
    print(json_int(doc, "user.nope"), json_int(doc, "user.tags.9"), json_int(doc, "user.tags.x"), json_int(doc, "user.age.x"));
    print(json_int("{\"a\": [1, 2", "a"));
    print(json_quote("he said \"hi\"\n\t\\ é \u0001"));
    let built = "{\"msg\": " + json_quote("a\"b") + "}";
    print(json_string(built, "msg"));
    var nested = "1";
    for i in 0..600 { nested = "[" + nested + "]"; }
    print(json_valid(nested));
    return ok();
}`
	src = strings.Replace(src, `\u0001`, `" + chr(1) + "`, 1)
	want := "true false false false false true\n" +
		"Né \"q\" 😀 12\n" +
		"ok(41) ok(false) ok(150.0)\n" +
		"ok(3) ok(4) ok([\"name\", \"age\", \"admin\", \"tags\"]) ok([\"user\", \"a.b\", \"big\", \"dup\", \"dup\", \"\"])\n" +
		"null number array  \n" +
		"ok(\"{\\\"deep\\\": [1.5e2, -0, null]}\") ok(\"1\") ok(1) err(\"json: cannot look up \\\"\\\" in a value that is not an object or array\")\n" +
		"err(\"json: \\\"big\\\" does not fit in an int\") err(\"json: \\\"user.tags.2.deep.0\\\" is not an integer\") err(\"json: \\\"user.age\\\" is number, not a string\")\n" +
		"err(\"json: no key \\\"nope\\\" at \\\"user\\\"\") err(\"json: index 9 out of range (length 3)\") err(\"json: \\\"x\\\" is not an array index\") err(\"json: cannot look up \\\"x\\\" in a value that is not an object or array\")\n" +
		"err(\"json: invalid document at byte 11\")\n" +
		"\"he said \\\"hi\\\"\\n\\t\\\\ é \\u0001\"\n" +
		"ok(\"a\\\"b\")\n" +
		"false\n"
	for _, sanitize := range []bool{false, true} {
		if sanitize {
			requireSanitizers(t)
		}
		r := runWith(t, src, "", sanitize)
		if r.code != 0 || r.stdout != want || r.stderr != "" {
			t.Errorf("sanitize %v: exit %d, stderr %q\ngot:\n%s\nwant:\n%s", sanitize, r.code, r.stderr, r.stdout, want)
		}
	}
}

func TestRuntimeParserFuzz(t *testing.T) {
	requireSanitizers(t)
	dir := writeFiles(t, map[string]string{"main.ks": "fn main() { print(json_valid(\"{}\")); }"})
	if err := Build(filepath.Join(dir, "main.ks"), BuildOptions{Output: filepath.Join(dir, "prog"), EmitC: filepath.Join(dir, "rt.c")}); err != nil {
		t.Fatal(err)
	}
	harness, err := os.ReadFile(filepath.Join("testdata", "fuzz_runtime.c"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "fuzz.c"), harness, 0o644); err != nil {
		t.Fatal(err)
	}
	cc, err := FindCC("")
	if err != nil {
		t.Fatal(err)
	}
	args := append(append([]string{}, cc[1:]...), "-std=c11", "-O1", "-g", "-fsanitize=address,undefined", "-fno-sanitize-recover=undefined", "-o", "fuzz", "fuzz.c", "-lm")
	build := exec.Command(cc[0], args...)
	build.Dir = dir
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build harness: %v\n%s", err, out)
	}
	iters := "30000"
	if v := os.Getenv("KESTREL_FUZZ_ITERS"); v != "" {
		iters = v
	}
	run := exec.Command(filepath.Join(dir, "fuzz"), iters)
	run.Env = append(os.Environ(), sanitizerEnv()...)
	out, err := run.CombinedOutput()
	if err != nil || strings.TrimSpace(string(out)) != "done" {
		t.Fatalf("fuzz: %v\n%s", err, out)
	}
}
