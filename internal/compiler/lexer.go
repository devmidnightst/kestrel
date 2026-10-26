package compiler

import (
	"math"
	"strconv"
	"strings"
	"unicode/utf8"
)

const MaxSourceSize = 8 << 20

type lexer struct {
	file string
	src  []rune
	off  int
	line int
	col  int
}

func Lex(file string, src []byte) ([]Token, error) {
	if len(src) > MaxSourceSize {
		return nil, errorf(Pos{File: file, Line: 1, Col: 1}, "source file is larger than %d bytes", MaxSourceSize)
	}
	if !utf8.Valid(src) {
		return nil, errorf(Pos{File: file, Line: 1, Col: 1}, "source file is not valid utf-8")
	}
	lx := &lexer{file: file, src: []rune(string(src)), line: 1, col: 1}
	var toks []Token
	for {
		tok, err := lx.next()
		if err != nil {
			return nil, err
		}
		toks = append(toks, tok)
		if tok.Kind == EOF {
			return toks, nil
		}
	}
}

func (lx *lexer) pos() Pos {
	return Pos{File: lx.file, Line: lx.line, Col: lx.col}
}

func (lx *lexer) peek(n int) rune {
	if lx.off+n < len(lx.src) {
		return lx.src[lx.off+n]
	}
	return 0
}

func (lx *lexer) atEnd() bool {
	return lx.off >= len(lx.src)
}

func (lx *lexer) advance() rune {
	r := lx.src[lx.off]
	lx.off++
	if r == '\n' {
		lx.line++
		lx.col = 1
	} else {
		lx.col++
	}
	return r
}

func (lx *lexer) skipSpace() {
	for !lx.atEnd() {
		r := lx.peek(0)
		switch {
		case r == ' ' || r == '\t' || r == '\r' || r == '\n':
			lx.advance()
		case r == '/' && lx.peek(1) == '/':
			for !lx.atEnd() && lx.peek(0) != '\n' {
				lx.advance()
			}
		default:
			return
		}
	}
}

func isLetter(r rune) bool {
	return r == '_' || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z')
}

func isDigit(r rune) bool {
	return r >= '0' && r <= '9'
}

var twoCharOps = map[string]Kind{
	"==": Eq,
	"!=": NotEq,
	"<=": LessEq,
	">=": GreaterEq,
	"&&": AndAnd,
	"||": OrOr,
	"->": Arrow,
}

var oneCharOps = map[rune]Kind{
	'+': Plus,
	'-': Minus,
	'*': Star,
	'/': Slash,
	'%': Percent,
	'=': Assign,
	'<': Less,
	'>': Greater,
	'!': Bang,
	'(': LParen,
	')': RParen,
	'{': LBrace,
	'}': RBrace,
	'[': LBracket,
	']': RBracket,
	',': Comma,
	';': Semicolon,
	':': Colon,
	'.': Dot,
}

func (lx *lexer) next() (Token, error) {
	lx.skipSpace()
	start := lx.pos()
	if lx.atEnd() {
		return Token{Kind: EOF, Pos: start}, nil
	}
	r := lx.peek(0)
	switch {
	case isLetter(r):
		begin := lx.off
		for !lx.atEnd() && (isLetter(lx.peek(0)) || isDigit(lx.peek(0))) {
			lx.advance()
		}
		text := string(lx.src[begin:lx.off])
		if kw, ok := keywords[text]; ok {
			return Token{Kind: kw, Text: text, Pos: start}, nil
		}
		return Token{Kind: IdentTok, Text: text, Pos: start}, nil
	case isDigit(r):
		return lx.lexNumber(start)
	case r == '"':
		return lx.lexString(start)
	}
	if lx.off+1 < len(lx.src) {
		if k, ok := twoCharOps[string(lx.src[lx.off:lx.off+2])]; ok {
			lx.advance()
			lx.advance()
			return Token{Kind: k, Text: k.String(), Pos: start}, nil
		}
	}
	if k, ok := oneCharOps[r]; ok {
		lx.advance()
		return Token{Kind: k, Text: k.String(), Pos: start}, nil
	}
	if r == '&' || r == '|' {
		return Token{}, errorf(start, "unexpected character %q (did you mean %q?)", r, string(r)+string(r))
	}
	return Token{}, errorf(start, "unexpected character %q", r)
}

func (lx *lexer) lexString(start Pos) (Token, error) {
	lx.advance()
	var sb strings.Builder
	for {
		if lx.atEnd() || lx.peek(0) == '\n' {
			return Token{}, errorf(start, "unterminated string literal")
		}
		p := lx.pos()
		r := lx.advance()
		switch {
		case r == '"':
			return Token{Kind: StringTok, Text: sb.String(), Pos: start}, nil
		case r == '\\':
			if lx.atEnd() {
				return Token{}, errorf(start, "unterminated string literal")
			}
			e := lx.advance()
			switch e {
			case 'n':
				sb.WriteRune('\n')
			case 't':
				sb.WriteRune('\t')
			case 'r':
				sb.WriteRune('\r')
			case '\\':
				sb.WriteRune('\\')
			case '"':
				sb.WriteRune('"')
			default:
				return Token{}, errorf(p, "unknown escape sequence \\%c", e)
			}
		case r == 0:
			return Token{}, errorf(p, "string literal may not contain a NUL character")
		case r < 0x20 && r != '\t':
			return Token{}, errorf(p, "string literal may not contain control character %q", r)
		default:
			sb.WriteRune(r)
		}
	}
}

func (lx *lexer) digits() int {
	n := 0
	for !lx.atEnd() && isDigit(lx.peek(0)) {
		lx.advance()
		n++
	}
	return n
}

func (lx *lexer) lexNumber(start Pos) (Token, error) {
	begin := lx.off
	lx.digits()
	isFloat := false
	if lx.peek(0) == '.' && isDigit(lx.peek(1)) {
		isFloat = true
		lx.advance()
		lx.digits()
	}
	if lx.peek(0) == 'e' || lx.peek(0) == 'E' {
		isFloat = true
		lx.advance()
		if lx.peek(0) == '+' || lx.peek(0) == '-' {
			lx.advance()
		}
		if lx.digits() == 0 {
			return Token{}, errorf(start, "invalid float literal %s: missing exponent digits", string(lx.src[begin:lx.off]))
		}
	}
	if !lx.atEnd() && isLetter(lx.peek(0)) {
		return Token{}, errorf(lx.pos(), "invalid character %q in number literal", lx.peek(0))
	}
	text := string(lx.src[begin:lx.off])
	if isFloat {
		v, err := strconv.ParseFloat(text, 64)
		if err != nil || math.IsInf(v, 0) {
			return Token{}, errorf(start, "float literal %s is out of range", text)
		}
		return Token{Kind: FloatTok, Text: text, Pos: start}, nil
	}
	if v, err := strconv.ParseUint(text, 10, 64); err != nil || v > 1<<63 {
		return Token{}, errorf(start, "integer literal %s does not fit in a signed 64-bit int", text)
	}
	return Token{Kind: IntTok, Text: text, Pos: start}, nil
}
