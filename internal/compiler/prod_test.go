package compiler

import (
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
