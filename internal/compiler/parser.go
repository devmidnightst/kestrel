package compiler

import (
	"math"
	"strconv"
)

const minIntMagnitude = "9223372036854775808"

const maxNesting = 256

type parser struct {
	toks     []Token
	pos      int
	depth    int
	noStruct bool
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

func (p *parser) enter() {
	p.depth++
	if p.depth > maxNesting {
		p.fail(p.tok().Pos, "nesting too deep (more than %d levels)", maxNesting)
	}
}

func (p *parser) leave() {
	p.depth--
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

func (p *parser) withStructLits(fn func()) {
	saved := p.noStruct
	p.noStruct = false
	fn()
	p.noStruct = saved
}

func (p *parser) parseFile(path string) *File {
	f := &File{Path: path}
	for p.at(KwImport) {
		p.parseImport(f)
	}
	for !p.at(EOF) {
		kind := p.tok().Kind
		if kind == KwPub {
			kind = p.peekKind(1)
		}
		switch {
		case kind == KwFn:
			f.Funcs = append(f.Funcs, p.parseFunc())
		case kind == KwStruct:
			f.Structs = append(f.Structs, p.parseStruct())
		case p.at(KwImport):
			p.fail(p.tok().Pos, "imports must appear before all declarations")
		default:
			p.fail(p.tok().Pos, "expected function or struct declaration, found %s", p.tok().describe())
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

func (p *parser) parseStruct() *StructDecl {
	start := p.tok().Pos
	pub := false
	if p.at(KwPub) {
		pub = true
		p.next()
	}
	p.expect(KwStruct, "to begin struct declaration")
	name := p.expectIdent("for struct name")
	decl := &StructDecl{Pos: start, NamePos: name.Pos, Pub: pub, Name: name.Text}
	p.expect(LBrace, "after struct name")
	for !p.at(RBrace) {
		fn := p.expectIdent("for field name")
		p.expect(Colon, "after field name")
		decl.Fields = append(decl.Fields, &FieldDecl{Pos: fn.Pos, Name: fn.Text, Type: p.parseType()})
		if !p.at(Comma) {
			break
		}
		p.next()
	}
	p.expect(RBrace, "to close struct declaration")
	return decl
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
	p.enter()
	defer p.leave()
	if p.at(LBracket) {
		lb := p.next()
		elem := p.parseType()
		p.expect(RBracket, "to close array type")
		return &TypeExpr{Pos: lb.Pos, Elem: elem}
	}
	if p.at(LBrace) {
		lb := p.next()
		key := p.parseType()
		p.expect(Colon, "in map type")
		val := p.parseType()
		p.expect(RBrace, "to close map type")
		return &TypeExpr{Pos: lb.Pos, Key: key, Elem: val}
	}
	t := p.expectIdent("for type")
	te := &TypeExpr{Pos: t.Pos, Name: t.Text}
	if p.at(Dot) {
		p.next()
		n := p.expectIdent("for type name after \".\"")
		te.Module, te.Name = t.Text, n.Text
	}
	return te
}

func (p *parser) parseMapLit(pos Pos) *MapLit {
	p.enter()
	defer p.leave()
	p.next()
	lit := &MapLit{Pos: pos}
	if p.at(RBrace) {
		p.next()
		return lit
	}
	for {
		key := p.parseExpr()
		p.expect(Colon, "between map key and value")
		val := p.parseExpr()
		lit.Entries = append(lit.Entries, MapEntry{Key: key, Value: val})
		if !p.at(Comma) {
			break
		}
		p.next()
		if p.at(RBrace) {
			break
		}
	}
	p.expect(RBrace, "to close map literal")
	return lit
}

func (p *parser) parseBlock() *Block {
	p.enter()
	defer p.leave()
	lb := p.expect(LBrace, "to open block")
	b := &Block{Pos: lb.Pos}
	saved := p.noStruct
	p.noStruct = false
	for !p.at(RBrace) {
		if p.at(EOF) {
			p.fail(p.tok().Pos, "unexpected end of file, expected \"}\" to close block opened at %d:%d", lb.Pos.Line, lb.Pos.Col)
		}
		b.Stmts = append(b.Stmts, p.parseStmt())
	}
	p.noStruct = saved
	b.End = p.next().Pos
	return b
}

func (p *parser) parseCond() Expr {
	saved := p.noStruct
	p.noStruct = true
	x := p.parseExpr()
	p.noStruct = saved
	p.rejectRange()
	if p.at(LBrace) && p.peekKind(1) == IdentTok && p.peekKind(2) == Colon {
		p.fail(p.tok().Pos, "struct literals in if and while conditions must be wrapped in parentheses")
	}
	return x
}

func isPlace(e Expr) bool {
	switch e := e.(type) {
	case *Ident:
		return true
	case *IndexExpr:
		return isPlace(e.X)
	case *SelectorExpr:
		return isPlace(e.X)
	}
	return false
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
		p.rejectRange()
		p.expect(Semicolon, "after variable declaration")
		return s
	case KwIf:
		return p.parseIf()
	case KwWhile:
		p.next()
		cond := p.parseCond()
		return &WhileStmt{Pos: t.Pos, Cond: cond, Body: p.parseBlock()}
	case KwFor:
		return p.parseFor()
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
	}
	x := p.parseExpr()
	p.rejectRange()
	if p.at(Assign) {
		if !isPlace(x) {
			p.fail(p.tok().Pos, "cannot assign to this expression (only variables, struct fields and array elements can be assigned)")
		}
		p.next()
		s := &AssignStmt{Pos: t.Pos, Target: x, Value: p.parseExpr()}
		p.expect(Semicolon, "after assignment")
		return s
	}
	if _, ok := x.(*CallExpr); !ok {
		p.fail(t.Pos, "expression result is unused; only function calls may be used as statements")
	}
	p.expect(Semicolon, "after expression statement")
	return &ExprStmt{Pos: t.Pos, X: x}
}

func (p *parser) sawRange() bool {
	return p.at(Dot) && p.peekKind(1) == Dot
}

func (p *parser) rejectRange() {
	if p.sawRange() {
		p.fail(p.tok().Pos, "a range is only allowed in for (for i in start..end or for i in start..=end)")
	}
}

func (p *parser) parseFor() *ForStmt {
	p.enter()
	defer p.leave()
	t := p.next()
	name := p.expectIdent("after for")
	p.expect(KwIn, "after the loop variable (for x in ...)")
	s := &ForStmt{Pos: t.Pos, Name: name.Text, NamePos: name.Pos}
	// The '{' of the body would otherwise be read as a struct literal.
	saved := p.noStruct
	p.noStruct = true
	start := p.parseExpr()
	if p.sawRange() {
		p.next()
		p.next()
		s.Kind = ForRange
		if p.at(Assign) {
			p.next()
			s.Kind = ForInclusive
		}
		s.Start = start
		s.End = p.parseExpr()
		p.rejectRange()
	} else {
		s.Kind = ForArray
		s.Seq = start
	}
	p.noStruct = saved
	if p.at(LBrace) && p.peekKind(1) == IdentTok && p.peekKind(2) == Colon {
		p.fail(p.tok().Pos, "struct literals in for must be wrapped in parentheses")
	}
	s.Body = p.parseBlock()
	return s
}

func (p *parser) parseIf() Stmt {
	p.enter()
	defer p.leave()
	t := p.expect(KwIf, "")
	s := &IfStmt{Pos: t.Pos, Cond: p.parseCond(), Then: p.parseBlock()}
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

func (p *parser) parseList(end Kind, context string) []Expr {
	var list []Expr
	p.withStructLits(func() {
		for !p.at(end) {
			list = append(list, p.parseExpr())
			p.rejectRange()
			if !p.at(Comma) {
				break
			}
			p.next()
		}
		p.expect(end, context)
	})
	return list
}

func (p *parser) parseArgs() []Expr {
	return p.parseList(RParen, "to close argument list")
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
			if p.peekKind(1) == Dot {
				return x
			}
			p.next()
			name := p.expectIdent("after \".\"")
			x = &SelectorExpr{X: x, Name: name.Text, NamePos: name.Pos}
		case LBracket:
			p.next()
			var idx Expr
			p.withStructLits(func() {
				idx = p.parseExpr()
				p.expect(RBracket, "to close index expression")
			})
			x = &IndexExpr{X: x, Index: idx}
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

func (p *parser) parseStructLit(pos Pos, module, name string) Expr {
	lit := &StructLit{Pos: pos, Module: module, Name: name}
	p.expect(LBrace, "to open struct literal")
	p.withStructLits(func() {
		for !p.at(RBrace) {
			fn := p.expectIdent("for field name in struct literal")
			p.expect(Colon, "after field name")
			lit.Fields = append(lit.Fields, &FieldInit{Pos: fn.Pos, Name: fn.Text, Value: p.parseExpr()})
			if !p.at(Comma) {
				break
			}
			p.next()
		}
		p.expect(RBrace, "to close struct literal")
	})
	return lit
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
	case FloatTok:
		p.next()
		v, err := strconv.ParseFloat(t.Text, 64)
		if err != nil {
			p.fail(t.Pos, "invalid float literal %s", t.Text)
		}
		return &FloatLit{Pos: t.Pos, Text: t.Text, Value: v}
	case StringTok:
		p.next()
		return &StringLit{Pos: t.Pos, Value: t.Text}
	case KwTrue, KwFalse:
		p.next()
		return &BoolLit{Pos: t.Pos, Value: t.Kind == KwTrue}
	case IdentTok:
		p.next()
		if !p.noStruct {
			if p.at(LBrace) {
				return p.parseStructLit(t.Pos, "", t.Text)
			}
			if p.at(Dot) && p.peekKind(1) == IdentTok && p.peekKind(2) == LBrace {
				p.next()
				name := p.next()
				return p.parseStructLit(t.Pos, t.Text, name.Text)
			}
		}
		return &Ident{Pos: t.Pos, Name: t.Text}
	case LParen:
		p.next()
		var x Expr
		p.withStructLits(func() {
			x = p.parseExpr()
			p.expect(RParen, "to close parenthesized expression")
		})
		return x
	case LBracket:
		p.next()
		return &ArrayLit{Pos: t.Pos, Elems: p.parseList(RBracket, "to close array literal")}
	case LBrace:
		return p.parseMapLit(t.Pos)
	}
	p.fail(t.Pos, "expected expression, found %s", t.describe())
	return nil
}
