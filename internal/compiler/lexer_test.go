package compiler

import (
	"strings"
	"testing"
)

func TestLexTokensAndPositions(t *testing.T) {
	src := "fn main() {\n  let x: int = 42;\n  x == y != z <= >= && || -> // trailing\n  \"a\\n\\\"b\\\"\"\n}"
	toks, err := Lex("t.ks", []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	want := []struct {
		kind Kind
		text string
		line int
		col  int
	}{
		{KwFn, "fn", 1, 1},
		{IdentTok, "main", 1, 4},
		{LParen, "(", 1, 8},
		{RParen, ")", 1, 9},
		{LBrace, "{", 1, 11},
		{KwLet, "let", 2, 3},
		{IdentTok, "x", 2, 7},
		{Colon, ":", 2, 8},
		{IdentTok, "int", 2, 10},
		{Assign, "=", 2, 14},
		{IntTok, "42", 2, 16},
		{Semicolon, ";", 2, 18},
		{IdentTok, "x", 3, 3},
		{Eq, "==", 3, 5},
		{IdentTok, "y", 3, 8},
		{NotEq, "!=", 3, 10},
		{IdentTok, "z", 3, 13},
		{LessEq, "<=", 3, 15},
		{GreaterEq, ">=", 3, 18},
		{AndAnd, "&&", 3, 21},
		{OrOr, "||", 3, 24},
		{Arrow, "->", 3, 27},
		{StringTok, "a\n\"b\"", 4, 3},
		{RBrace, "}", 5, 1},
		{EOF, "", 5, 2},
	}
	if len(toks) != len(want) {
		t.Fatalf("got %d tokens, want %d: %v", len(toks), len(want), toks)
	}
	for i, w := range want {
		g := toks[i]
		if g.Kind != w.kind || g.Text != w.text || g.Pos.Line != w.line || g.Pos.Col != w.col || g.Pos.File != "t.ks" {
			t.Errorf("token %d: got %v %q at %d:%d, want %v %q at %d:%d", i, g.Kind, g.Text, g.Pos.Line, g.Pos.Col, w.kind, w.text, w.line, w.col)
		}
	}
}

func TestLexKeywords(t *testing.T) {
	src := "fn pub let var if else while return print true false import as break continue ifx"
	toks, err := Lex("k.ks", []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	want := []Kind{KwFn, KwPub, KwLet, KwVar, KwIf, KwElse, KwWhile, KwReturn, KwPrint, KwTrue, KwFalse, KwImport, KwAs, KwBreak, KwContinue, IdentTok, EOF}
	for i, k := range want {
		if toks[i].Kind != k {
			t.Errorf("token %d: got %v, want %v", i, toks[i].Kind, k)
		}
	}
}

func TestLexUnicodeColumns(t *testing.T) {
	toks, err := Lex("u.ks", []byte("\"héllo\" x"))
	if err != nil {
		t.Fatal(err)
	}
	if toks[1].Pos.Col != 9 {
		t.Errorf("identifier after non-ascii string at column %d, want 9", toks[1].Pos.Col)
	}
}

func TestLexErrors(t *testing.T) {
	cases := []struct {
		src  string
		want string
	}{
		{"let s = \"abc", "e.ks:1:9: error: unterminated string literal"},
		{"let s = \"a\nb\"", "e.ks:1:9: error: unterminated string literal"},
		{"let s = \"\\q\"", "e.ks:1:10: error: unknown escape sequence \\q"},
		{"let x = 1 # 2", "e.ks:1:11: error: unexpected character '#'"},
		{"\n  a & b", "e.ks:2:5: error: unexpected character '&' (did you mean \"&&\"?)"},
		{"let x = 12abc;", "e.ks:1:11: error: invalid character 'a' in number literal"},
		{"let x = 9223372036854775809;", "e.ks:1:9: error: integer literal 9223372036854775809 does not fit in a signed 64-bit int"},
		{"\xff", "e.ks:1:1: error: source file is not valid utf-8"},
	}
	for _, c := range cases {
		_, err := Lex("e.ks", []byte(c.src))
		if err == nil {
			t.Errorf("%q: expected error", c.src)
			continue
		}
		if !strings.Contains(err.Error(), c.want) {
			t.Errorf("%q: got %q, want %q", c.src, err.Error(), c.want)
		}
	}
}
