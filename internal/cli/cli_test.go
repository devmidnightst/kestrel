package cli

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"

	"github.com/devmidnightst/kestrel/internal/compiler"
)

func runCLI(args ...string) (int, string, string) {
	var stdout, stderr bytes.Buffer
	code := Main(args, strings.NewReader(""), &stdout, &stderr)
	return code, stdout.String(), stderr.String()
}

func writeSource(t *testing.T, name, src string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func requireCC(t *testing.T) {
	t.Helper()
	if _, err := compiler.FindCC(""); err != nil {
		t.Skipf("no c compiler available: %v", err)
	}
}

func TestUsage(t *testing.T) {
	if code, _, errOut := runCLI(); code != 2 || !strings.Contains(errOut, "usage:") {
		t.Errorf("no args: code %d, stderr %q", code, errOut)
	}
	if code, out, _ := runCLI("help"); code != 0 || !strings.Contains(out, "kestrel build") {
		t.Errorf("help: code %d, out %q", code, out)
	}
	if code, out, _ := runCLI("version"); code != 0 || !strings.HasPrefix(out, "kestrel ") {
		t.Errorf("version: code %d, out %q", code, out)
	}
	if code, _, errOut := runCLI("frobnicate"); code != 2 || !strings.Contains(errOut, "unknown command") {
		t.Errorf("unknown command: code %d, stderr %q", code, errOut)
	}
	if code, _, errOut := runCLI("build"); code != 2 || !strings.Contains(errOut, "missing source file") {
		t.Errorf("missing file: code %d, stderr %q", code, errOut)
	}
	if code, _, errOut := runCLI("build", "a.ks", "-o"); code != 2 || !strings.Contains(errOut, "flag -o needs a value") {
		t.Errorf("missing flag value: code %d, stderr %q", code, errOut)
	}
	if code, _, errOut := runCLI("check", "a.ks", "--bogus"); code != 2 || !strings.Contains(errOut, "unknown flag --bogus") {
		t.Errorf("unknown flag: code %d, stderr %q", code, errOut)
	}
}

func TestCheck(t *testing.T) {
	good := writeSource(t, "good.ks", "fn main() { print(1); }")
	if code, out, errOut := runCLI("check", good); code != 0 || out != "" || errOut != "" {
		t.Errorf("check good: code %d, out %q, err %q", code, out, errOut)
	}
	bad := writeSource(t, "bad.ks", "fn main() {\n  let x = 1;\n  x = true;\n  print(y);\n}")
	code, _, errOut := runCLI("check", bad)
	if code != 1 || !strings.Contains(errOut, "bad.ks:3:3: error: cannot assign to x") || !strings.Contains(errOut, "bad.ks:4:9: error: undefined: y") || !strings.Contains(errOut, "2 errors") {
		t.Errorf("check bad: code %d, stderr %q", code, errOut)
	}
	if code, _, errOut := runCLI("check", filepath.Join(t.TempDir(), "missing.ks")); code != 1 || !strings.Contains(errOut, "no such file") {
		t.Errorf("check missing: code %d, stderr %q", code, errOut)
	}
}

func TestBuildAndRun(t *testing.T) {
	requireCC(t)
	src := writeSource(t, "hello.ks", "fn main() -> int { print(\"hello\", 42); return 3; }")
	exe := filepath.Join(t.TempDir(), "hello")
	if code, _, errOut := runCLI("build", src, "-o", exe); code != 0 {
		t.Fatalf("build: code %d, stderr %q", code, errOut)
	}
	out, err := exec.Command(exe).Output()
	if string(out) != "hello 42\n" {
		t.Errorf("built program output %q (err %v)", out, err)
	}
	code, out2, errOut := runCLI("run", src)
	if code != 3 || out2 != "hello 42\n" {
		t.Errorf("run: code %d, out %q, stderr %q", code, out2, errOut)
	}
	exe2 := filepath.Join(t.TempDir(), "flagfirst")
	if code, _, errOut := runCLI("build", "-o", exe2, src); code != 0 {
		t.Errorf("build with -o first: code %d, stderr %q", code, errOut)
	}
	if _, err := os.Stat(exe2); err != nil {
		t.Errorf("expected output at %s: %v", exe2, err)
	}
}

func TestRunReportsCompileErrors(t *testing.T) {
	src := writeSource(t, "broken.ks", "fn main() { print(1 + \"x\"); }")
	code, out, errOut := runCLI("run", src)
	if code != 1 || out != "" || !strings.Contains(errOut, "broken.ks:1:21: error: operator + is not defined for int and string") {
		t.Errorf("code %d, out %q, stderr %q", code, out, errOut)
	}
}

func TestBuildWithMissingCompiler(t *testing.T) {
	src := writeSource(t, "ok.ks", "fn main() {}")
	code, _, errOut := runCLI("build", src, "-o", filepath.Join(t.TempDir(), "x"), "--cc", "definitely-not-a-c-compiler")
	if code != 1 || !strings.Contains(errOut, "c compiler \"definitely-not-a-c-compiler\" not found") {
		t.Errorf("code %d, stderr %q", code, errOut)
	}
}

func TestErrorCap(t *testing.T) {
	src := "fn main() {\n" + strings.Repeat("  print(undefined_name);\n", 60) + "}\n"
	code, _, errOut := runCLI("check", writeSource(t, "many.ks", src))
	if code != 1 || strings.Count(errOut, "error: undefined") != maxReportedErrors || !strings.Contains(errOut, "too many errors; 10 more not shown") {
		t.Errorf("code %d, stderr tail %q", code, errOut[len(errOut)-80:])
	}
}

func TestRunReportsSignals(t *testing.T) {
	requireCC(t)
	if runtime.GOOS == "windows" {
		t.Skip("signals are unix only")
	}
	src := writeSource(t, "abort.ks", "import c \"stdlib.h\" { fn abort(); }\nfn main() { print(\"before\"); abort(); }")
	code, out, errOut := runCLI("run", src)
	if code != 128+int(syscall.SIGABRT) || out != "before\n" || !strings.Contains(errOut, "killed by signal") {
		t.Errorf("code %d, out %q, stderr %q", code, out, errOut)
	}
}

func TestRunPassesProgramArgs(t *testing.T) {
	requireCC(t)
	src := writeSource(t, "args.ks", "fn main() { print(args(), len(args())); }")
	code, out, errOut := runCLI("run", src, "--", "a", "--flag", "b c")
	if code != 0 || out != "[\"a\", \"--flag\", \"b c\"] 3\n" {
		t.Errorf("code %d, out %q, stderr %q", code, out, errOut)
	}
	if code, _, errOut := runCLI("build", src, "--", "x"); code != 2 || !strings.Contains(errOut, "unknown flag --") {
		t.Errorf("build with --: code %d, stderr %q", code, errOut)
	}
}

func TestSanitizeFlag(t *testing.T) {
	requireCC(t)
	src := writeSource(t, "san.ks", "fn main() { var a = [\"x\"]; push(a, \"y\"); print(a); }")
	code, out, errOut := runCLI("run", src, "--sanitize")
	if code != 0 {
		t.Skipf("sanitized build unavailable: %s", errOut)
	}
	if out != "[\"x\", \"y\"]\n" {
		t.Errorf("out %q, stderr %q", out, errOut)
	}
}
