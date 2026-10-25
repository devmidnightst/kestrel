package compiler

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

type BuildOptions struct {
	Output string
	EmitC  string
	CC     string
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
	return CompileC(src, opts.Output, opts.CC)
}

func CompileC(src, output, ccOverride string) error {
	cc, err := FindCC(ccOverride)
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
	out, err := filepath.Abs(output)
	if err != nil {
		return err
	}
	args := append(cc[1:],
		"-std=c11", "-O2",
		"-Werror=implicit-function-declaration",
		"-Werror=int-conversion",
		"-Werror=incompatible-pointer-types",
		"-o", out, cfile, "-lm")
	cmd := exec.Command(cc[0], args...)
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("c compiler %s failed: %v\n%s", cc[0], err, strings.TrimSpace(buf.String()))
	}
	return nil
}
