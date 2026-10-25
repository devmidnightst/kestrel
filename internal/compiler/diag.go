package compiler

import (
	"fmt"
	"sort"
	"strings"
)

type Pos struct {
	File string
	Line int
	Col  int
}

func (p Pos) String() string {
	return fmt.Sprintf("%s:%d:%d", p.File, p.Line, p.Col)
}

type Error struct {
	Pos Pos
	Msg string
}

func (e *Error) Error() string {
	return fmt.Sprintf("%s: error: %s", e.Pos, e.Msg)
}

type ErrorList []*Error

func (l ErrorList) Error() string {
	parts := make([]string, len(l))
	for i, e := range l {
		parts[i] = e.Error()
	}
	return strings.Join(parts, "\n")
}

func (l ErrorList) sorted() ErrorList {
	sort.SliceStable(l, func(i, j int) bool {
		a, b := l[i].Pos, l[j].Pos
		if a.File != b.File {
			return a.File < b.File
		}
		if a.Line != b.Line {
			return a.Line < b.Line
		}
		return a.Col < b.Col
	})
	return l
}

func errorf(pos Pos, format string, args ...any) *Error {
	return &Error{Pos: pos, Msg: fmt.Sprintf(format, args...)}
}
