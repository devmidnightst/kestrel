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
