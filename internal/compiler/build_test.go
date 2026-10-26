package compiler

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
)

func requireCC(t *testing.T) {
	t.Helper()
	if _, err := FindCC(""); err != nil {
		t.Skipf("no c compiler available: %v", err)
	}
}

func buildAndRun(t *testing.T, entry string) (string, string, int) {
	t.Helper()
	return buildAndRunOpts(t, entry, BuildOptions{})
}

func buildAndRunOpts(t *testing.T, entry string, opts BuildOptions, args ...string) (string, string, int) {
	t.Helper()
	exe := filepath.Join(t.TempDir(), "prog")
	if runtime.GOOS == "windows" {
		exe += ".exe"
	}
	opts.Output = exe
	if err := Build(entry, opts); err != nil {
		t.Fatalf("build %s: %v", entry, err)
	}
	var stdout, stderr bytes.Buffer
	cmd := exec.Command(exe, args...)
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	cmd.Env = append(os.Environ(), sanitizerEnv()...)
	code := 0
	if err := cmd.Run(); err != nil {
		var exit *exec.ExitError
		if !errors.As(err, &exit) {
			t.Fatalf("run %s: %v", exe, err)
		}
		code = exit.ExitCode()
	}
	return stdout.String(), stderr.String(), code
}

var (
	sanitizerOnce sync.Once
	sanitizerErr  error
)

func requireSanitizers(t *testing.T) {
	t.Helper()
	requireCC(t)
	sanitizerOnce.Do(func() {
		dir, err := os.MkdirTemp("", "kestrel-san-")
		if err != nil {
			sanitizerErr = err
			return
		}
		defer os.RemoveAll(dir)
		src := filepath.Join(dir, "main.ks")
		if err := os.WriteFile(src, []byte("fn main() { print(\"ok\"); }"), 0o644); err != nil {
			sanitizerErr = err
			return
		}
		exe := filepath.Join(dir, "prog")
		if err := Build(src, BuildOptions{Output: exe, Sanitize: true}); err != nil {
			sanitizerErr = err
			return
		}
		cmd := exec.Command(exe)
		cmd.Env = append(os.Environ(), sanitizerEnv()...)
		if out, err := cmd.CombinedOutput(); err != nil || string(out) != "ok\n" {
			sanitizerErr = fmt.Errorf("sanitized program failed: %v %q", err, out)
		}
	})
	if sanitizerErr != nil {
		t.Skipf("sanitizers unavailable: %v", sanitizerErr)
	}
}

func runSource(t *testing.T, src string) (string, string, int) {
	t.Helper()
	dir := writeFiles(t, map[string]string{"main.ks": src})
	return buildAndRun(t, filepath.Join(dir, "main.ks"))
}

func TestNativePrograms(t *testing.T) {
	requireCC(t)
	cases := []struct {
		name string
		src  string
		want string
	}{
		{"arithmetic", "fn main() { print(1 + 2 * 3, (1 + 2) * 3, 7 / 2, -7 / 2, 7 % 3, -7 % 3, 10 - 20); }", "7 9 3 -3 1 -1 -10\n"},
		{"int64 range", "fn main() { let max = 9223372036854775807; print(max, -max - 1); }", "9223372036854775807 -9223372036854775808\n"},
		{"booleans", "fn main() { print(true && false, true || false, !true, 1 < 2, 2 <= 2, 3 > 4, 4 >= 5, 1 == 1, 1 != 1); }", "false true false true true false false true false\n"},
		{"short circuit", "fn boom() -> bool { print(\"evaluated\"); return true; }\nfn main() { print(false && boom()); print(true || boom()); }", "false\ntrue\n"},
		{"strings", "fn main() { let s = \"tab\\there \\\"q\\\" back\\\\slash\"; print(s); print(len(\"héllo\"), \"a\" + \"b\" == \"ab\", \"x\" != \"x\"); print(\"n=\" + str(-42)); }", "tab\there \"q\" back\\slash\n6 true false\nn=-42\n"},
		{"trigraph safe", "fn main() { print(\"??=??/\"); }", "??=??/\n"},
		{"empty print", "fn main() { print(); print(\"\"); }", "\n\n"},
		{"scoping", "fn main() { let x = 1; { let x = 2; print(x); { let x = x + 10; print(x); } } print(x); var y = 0; { y = 5; } print(y); }", "2\n12\n1\n5\n"},
		{"loops", "fn main() { var i = 0; var s = 0; while true { i = i + 1; if i > 10 { break; } if i % 2 == 0 { continue; } s = s + i; } print(s); }", "25\n"},
		{"recursion", "fn fib(n: int) -> int { if n < 2 { return n; } return fib(n - 1) + fib(n - 2); }\nfn main() { print(fib(20)); }", "6765\n"},
		{"else if chain", "fn sign(n: int) -> int { if n < 0 { return -1; } else if n == 0 { return 0; } else { return 1; } }\nfn main() { print(sign(-9), sign(0), sign(9)); }", "-1 0 1\n"},
		{"void return", "fn early(n: int) { if n > 0 { print(\"pos\"); return; } print(\"nonpos\"); }\nfn main() { early(1); early(0); }", "pos\nnonpos\n"},
		{"reserved c names", "fn int64_t(printf: int) -> int { let main = printf; return main; }\nfn main() { let exit = int64_t(3); print(exit); }", "3\n"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			out, errOut, code := runSource(t, c.src)
			if code != 0 || out != c.want {
				t.Errorf("exit %d, stdout %q, stderr %q; want stdout %q", code, out, errOut, c.want)
			}
		})
	}
}

func TestMainExitCode(t *testing.T) {
	requireCC(t)
	_, _, code := runSource(t, "fn main() -> int { print(\"bye\"); return 7; }")
	if code != 7 {
		t.Errorf("exit code %d, want 7", code)
	}
}

func TestRuntimeErrors(t *testing.T) {
	requireCC(t)
	cases := map[string]string{
		"fn main() { let z = 0; print(1 / z); }":                         "division by zero",
		"fn main() { let z = 0; print(1 % z); }":                         "division by zero",
		"fn main() { print(9223372036854775807 + 1); }":                  "integer overflow",
		"fn main() { let m = -9223372036854775807 - 1; print(m - 1); }":  "integer overflow",
		"fn main() { print(4611686018427387904 * 2); }":                  "integer overflow",
		"fn main() { let m = -9223372036854775807 - 1; print(-m); }":     "integer overflow",
		"fn main() { let m = -9223372036854775807 - 1; print(m / -1); }": "integer overflow",
	}
	for src, want := range cases {
		out, errOut, code := runSource(t, src)
		if code != 101 || !strings.Contains(errOut, "kestrel: runtime error: "+want) || out != "" {
			t.Errorf("%s: exit %d, stdout %q, stderr %q; want runtime error %q", src, code, out, errOut, want)
		}
	}
	out, _, code := runSource(t, "fn main() { let m = -9223372036854775807 - 1; print(m % -1); }")
	if code != 0 || out != "0\n" {
		t.Errorf("INT64_MIN %% -1: exit %d, out %q", code, out)
	}
	out, _, code = runSource(t, "fn main() { print(\"before\"); let z = 0; print(1 / z); }")
	if code != 101 || out != "before\n" {
		t.Errorf("stdout should be flushed before a runtime error: exit %d, out %q", code, out)
	}
}

func TestCHeaderImport(t *testing.T) {
	requireCC(t)
	out, errOut, code := runSource(t, "import c \"stdlib.h\" {\n  fn llabs(n: int) -> int;\n}\nfn main() { print(llabs(-5), llabs(6)); }")
	if code != 0 || out != "5 6\n" {
		t.Errorf("exit %d, out %q, stderr %q", code, out, errOut)
	}
	dir := writeFiles(t, map[string]string{"main.ks": "import c \"stdlib.h\" {\n  fn no_such_function_here(n: int) -> int;\n}\nfn main() { print(no_such_function_here(1)); }"})
	err := Build(filepath.Join(dir, "main.ks"), BuildOptions{Output: filepath.Join(dir, "prog")})
	if err == nil || !strings.Contains(err.Error(), "c compiler") {
		t.Errorf("expected c compiler failure for undeclared c function, got %v", err)
	}
	dir = writeFiles(t, map[string]string{"main.ks": "import c \"stdlib.h\" {\n  fn getenv(n: int) -> int;\n}\nfn main() { print(getenv(1)); }"})
	err = Build(filepath.Join(dir, "main.ks"), BuildOptions{Output: filepath.Join(dir, "prog")})
	if err == nil || !strings.Contains(err.Error(), "c compiler") {
		t.Errorf("expected c compiler failure for pointer-typed c function, got %v", err)
	}
}

func TestEmitC(t *testing.T) {
	requireCC(t)
	dir := writeFiles(t, map[string]string{"main.ks": "fn main() { print(1); }"})
	cfile := filepath.Join(dir, "out.c")
	if err := Build(filepath.Join(dir, "main.ks"), BuildOptions{Output: filepath.Join(dir, "prog"), EmitC: cfile}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(cfile)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "int main(int argc, char **argv)") {
		t.Errorf("emitted c does not contain main")
	}
}

func TestExamples(t *testing.T) {
	requireCC(t)
	examples := map[string]string{
		"variables.ks":    "name: kestrel\nyear: 2026 count: 10\nready: true\nlength of name: 7\njoined: kestrel 2026\ninner: shadowed\nouter: kestrel\nprecedence: 14 20 2 -3\n",
		"functions.ks":    "square(12) = 144\nhello, world!\nfactorial(20) = 2432902008176640000\nis_even(10): true is_even(7): false\n",
		"branching.ks":    "negative zero small large\n1\n2\nfizz\n4\nbuzz\nfizz\n7\n8\nfizz\nbuzz\n11\nfizz\n13\n14\nfizzbuzz\na is at most b\nstrings compare by value\n",
		"loops.ks":        "sum 1..100 = 5050\nfirst ten primes: 2 3 5 7 11 13 17 19 23 29 \nfib(10) = 55\n",
		"modules/main.ks": "gcd(84, 36) = 12\npow(3, 4) = 81\n== modules ==\n",
		"cheader.ks":      "llabs(-42) = 42\n",
		"arrays.ks":       "original: [42, 7, 19, 3, 88, 1]\nsorted: [1, 3, 7, 19, 42, 88]\npopped c left [\"a\", \"b\"]\nidentity: [[1, 0, 0], [0, 1, 0], [0, 0, 1]]\nmiddle: [3, 7, 19, 42]\n",
		"structs.ks":      "Item { name: \"rope\", price: 4.5, qty: 10 }\ntoday: 57.25 yesterday: 25.75\nInventory { owner: \"kes\", items: [Item { name: \"rope\", price: 4.5, qty: 3 }, Item { name: \"lamp\", price: 12.25, qty: 1 }] }\n",
		"wordfreq.ks":     "lines: 0\n",
	}
	for name, want := range examples {
		t.Run(name, func(t *testing.T) {
			out, errOut, code := buildAndRun(t, filepath.Join("..", "..", "examples", filepath.FromSlash(name)))
			if code != 0 || out != want {
				t.Errorf("exit %d, stderr %q\ngot:\n%s\nwant:\n%s", code, errOut, out, want)
			}
		})
	}
}

func TestDeterministicOutput(t *testing.T) {
	dir := writeFiles(t, map[string]string{
		"main.ks":  "import \"b.ks\";\nimport \"a.ks\";\nimport c \"stdlib.h\" { fn llabs(n: int) -> int; }\nimport c \"stdio.h\" { fn getchar() -> int; }\nfn main() { a.f(); b.g(); print(llabs(-1)); }",
		"a.ks":     "import c \"stdlib.h\" { fn llabs(n: int) -> int; }\npub fn f() { print(llabs(-2)); }",
		"b.ks":     "pub fn g() { let x = 1; { let x = 2; print(x); } }",
		"other.ks": "fn main() {}",
	})
	var first string
	for i := 0; i < 5; i++ {
		prog, err := Load(filepath.Join(dir, "main.ks"))
		if err != nil {
			t.Fatal(err)
		}
		src := GenerateC(prog)
		if i == 0 {
			first = src
		} else if src != first {
			t.Fatal("generated c differs between runs")
		}
	}
}

func TestMinIntNative(t *testing.T) {
	requireCC(t)
	out, errOut, code := runSource(t, "fn main() { let m = -9223372036854775808; print(m, m + 1, m == -9223372036854775807 - 1); }")
	if code != 0 || out != "-9223372036854775808 -9223372036854775807 true\n" {
		t.Errorf("exit %d, out %q, stderr %q", code, out, errOut)
	}
}

func TestFailedBuildKeepsExistingOutput(t *testing.T) {
	requireCC(t)
	dir := writeFiles(t, map[string]string{
		"main.ks": "import c \"stdlib.h\" { fn no_such_function_here() -> int; }\nfn main() { print(no_such_function_here()); }",
		"prog":    "previous",
	})
	if err := Build(filepath.Join(dir, "main.ks"), BuildOptions{Output: filepath.Join(dir, "prog")}); err == nil {
		t.Fatal("expected build failure")
	}
	data, err := os.ReadFile(filepath.Join(dir, "prog"))
	if err != nil || string(data) != "previous" {
		t.Errorf("existing output was clobbered: %q, %v", data, err)
	}
}

func sanitizerEnv() []string {
	env := []string{"UBSAN_OPTIONS=print_stacktrace=1"}
	if runtime.GOOS == "linux" {
		env = append(env, "ASAN_OPTIONS=detect_leaks=1")
	}
	return env
}
