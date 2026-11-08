package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"

	"github.com/devmidnightst/kestrel/internal/compiler"
)

var Version = "0.4.0-dev"

const maxReportedErrors = 50

const usage = `usage:
  kestrel build <file.ks> [-o output] [--emit-c file.c] [build flags]
  kestrel run <file.ks> [build flags] [-- program args...]
  kestrel check <file.ks>
  kestrel test [build flags] [--run name] [files or directories...]
  kestrel version
  kestrel help

build flags:
  --cc compiler     c compiler to use (default: $CC, then cc, gcc, clang)
  --release         hardened, stripped binary for deployment
  --static          link statically (best with musl, e.g. alpine)
  --sanitize        debug build with address and undefined behavior sanitizers
  --cflags "..."    extra c compiler flags (repeatable)
  --ldflags "..."   extra linker flags (repeatable)

kestrel test runs every fn test_name() in each *_test.ks file (the current
directory, searched recursively, when no paths are given). Each test runs in
its own process: a runtime error, failed assert or returned err fails only
that test. --run selects tests whose names contain the given text.
`

type options struct {
	file     string
	output   string
	emitC    string
	cc       string
	sanitize bool
	release  bool
	static   bool
	cflags   []string
	ldflags  []string
	progArgs []string
}

func (o options) build(output, emitC string) compiler.BuildOptions {
	return compiler.BuildOptions{
		Output: output, EmitC: emitC, CC: o.cc, Sanitize: o.sanitize,
		Release: o.release, Static: o.static, CFlags: o.cflags, LDFlags: o.ldflags,
	}
}

func Main(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, usage)
		return 2
	}
	cmd, rest := args[0], args[1:]
	switch cmd {
	case "help", "-h", "--help":
		fmt.Fprint(stdout, usage)
		return 0
	case "version", "--version":
		fmt.Fprintf(stdout, "kestrel %s\n", Version)
		return 0
	case "test":
		return runTests(rest, stdout, stderr)
	case "build", "run", "check":
	default:
		fmt.Fprintf(stderr, "kestrel: unknown command %q\n\n%s", cmd, usage)
		return 2
	}
	opts, err := parseOptions(cmd, rest)
	if err != nil {
		fmt.Fprintf(stderr, "kestrel %s: %v\n\n%s", cmd, err, usage)
		return 2
	}
	switch cmd {
	case "check":
		if _, err := compiler.Load(opts.file); err != nil {
			reportError(stderr, err)
			return 1
		}
		return 0
	case "build":
		if opts.output == "" {
			opts.output = strings.TrimSuffix(filepath.Base(opts.file), ".ks")
			if runtime.GOOS == "windows" {
				opts.output += ".exe"
			}
		}
		err := compiler.Build(opts.file, opts.build(opts.output, opts.emitC))
		if err != nil {
			reportError(stderr, err)
			return 1
		}
		return 0
	}
	return run(opts, stdin, stdout, stderr)
}

func run(opts options, stdin io.Reader, stdout, stderr io.Writer) int {
	dir, err := os.MkdirTemp("", "kestrel-run-")
	if err != nil {
		fmt.Fprintf(stderr, "kestrel: %v\n", err)
		return 1
	}
	defer os.RemoveAll(dir)
	exe := filepath.Join(dir, "program")
	if runtime.GOOS == "windows" {
		exe += ".exe"
	}
	if err := compiler.Build(opts.file, opts.build(exe, "")); err != nil {
		reportError(stderr, err)
		return 1
	}
	cmd := exec.Command(exe, opts.progArgs...)
	cmd.Stdin = stdin
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	interrupts := make(chan os.Signal, 1)
	signal.Notify(interrupts, os.Interrupt)
	defer signal.Stop(interrupts)
	err = cmd.Run()
	if err == nil {
		return 0
	}
	var exit *exec.ExitError
	if !errors.As(err, &exit) {
		fmt.Fprintf(stderr, "kestrel: %v\n", err)
		return 1
	}
	if status, ok := exit.Sys().(syscall.WaitStatus); ok && status.Signaled() {
		sig := status.Signal()
		if sig != syscall.SIGINT {
			fmt.Fprintf(stderr, "kestrel: program killed by signal: %v%s\n", sig, signalHint(sig))
		}
		return 128 + int(sig)
	}
	return exit.ExitCode()
}

func signalHint(sig syscall.Signal) string {
	if sig == syscall.SIGSEGV {
		return " (often a stack overflow from deep recursion)"
	}
	return ""
}

func parseOptions(cmd string, args []string) (options, error) {
	var o options
	for i := 0; i < len(args); i++ {
		a := args[i]
		value := func() (string, error) {
			if i+1 >= len(args) {
				return "", fmt.Errorf("flag %s needs a value", a)
			}
			i++
			return args[i], nil
		}
		var err error
		switch {
		case a == "--" && cmd == "run":
			o.progArgs = append([]string{}, args[i+1:]...)
			i = len(args)
		case a == "-o" && cmd == "build":
			o.output, err = value()
		case (a == "--emit-c" || a == "-emit-c") && cmd == "build":
			o.emitC, err = value()
		case (a == "--cc" || a == "-cc") && cmd != "check":
			o.cc, err = value()
		case a == "--sanitize" && cmd != "check":
			o.sanitize = true
		case a == "--release" && cmd != "check":
			o.release = true
		case a == "--static" && cmd != "check":
			o.static = true
		case (a == "--cflags" || a == "--ldflags") && cmd != "check":
			var v string
			if v, err = value(); err == nil {
				if a == "--cflags" {
					o.cflags = append(o.cflags, strings.Fields(v)...)
				} else {
					o.ldflags = append(o.ldflags, strings.Fields(v)...)
				}
			}
		case strings.HasPrefix(a, "-") && a != "-":
			return o, fmt.Errorf("unknown flag %s", a)
		default:
			if o.file != "" {
				return o, fmt.Errorf("unexpected argument %q (only one source file is accepted)", a)
			}
			o.file = a
		}
		if err != nil {
			return o, err
		}
	}
	if o.file == "" {
		return o, errors.New("missing source file")
	}
	if o.sanitize && (o.release || o.static) {
		return o, errors.New("--sanitize cannot be combined with --release or --static")
	}
	return o, nil
}

func reportError(w io.Writer, err error) {
	var list compiler.ErrorList
	if errors.As(err, &list) {
		shown := list
		if len(shown) > maxReportedErrors {
			shown = shown[:maxReportedErrors]
		}
		for _, e := range shown {
			fmt.Fprintln(w, e.Error())
		}
		if len(list) > len(shown) {
			fmt.Fprintf(w, "too many errors; %d more not shown\n", len(list)-len(shown))
		}
		if len(list) > 1 {
			fmt.Fprintf(w, "%d errors\n", len(list))
		}
		return
	}
	fmt.Fprintln(w, err.Error())
}

func runTests(args []string, stdout, stderr io.Writer) int {
	var o options
	var paths []string
	filter := ""
	for i := 0; i < len(args); i++ {
		a := args[i]
		next := func() (string, bool) {
			if i+1 >= len(args) {
				fmt.Fprintf(stderr, "kestrel test: flag %s needs a value\n\n%s", a, usage)
				return "", false
			}
			i++
			return args[i], true
		}
		switch {
		case a == "--run":
			v, ok := next()
			if !ok {
				return 2
			}
			filter = v
		case a == "--cc":
			v, ok := next()
			if !ok {
				return 2
			}
			o.cc = v
		case a == "--cflags" || a == "--ldflags":
			v, ok := next()
			if !ok {
				return 2
			}
			if a == "--cflags" {
				o.cflags = append(o.cflags, strings.Fields(v)...)
			} else {
				o.ldflags = append(o.ldflags, strings.Fields(v)...)
			}
		case a == "--sanitize":
			o.sanitize = true
		case a == "--release":
			o.release = true
		case strings.HasPrefix(a, "-") && a != "-":
			fmt.Fprintf(stderr, "kestrel test: unknown flag %s\n\n%s", a, usage)
			return 2
		default:
			paths = append(paths, a)
		}
	}
	if o.sanitize && o.release {
		fmt.Fprintf(stderr, "kestrel test: --sanitize cannot be combined with --release\n")
		return 2
	}
	if len(paths) == 0 {
		paths = []string{"."}
	}
	var files []string
	for _, p := range paths {
		info, err := os.Stat(p)
		if err != nil {
			fmt.Fprintf(stderr, "kestrel test: %v\n", err)
			return 1
		}
		if !info.IsDir() {
			files = append(files, p)
			continue
		}
		err = filepath.WalkDir(p, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() && path != p && strings.HasPrefix(d.Name(), ".") {
				return filepath.SkipDir
			}
			if !d.IsDir() && strings.HasSuffix(d.Name(), "_test.ks") {
				files = append(files, path)
			}
			return nil
		})
		if err != nil {
			fmt.Fprintf(stderr, "kestrel test: %v\n", err)
			return 1
		}
	}
	if len(files) == 0 {
		fmt.Fprintln(stdout, "no test files (*_test.ks)")
		return 0
	}
	dir, err := os.MkdirTemp("", "kestrel-test-")
	if err != nil {
		fmt.Fprintf(stderr, "kestrel: %v\n", err)
		return 1
	}
	defer os.RemoveAll(dir)
	failed := false
	for i, f := range files {
		fmt.Fprintf(stdout, "== %s\n", f)
		exe := filepath.Join(dir, fmt.Sprintf("test%d", i))
		n, err := compiler.BuildTests(f, o.build(exe, ""))
		if err != nil {
			reportError(stderr, err)
			failed = true
			continue
		}
		if n == 0 {
			fmt.Fprintln(stdout, "no test functions (fn test_name())")
			continue
		}
		cmd := exec.Command(exe)
		cmd.Dir = filepath.Dir(f)
		cmd.Stdout = stdout
		cmd.Stderr = stderr
		cmd.Env = os.Environ()
		if filter != "" {
			cmd.Env = append(cmd.Env, "KESTREL_TEST_RUN="+filter)
		}
		if err := cmd.Run(); err != nil {
			failed = true
		}
	}
	if failed {
		return 1
	}
	return 0
}
