package compiler

import (
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
