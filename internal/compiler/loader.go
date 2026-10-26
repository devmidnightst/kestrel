package compiler

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

type loader struct {
	root     string
	rootDisp string
	modules  map[string]*Module
	visiting map[string]bool
	order    []*Module
	errs     ErrorList
}

var headerPattern = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_./-]*\.h$`)

func Load(entry string) (*Program, error) {
	abs, err := filepath.Abs(entry)
	if err != nil {
		return nil, err
	}
	real, err := filepath.EvalSymlinks(abs)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("%s: no such file", entry)
		}
		return nil, err
	}
	if !strings.HasSuffix(real, ".ks") {
		return nil, fmt.Errorf("%s: kestrel source files must end in .ks", entry)
	}
	l := &loader{
		root:     filepath.Dir(real),
		rootDisp: filepath.Dir(entry),
		modules:  map[string]*Module{},
		visiting: map[string]bool{},
	}
	main, err := l.load(real, nil)
	if err != nil {
		return nil, err
	}
	if len(l.errs) > 0 {
		return nil, l.errs.sorted()
	}
	return Check(l.order, main)
}

func (l *loader) display(real string) string {
	rel, err := filepath.Rel(l.root, real)
	if err != nil {
		return real
	}
	return filepath.Join(l.rootDisp, rel)
}

func (l *loader) load(real string, chain []string) (*Module, error) {
	if m, ok := l.modules[real]; ok {
		return m, nil
	}
	disp := l.display(real)
	src, err := os.ReadFile(real)
	if err != nil {
		return nil, fmt.Errorf("%s: %v", disp, err)
	}
	f, err := Parse(disp, src)
	if err != nil {
		return nil, err
	}
	l.visiting[real] = true
	m := &Module{Path: real, Display: disp, File: f, Imports: map[string]*Module{}}
	for _, ci := range f.CImports {
		if err := validateHeader(ci.Header); err != nil {
			l.errs = append(l.errs, errorf(ci.HdrPos, "%v", err))
			continue
		}
		m.Headers = append(m.Headers, ci.Header)
	}
	chain = append(chain, disp)
	for _, imp := range f.Imports {
		target, err := l.resolve(filepath.Dir(real), imp)
		if err != nil {
			l.errs = append(l.errs, err.(*Error))
			continue
		}
		alias := imp.Alias
		aliasPos := imp.AliasPos
		if alias == "" {
			alias = strings.TrimSuffix(filepath.Base(imp.Path), ".ks")
			aliasPos = imp.PathPos
			if !isIdentifier(alias) {
				l.errs = append(l.errs, errorf(imp.PathPos, "module name %q is not a valid identifier; add `as name`", alias))
				continue
			}
		}
		if _, dup := m.Imports[alias]; dup {
			l.errs = append(l.errs, errorf(aliasPos, "module name %s is imported more than once", alias))
			continue
		}
		if l.visiting[target] {
			cycle := append(append([]string{}, chain...), l.display(target))
			return nil, errorf(imp.PathPos, "import cycle: %s", strings.Join(cycle, " -> "))
		}
		dep, err := l.load(target, chain)
		if err != nil {
			return nil, err
		}
		m.Imports[alias] = dep
	}
	delete(l.visiting, real)
	m.ID = len(l.order)
	l.modules[real] = m
	l.order = append(l.order, m)
	return m, nil
}

func (l *loader) resolve(dir string, imp *Import) (string, error) {
	p := imp.Path
	switch {
	case p == "":
		return "", errorf(imp.PathPos, "empty import path")
	case !strings.HasSuffix(p, ".ks"):
		return "", errorf(imp.PathPos, "import path %q must name a .ks file", p)
	case strings.ContainsAny(p, "\\\x00"):
		return "", errorf(imp.PathPos, "import path %q contains an illegal character", p)
	case filepath.IsAbs(p) || strings.HasPrefix(p, "/"):
		return "", errorf(imp.PathPos, "import path %q must be relative", p)
	}
	target := filepath.Join(dir, filepath.FromSlash(p))
	real, err := filepath.EvalSymlinks(target)
	if err != nil {
		return "", errorf(imp.PathPos, "cannot find module %q", p)
	}
	rel, err := filepath.Rel(l.root, real)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", errorf(imp.PathPos, "import path %q escapes the project directory %s", p, l.rootDisp)
	}
	info, err := os.Stat(real)
	if err != nil || !info.Mode().IsRegular() {
		return "", errorf(imp.PathPos, "module %q is not a regular file", p)
	}
	return real, nil
}

func validateHeader(h string) error {
	if !headerPattern.MatchString(h) {
		return fmt.Errorf("invalid c header name %q (expected something like \"stdlib.h\")", h)
	}
	for _, seg := range strings.Split(h, "/") {
		if seg == ".." || seg == "." || seg == "" {
			return fmt.Errorf("invalid c header name %q (no empty, \".\" or \"..\" path segments)", h)
		}
	}
	return nil
}

func isIdentifier(s string) bool {
	if s == "" {
		return false
	}
	for i, r := range s {
		if !isLetter(r) && (i == 0 || !isDigit(r)) {
			return false
		}
	}
	_, kw := keywords[s]
	return !kw
}
