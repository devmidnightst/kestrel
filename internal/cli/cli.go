package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/devmidnightst/kestrel/internal/compiler"
)

const Version = "0.1.0"

const usage = `usage:
  kestrel build <file.ks> [-o output] [--emit-c file.c] [--cc compiler]
  kestrel run <file.ks> [--cc compiler]
  kestrel check <file.ks>
  kestrel version
  kestrel help
`

type options struct {
	file   string
	output string
	emitC  string
	cc     string
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
		}
		err := compiler.Build(opts.file, compiler.BuildOptions{Output: opts.output, EmitC: opts.emitC, CC: opts.cc})
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
	if err := compiler.Build(opts.file, compiler.BuildOptions{Output: exe, CC: opts.cc}); err != nil {
		reportError(stderr, err)
		return 1
	}
	cmd := exec.Command(exe)
	cmd.Stdin = stdin
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	if err := cmd.Run(); err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			if code := exit.ExitCode(); code >= 0 {
				return code
			}
			fmt.Fprintf(stderr, "kestrel: program terminated: %v\n", err)
			return 1
		}
		fmt.Fprintf(stderr, "kestrel: %v\n", err)
		return 1
	}
	return 0
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
		case a == "-o" && cmd == "build":
			o.output, err = value()
		case (a == "--emit-c" || a == "-emit-c") && cmd == "build":
			o.emitC, err = value()
		case (a == "--cc" || a == "-cc") && cmd != "check":
			o.cc, err = value()
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
		for _, e := range list {
			fmt.Fprintln(w, e.Error())
		}
		if len(list) > 1 {
			fmt.Fprintf(w, "%d errors\n", len(list))
		}
		return
	}
	fmt.Fprintln(w, err.Error())
}
