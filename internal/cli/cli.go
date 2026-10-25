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

var Version = "0.2.0-dev"

const maxReportedErrors = 50

const usage = `usage:
  kestrel build <file.ks> [-o output] [--emit-c file.c] [--cc compiler] [--sanitize]
  kestrel run <file.ks> [--cc compiler] [--sanitize] [-- program args...]
  kestrel check <file.ks>
  kestrel version
  kestrel help
`

type options struct {
	file     string
	output   string
	emitC    string
	cc       string
	sanitize bool
	progArgs []string
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
		err := compiler.Build(opts.file, compiler.BuildOptions{Output: opts.output, EmitC: opts.emitC, CC: opts.cc, Sanitize: opts.sanitize})
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
	if err := compiler.Build(opts.file, compiler.BuildOptions{Output: exe, CC: opts.cc, Sanitize: opts.sanitize}); err != nil {
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
