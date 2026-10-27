package compiler

import "fmt"

type Kind int

const (
	EOF Kind = iota
	IdentTok
	IntTok
	StringTok
	FloatTok

	KwFn
	KwPub
	KwLet
	KwVar
	KwIf
	KwElse
	KwWhile
	KwFor
	KwIn
	KwReturn
	KwPrint
	KwTrue
	KwFalse
	KwImport
	KwAs
	KwBreak
	KwContinue
	KwStruct

	Plus
	Minus
	Star
	Slash
	Percent
	Assign
	Eq
	NotEq
	Less
	LessEq
	Greater
	GreaterEq
	AndAnd
	OrOr
	Bang
	LParen
	RParen
	LBrace
	RBrace
	LBracket
	RBracket
	Comma
	Semicolon
	Colon
	Arrow
	Dot
)

var kindText = map[Kind]string{
	EOF:        "end of file",
	IdentTok:   "identifier",
	IntTok:     "integer literal",
	FloatTok:   "float literal",
	StringTok:  "string literal",
	KwFn:       "fn",
	KwPub:      "pub",
	KwLet:      "let",
	KwVar:      "var",
	KwIf:       "if",
	KwElse:     "else",
	KwWhile:    "while",
	KwFor:      "for",
	KwIn:       "in",
	KwReturn:   "return",
	KwPrint:    "print",
	KwTrue:     "true",
	KwFalse:    "false",
	KwImport:   "import",
	KwAs:       "as",
	KwBreak:    "break",
	KwContinue: "continue",
	KwStruct:   "struct",
	Plus:       "+",
	Minus:      "-",
	Star:       "*",
	Slash:      "/",
	Percent:    "%",
	Assign:     "=",
	Eq:         "==",
	NotEq:      "!=",
	Less:       "<",
	LessEq:     "<=",
	Greater:    ">",
	GreaterEq:  ">=",
	AndAnd:     "&&",
	OrOr:       "||",
	Bang:       "!",
	LParen:     "(",
	RParen:     ")",
	LBrace:     "{",
	RBrace:     "}",
	LBracket:   "[",
	RBracket:   "]",
	Comma:      ",",
	Semicolon:  ";",
	Colon:      ":",
	Arrow:      "->",
	Dot:        ".",
}

var keywords = map[string]Kind{
	"fn":       KwFn,
	"pub":      KwPub,
	"let":      KwLet,
	"var":      KwVar,
	"if":       KwIf,
	"else":     KwElse,
	"while":    KwWhile,
	"for":      KwFor,
	"in":       KwIn,
	"return":   KwReturn,
	"print":    KwPrint,
	"true":     KwTrue,
	"false":    KwFalse,
	"import":   KwImport,
	"as":       KwAs,
	"break":    KwBreak,
	"continue": KwContinue,
	"struct":   KwStruct,
}

func (k Kind) String() string {
	if s, ok := kindText[k]; ok {
		return s
	}
	return fmt.Sprintf("Kind(%d)", int(k))
}

type Token struct {
	Kind Kind
	Text string
	Pos  Pos
}

func (t Token) describe() string {
	switch t.Kind {
	case IdentTok:
		return fmt.Sprintf("identifier %q", t.Text)
	case IntTok:
		return fmt.Sprintf("integer %s", t.Text)
	case FloatTok:
		return fmt.Sprintf("float %s", t.Text)
	case StringTok:
		return "string literal"
	case EOF:
		return "end of file"
	}
	return fmt.Sprintf("%q", t.Kind.String())
}
