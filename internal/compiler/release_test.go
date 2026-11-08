package compiler

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func argHas(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

func argIndex(list []string, s string) int {
	for i, x := range list {
		if x == s {
			return i
		}
	}
	return -1
}

func TestCCArgs(t *testing.T) {
	cc := []string{"gcc", "-m64"}
	args, err := ccArgs(cc, BuildOptions{Release: true, CFlags: []string{"-DX=1"}, LDFlags: []string{"-lz"}}, "linux")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"-m64", "-O2", "-fstack-protector-strong", "-D_FORTIFY_SOURCE=2", "-fPIE", "-pie", "-Wl,-z,relro", "-Wl,-z,now", "-s", "-DX=1", "-lz"} {
		if !argHas(args, want) {
			t.Errorf("linux release args missing %s: %q", want, args)
		}
	}
	if argIndex(args, "-DX=1") > argIndex(args, "program.c") || argIndex(args, "-lz") < argIndex(args, "-lm") {
		t.Errorf("cflags must come before the source and ldflags after -lm: %q", args)
	}
	args, _ = ccArgs(cc, BuildOptions{Release: true}, "darwin")
	for _, bad := range []string{"-pie", "-Wl,-z,now", "-s", "-fPIE"} {
		if argHas(args, bad) {
			t.Errorf("darwin release args contain %s: %q", bad, args)
		}
	}
	args, _ = ccArgs(cc, BuildOptions{Release: true, Static: true}, "linux")
	if !argHas(args, "-static") || argHas(args, "-pie") || argHas(args, "-fPIE") {
		t.Errorf("static release args: %q", args)
	}
	args, _ = ccArgs(cc, BuildOptions{}, "linux")
	if argHas(args, "-s") || argHas(args, "-fstack-protector-strong") || !argHas(args, "-O2") {
		t.Errorf("default args: %q", args)
	}
	if argHas(args, "program.out") && args[argIndex(args, "program.out")-1] != "-o" {
		t.Errorf("output flag: %q", args)
	}
	if _, err := ccArgs(cc, BuildOptions{Sanitize: true, Release: true}, "linux"); err == nil {
		t.Error("sanitize with release should be rejected")
	}
}

func TestReleaseBuildRunsAndIsReproducible(t *testing.T) {
	requireCC(t)
	dir := writeFiles(t, map[string]string{"main.ks": `fn main() -> ! {
    let n = try try_parse_int("41");
    print(n + 1, fmt("{}", "release"));
    return ok();
}`})
	var bins [][]byte
	for i, opts := range []BuildOptions{{Release: true}, {Release: true}} {
		exe := filepath.Join(dir, "prog"+string(rune('a'+i)))
		opts.Output = exe
		if err := Build(filepath.Join(dir, "main.ks"), opts); err != nil {
			t.Fatal(err)
		}
		out, err := exec.Command(exe).CombinedOutput()
		if err != nil || string(out) != "42 release\n" {
			t.Fatalf("release binary: %v %q", err, out)
		}
		data, err := os.ReadFile(exe)
		if err != nil {
			t.Fatal(err)
		}
		bins = append(bins, data)
	}
	if !bytes.Equal(bins[0], bins[1]) {
		t.Error("two release builds of the same program differ")
	}
	if runtime.GOOS == "linux" {
		if _, err := exec.LookPath("readelf"); err == nil {
			out, err := exec.Command("readelf", "-lW", filepath.Join(dir, "proga")).CombinedOutput()
			if err != nil || !strings.Contains(string(out), "GNU_RELRO") {
				t.Errorf("release binary has no RELRO segment: %v\n%s", err, out)
			}
			out, _ = exec.Command("readelf", "-SW", filepath.Join(dir, "proga")).CombinedOutput()
			if strings.Contains(string(out), " .symtab ") {
				t.Error("release binary is not stripped")
			}
		}
	}
}

func TestStaticBuild(t *testing.T) {
	requireCC(t)
	if runtime.GOOS != "linux" {
		t.Skip("static linking is only supported on linux")
	}
	dir := writeFiles(t, map[string]string{"main.ks": "fn main() { print(\"static\", getenv(\"HOME\") != \"\" || true); }"})
	exe := filepath.Join(dir, "prog")
	if err := Build(filepath.Join(dir, "main.ks"), BuildOptions{Output: exe, Release: true, Static: true}); err != nil {
		t.Skipf("static libc not available: %v", err)
	}
	out, err := exec.Command(exe).CombinedOutput()
	if err != nil || string(out) != "static true\n" {
		t.Fatalf("static binary: %v %q", err, out)
	}
}

func TestSanitizeWithReleaseRejected(t *testing.T) {
	requireCC(t)
	dir := writeFiles(t, map[string]string{"main.ks": "fn main() {}"})
	err := Build(filepath.Join(dir, "main.ks"), BuildOptions{Output: filepath.Join(dir, "p"), Sanitize: true, Static: true})
	if err == nil || !strings.Contains(err.Error(), "cannot be combined") {
		t.Fatalf("got %v", err)
	}
}
