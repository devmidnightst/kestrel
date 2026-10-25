package compiler

import (
	"math"
	"strconv"
)

const minIntMagnitude = "9223372036854775808"

const maxNesting = 256

type parser struct {
	toks  []Token
	pos   int
	depth int
}

func (p *parser) enter() {
	p.depth++
	if p.depth > maxNesting {
		p.fail(p.tok().Pos, "nesting too deep (more than %d levels)", maxNesting)
	}
}

func (p *parser) leave() {
	p.depth--
}

type parseBailout struct {
	err *Error
}

func Parse(file string, src []byte) (f *File, err error) {
	toks, err := Lex(file, src)
	if err != nil {
		return nil, err
	}
	p := &parser{toks: toks}
	defer func() {
		if r := recover(); r != nil {
			b, ok := r.(parseBailout)
			if !ok {
				panic(r)
			}
			f, err = nil, b.err
		}
	}()
	return p.parseFile(file), nil
}

func (p *parser) tok() Token {
	return p.toks[p.pos]
}

func (p *parser) peekKind(n int) Kind {
	if p.pos+n < len(p.toks) {
		return p.toks[p.pos+n].Kind
	}
	return EOF
}

func (p *parser) at(k Kind) bool {
	return p.tok().Kind == k
}

func (p *parser) next() Token {
	t := p.tok()
	if t.Kind != EOF {
		p.pos++
	}
	return t
}

func (p *parser) fail(pos Pos, format string, args ...any) {
	panic(parseBailout{err: errorf(pos, format, args...)})
}

func (p *parser) expect(k Kind, context string) Token {
	t := p.tok()
	if t.Kind != k {
		p.fail(t.Pos, "expected %q %s, found %s", k.String(), context, t.describe())
	}
	return p.next()
}

func (p *parser) expectIdent(context string) Token {
	t := p.tok()
	if t.Kind != IdentTok {
		p.fail(t.Pos, "expected identifier %s, found %s", context, t.describe())
	}
	return p.next()
}

func (p *parser) parseFile(path string) *File {
	f := &File{Path: path}
	for p.at(KwImport) {
		p.parseImport(f)
	}
	for !p.at(EOF) {
		switch p.tok().Kind {
		case KwFn, KwPub:
			f.Funcs = append(f.Funcs, p.parseFunc())
		case KwImport:
			p.fail(p.tok().Pos, "imports must appear before all function declarations")
		default:
			p.fail(p.tok().Pos, "expected function declaration, found %s", p.tok().describe())
		}
	}
	return f
}

func (p *parser) parseImport(f *File) {
	kw := p.next()
	if p.at(IdentTok) && p.tok().Text == "c" && p.peekKind(1) == StringTok {
		p.next()
		hdr := p.next()
		ci := &CImport{Pos: kw.Pos, Header: hdr.Text, HdrPos: hdr.Pos}
		p.expect(LBrace, "after c header name")
		for !p.at(RBrace) {
			if p.at(EOF) {
				p.fail(p.tok().Pos, "unterminated c import block")
			}
			fnTok := p.expect(KwFn, "in c import block")
			decl := p.parseSignature(fnTok.Pos, false)
			decl.Header = hdr.Text
			p.expect(Semicolon, "after c function declaration")
			ci.Externs = append(ci.Externs, decl)
		}
		p.next()
		f.CImports = append(f.CImports, ci)
		return
	}
	pathTok := p.tok()
	if pathTok.Kind != StringTok {
		p.fail(pathTok.Pos, "expected module path string after import, found %s", pathTok.describe())
	}
	p.next()
	imp := &Import{Pos: kw.Pos, Path: pathTok.Text, PathPos: pathTok.Pos}
	if p.at(KwAs) {
		p.next()
		alias := p.expectIdent("after as")
		imp.Alias = alias.Text
		imp.AliasPos = alias.Pos
	}
	p.expect(Semicolon, "after import")
	f.Imports = append(f.Imports, imp)
}

func (p *parser) parseFunc() *FuncDecl {
	start := p.tok().Pos
	pub := false
	if p.at(KwPub) {
		pub = true
		p.next()
	}
	p.expect(KwFn, "to begin function declaration")
	decl := p.parseSignature(start, pub)
	decl.Body = p.parseBlock()
	return decl
}

func (p *parser) parseSignature(start Pos, pub bool) *FuncDecl {
	name := p.expectIdent("for function name")
	decl := &FuncDecl{Pos: start, NamePos: name.Pos, Pub: pub, Name: name.Text}
	p.expect(LParen, "after function name")
	for !p.at(RParen) {
		pn := p.expectIdent("for parameter name")
		p.expect(Colon, "after parameter name")
		decl.Params = append(decl.Params, &Param{Pos: pn.Pos, Name: pn.Text, Type: p.parseType()})
		if !p.at(Comma) {
			break
		}
		p.next()
	}
	p.expect(RParen, "to close parameter list")
	if p.at(Arrow) {
		p.next()
		decl.Result = p.parseType()
	}
	return decl
}

func (p *parser) parseType() *TypeExpr {
	t := p.expectIdent("for type")
	return &TypeExpr{Pos: t.Pos, Name: t.Text}
}

func (p *parser) parseBlock() *Block {
	p.enter()
	defer p.leave()
	lb := p.expect(LBrace, "to open block")
	b := &Block{Pos: lb.Pos}
	for !p.at(RBrace) {
		if p.at(EOF) {
			p.fail(p.tok().Pos, "unexpected end of file, expected \"}\" to close block opened at %d:%d", lb.Pos.Line, lb.Pos.Col)
		}
		b.Stmts = append(b.Stmts, p.parseStmt())
	}
	b.End = p.next().Pos
	return b
}

func (p *parser) parseStmt() Stmt {
	t := p.tok()
	switch t.Kind {
	case KwLet, KwVar:
		p.next()
		name := p.expectIdent("after " + t.Kind.String())
		s := &LetStmt{Pos: t.Pos, Mutable: t.Kind == KwVar, Name: name.Text, NamePos: name.Pos}
		if p.at(Colon) {
			p.next()
			s.Type = p.parseType()
		}
		p.expect(Assign, "in variable declaration (variables must be initialized)")
		s.Value = p.parseExpr()
		p.expect(Semicolon, "after variable declaration")
		return s
	case KwIf:
		return p.parseIf()
	case KwWhile:
		p.next()
		cond := p.parseExpr()
		return &WhileStmt{Pos: t.Pos, Cond: cond, Body: p.parseBlock()}
	case KwReturn:
		p.next()
		s := &ReturnStmt{Pos: t.Pos}
		if !p.at(Semicolon) {
			s.Value = p.parseExpr()
		}
		p.expect(Semicolon, "after return")
		return s
	case KwPrint:
		p.next()
		p.expect(LParen, "after print")
		s := &PrintStmt{Pos: t.Pos, Args: p.parseArgs()}
		p.expect(Semicolon, "after print statement")
		return s
	case KwBreak:
		p.next()
		p.expect(Semicolon, "after break")
		return &BreakStmt{Pos: t.Pos}
	case KwContinue:
		p.next()
		p.expect(Semicolon, "after continue")
		return &ContinueStmt{Pos: t.Pos}
	case LBrace:
		return &BlockStmt{Block: p.parseBlock()}
	case IdentTok:
		if p.peekKind(1) == Assign {
			p.next()
			p.next()
			s := &AssignStmt{Pos: t.Pos, Name: t.Text, Value: p.parseExpr()}
			p.expect(Semicolon, "after assignment")
			return s
		}
	}
	x := p.parseExpr()
	if p.at(Assign) {
		p.fail(p.tok().Pos, "only a plain variable name can be assigned to")
	}
	if _, ok := x.(*CallExpr); !ok {
		p.fail(t.Pos, "expression result is unused; only function calls may be used as statements")
	}
	p.expect(Semicolon, "after expression statement")
	return &ExprStmt{Pos: t.Pos, X: x}
}

func (p *parser) parseIf() Stmt {
	p.enter()
	defer p.leave()
	t := p.expect(KwIf, "")
	s := &IfStmt{Pos: t.Pos, Cond: p.parseExpr(), Then: p.parseBlock()}
	if p.at(KwElse) {
		p.next()
		if p.at(KwIf) {
			s.Else = p.parseIf()
		} else {
			s.Else = &BlockStmt{Block: p.parseBlock()}
		}
	}
	return s
}

func (p *parser) parseArgs() []Expr {
	var args []Expr
	for !p.at(RParen) {
		args = append(args, p.parseExpr())
		if !p.at(Comma) {
			break
		}
		p.next()
	}
	p.expect(RParen, "to close argument list")
	return args
}

var binaryPrec = map[Kind]int{
	OrOr:      1,
	AndAnd:    2,
	Eq:        3,
	NotEq:     3,
	Less:      4,
	LessEq:    4,
	Greater:   4,
	GreaterEq: 4,
	Plus:      5,
	Minus:     5,
	Star:      6,
	Slash:     6,
	Percent:   6,
}

func (p *parser) parseExpr() Expr {
	return p.parseBinary(1)
}

func (p *parser) parseBinary(minPrec int) Expr {
	x := p.parseUnary()
	for {
		op := p.tok()
		prec, ok := binaryPrec[op.Kind]
		if !ok || prec < minPrec {
			return x
		}
		p.next()
		y := p.parseBinary(prec + 1)
		x = &BinaryExpr{Pos: op.Pos, Op: op.Kind, X: x, Y: y}
	}
}

func (p *parser) parseUnary() Expr {
	p.enter()
	defer p.leave()
	t := p.tok()
	if t.Kind == Minus || t.Kind == Bang {
		p.next()
		if lit := p.tok(); t.Kind == Minus && lit.Kind == IntTok && lit.Text == minIntMagnitude {
			p.next()
			return &IntLit{Pos: t.Pos, Text: "-" + lit.Text, Value: math.MinInt64}
		}
		return &UnaryExpr{Pos: t.Pos, Op: t.Kind, X: p.parseUnary()}
	}
	return p.parsePostfix()
}

func (p *parser) parsePostfix() Expr {
	x := p.parsePrimary()
	for {
		switch p.tok().Kind {
		case Dot:
			dot := p.next()
			id, ok := x.(*Ident)
			if !ok {
				p.fail(dot.Pos, "\".\" may only follow a module name")
			}
			name := p.expectIdent("after \".\"")
			x = &SelectorExpr{Pos: id.Pos, X: id, Name: name.Text}
		case LParen:
			lp := p.next()
			switch x.(type) {
			case *Ident, *SelectorExpr:
			default:
				p.fail(lp.Pos, "only named functions can be called")
			}
			x = &CallExpr{Pos: x.P(), Fn: x, Args: p.parseArgs()}
		default:
			return x
		}
	}
}

func (p *parser) parsePrimary() Expr {
	t := p.tok()
	switch t.Kind {
	case IntTok:
		p.next()
		if t.Text == minIntMagnitude {
			p.fail(t.Pos, "integer literal %s does not fit in a signed 64-bit int (only -%s is allowed)", t.Text, t.Text)
		}
		v, err := strconv.ParseInt(t.Text, 10, 64)
		if err != nil {
			p.fail(t.Pos, "invalid integer literal %s", t.Text)
		}
		return &IntLit{Pos: t.Pos, Text: t.Text, Value: v}
	case StringTok:
		p.next()
		return &StringLit{Pos: t.Pos, Value: t.Text}
	case KwTrue, KwFalse:
		p.next()
		return &BoolLit{Pos: t.Pos, Value: t.Kind == KwTrue}
	case IdentTok:
		p.next()
		return &Ident{Pos: t.Pos, Name: t.Text}
	case LParen:
		p.next()
		x := p.parseExpr()
		p.expect(RParen, "to close parenthesized expression")
		return x
	}
	p.fail(t.Pos, "expected expression, found %s", t.describe())
	return nil
}
