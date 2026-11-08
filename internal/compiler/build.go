package compiler

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

type BuildOptions struct {
	Output   string
	EmitC    string
	CC       string
	Sanitize bool
	// Release adds hardening (stack protector, fortified libc calls, PIE and
	// full RELRO on Linux) and strips symbols. Use it for binaries you deploy.
	Release bool
	// Static links the executable statically. It works best with musl (for
	// example Alpine's gcc); glibc warns that getaddrinfo still needs shared
	// libraries at run time.
	Static bool
	// CFlags and LDFlags are extra arguments for the C compiler, placed
	// before and after the source file.
	CFlags  []string
	LDFlags []string
}

func FindCC(override string) ([]string, error) {
	if override == "" {
		override = os.Getenv("CC")
	}
	if override != "" {
		parts := strings.Fields(override)
		if _, err := exec.LookPath(parts[0]); err != nil {
			return nil, fmt.Errorf("c compiler %q not found", parts[0])
		}
		return parts, nil
	}
	for _, name := range []string{"cc", "gcc", "clang"} {
		if _, err := exec.LookPath(name); err == nil {
			return []string{name}, nil
		}
	}
	return nil, errors.New("no c compiler found (looked for cc, gcc and clang); install one or set CC")
}

func Build(entry string, opts BuildOptions) error {
	prog, err := Load(entry)
	if err != nil {
		return err
	}
	src := GenerateC(prog)
	if opts.EmitC != "" {
		if err := os.WriteFile(opts.EmitC, []byte(src), 0o644); err != nil {
			return err
		}
	}
	return CompileC(src, opts)
}

// ccArgs returns the arguments for the C compiler, which runs in the build
// directory on program.c so no temporary path ends up in the binary.
func ccArgs(cc []string, opts BuildOptions, goos string) ([]string, error) {
	if opts.Sanitize && (opts.Release || opts.Static) {
		return nil, errors.New("--sanitize cannot be combined with --release or --static")
	}
	args := append(append([]string{}, cc[1:]...), "-std=c11")
	switch {
	case opts.Sanitize:
		args = append(args, "-O1", "-g", "-fno-omit-frame-pointer", "-fsanitize=address,undefined", "-fno-sanitize-recover=undefined")
	case opts.Release:
		args = append(args, "-O2", "-fstack-protector-strong", "-U_FORTIFY_SOURCE", "-D_FORTIFY_SOURCE=2")
		if goos == "linux" && !opts.Static {
			args = append(args, "-fPIE")
		}
	default:
		args = append(args, "-O2")
	}
	args = append(args,
		"-Werror=implicit-function-declaration",
		"-Werror=int-conversion",
		"-Werror=incompatible-pointer-types")
	args = append(args, opts.CFlags...)
	args = append(args, "-o", "program.out", "program.c", "-lm")
	if opts.Release && goos == "linux" {
		if !opts.Static {
			args = append(args, "-pie")
		}
		args = append(args, "-Wl,-z,relro", "-Wl,-z,now", "-s")
	}
	if opts.Static {
		args = append(args, "-static")
	}
	args = append(args, opts.LDFlags...)
	return args, nil
}

func CompileC(src string, opts BuildOptions) error {
	cc, err := FindCC(opts.CC)
	if err != nil {
		return err
	}
	dir, err := os.MkdirTemp("", "kestrel-build-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	cfile := filepath.Join(dir, "program.c")
	if err := os.WriteFile(cfile, []byte(src), 0o644); err != nil {
		return err
	}
	out, err := filepath.Abs(opts.Output)
	if err != nil {
		return err
	}
	built := filepath.Join(dir, "program.out")
	args, err := ccArgs(cc, opts, runtime.GOOS)
	if err != nil {
		return err
	}
	cmd := exec.Command(cc[0], args...)
	cmd.Dir = dir
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("c compiler %s failed: %v\n%s", cc[0], err, strings.TrimSpace(buf.String()))
	}
	return install(built, out)
}

func install(src, dst string) error {
	if err := os.Rename(src, dst); err == nil {
		return nil
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	tmp, err := os.CreateTemp(filepath.Dir(dst), "."+filepath.Base(dst)+".tmp-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := io.Copy(tmp, in); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(0o755); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), dst)
}
