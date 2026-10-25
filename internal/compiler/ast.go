package compiler

type Node interface {
	P() Pos
}

type Expr interface {
	Node
	exprNode()
}

type Stmt interface {
	Node
	stmtNode()
}

type File struct {
	Path     string
	Imports  []*Import
	CImports []*CImport
	Funcs    []*FuncDecl
}

type Import struct {
	Pos      Pos
	Path     string
	PathPos  Pos
	Alias    string
	AliasPos Pos
}

type CImport struct {
	Pos     Pos
	Header  string
	HdrPos  Pos
	Externs []*FuncDecl
}

type TypeExpr struct {
	Pos  Pos
	Name string
}

type Param struct {
	Pos  Pos
	Name string
	Type *TypeExpr
}

type FuncDecl struct {
	Pos     Pos
	NamePos Pos
	Pub     bool
	Name    string
	Params  []*Param
	Result  *TypeExpr
	Body    *Block
	Header  string
}

type Block struct {
	Pos   Pos
	Stmts []Stmt
	End   Pos
}

type LetStmt struct {
	Pos     Pos
	Mutable bool
	Name    string
	NamePos Pos
	Type    *TypeExpr
	Value   Expr
}

type AssignStmt struct {
	Pos   Pos
	Name  string
	Value Expr
}

type IfStmt struct {
	Pos  Pos
	Cond Expr
	Then *Block
	Else Stmt
}

type WhileStmt struct {
	Pos  Pos
	Cond Expr
	Body *Block
}

type ReturnStmt struct {
	Pos   Pos
	Value Expr
}

type PrintStmt struct {
	Pos  Pos
	Args []Expr
}

type ExprStmt struct {
	Pos Pos
	X   Expr
}

type BreakStmt struct {
	Pos Pos
}

type ContinueStmt struct {
	Pos Pos
}

type BlockStmt struct {
	Block *Block
}

type IntLit struct {
	Pos   Pos
	Text  string
	Value int64
}

type StringLit struct {
	Pos   Pos
	Value string
}

type BoolLit struct {
	Pos   Pos
	Value bool
}

type Ident struct {
	Pos  Pos
	Name string
}

type UnaryExpr struct {
	Pos Pos
	Op  Kind
	X   Expr
}

type BinaryExpr struct {
	Pos Pos
	Op  Kind
	X   Expr
	Y   Expr
}

type CallExpr struct {
	Pos  Pos
	Fn   Expr
	Args []Expr
}

type SelectorExpr struct {
	Pos  Pos
	X    *Ident
	Name string
}

func (b *Block) P() Pos        { return b.Pos }
func (s *LetStmt) P() Pos      { return s.Pos }
func (s *AssignStmt) P() Pos   { return s.Pos }
func (s *IfStmt) P() Pos       { return s.Pos }
func (s *WhileStmt) P() Pos    { return s.Pos }
func (s *ReturnStmt) P() Pos   { return s.Pos }
func (s *PrintStmt) P() Pos    { return s.Pos }
func (s *ExprStmt) P() Pos     { return s.Pos }
func (s *BreakStmt) P() Pos    { return s.Pos }
func (s *ContinueStmt) P() Pos { return s.Pos }
func (s *BlockStmt) P() Pos    { return s.Block.Pos }
func (e *IntLit) P() Pos       { return e.Pos }
func (e *StringLit) P() Pos    { return e.Pos }
func (e *BoolLit) P() Pos      { return e.Pos }
func (e *Ident) P() Pos        { return e.Pos }
func (e *UnaryExpr) P() Pos    { return e.Pos }
func (e *BinaryExpr) P() Pos   { return e.Pos }
func (e *CallExpr) P() Pos     { return e.Pos }
func (e *SelectorExpr) P() Pos { return e.Pos }

func (*LetStmt) stmtNode()      {}
func (*AssignStmt) stmtNode()   {}
func (*IfStmt) stmtNode()       {}
func (*WhileStmt) stmtNode()    {}
func (*ReturnStmt) stmtNode()   {}
func (*PrintStmt) stmtNode()    {}
func (*ExprStmt) stmtNode()     {}
func (*BreakStmt) stmtNode()    {}
func (*ContinueStmt) stmtNode() {}
func (*BlockStmt) stmtNode()    {}

func (*IntLit) exprNode()       {}
func (*StringLit) exprNode()    {}
func (*BoolLit) exprNode()      {}
func (*Ident) exprNode()        {}
func (*UnaryExpr) exprNode()    {}
func (*BinaryExpr) exprNode()   {}
func (*CallExpr) exprNode()     {}
func (*SelectorExpr) exprNode() {}
