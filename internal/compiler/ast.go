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
	Structs  []*StructDecl
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
	Pos    Pos
	Name   string
	Module string
	Elem   *TypeExpr
	Key    *TypeExpr
}

func (t *TypeExpr) String() string {
	switch {
	case t.Key != nil:
		return "{" + t.Key.String() + ": " + t.Elem.String() + "}"
	case t.Elem != nil:
		return "[" + t.Elem.String() + "]"
	case t.Module != "":
		return t.Module + "." + t.Name
	}
	return t.Name
}

type StructDecl struct {
	Pos     Pos
	NamePos Pos
	Pub     bool
	Name    string
	Fields  []*FieldDecl
}

type FieldDecl struct {
	Pos  Pos
	Name string
	Type *TypeExpr
}

type Param struct {
	Pos  Pos
	Name string
	Type *TypeExpr
}

type FuncDecl struct {
	Pos         Pos
	NamePos     Pos
	Pub         bool
	Name        string
	Recv        *Param
	RecvMutable bool
	Params      []*Param
	Result      *TypeExpr
	Body        *Block
	Header      string
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
	Pos    Pos
	Target Expr
	Value  Expr
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

type ForKind int

const (
	ForRange ForKind = iota
	ForInclusive
	ForArray
)

// ForStmt is `for name in start..end`, `for name in start..=end`, or
// `for name in array`. The name is in scope only in the body.
type ForStmt struct {
	Pos     Pos
	Name    string
	NamePos Pos
	Kind    ForKind
	Start   Expr
	End     Expr
	Seq     Expr
	Body    *Block
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

type FloatLit struct {
	Pos   Pos
	Text  string
	Value float64
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
	X       Expr
	Name    string
	NamePos Pos
}

type IndexExpr struct {
	X     Expr
	Index Expr
}

type ArrayLit struct {
	Pos   Pos
	Elems []Expr
}

type MapLit struct {
	Pos     Pos
	Entries []MapEntry
}

type MapEntry struct {
	Key   Expr
	Value Expr
}

type StructLit struct {
	Pos    Pos
	Module string
	Name   string
	Fields []*FieldInit
}

type FieldInit struct {
	Pos   Pos
	Name  string
	Value Expr
}

func (b *Block) P() Pos        { return b.Pos }
func (s *LetStmt) P() Pos      { return s.Pos }
func (s *AssignStmt) P() Pos   { return s.Pos }
func (s *IfStmt) P() Pos       { return s.Pos }
func (s *WhileStmt) P() Pos    { return s.Pos }
func (s *ForStmt) P() Pos      { return s.Pos }
func (s *ReturnStmt) P() Pos   { return s.Pos }
func (s *PrintStmt) P() Pos    { return s.Pos }
func (s *ExprStmt) P() Pos     { return s.Pos }
func (s *BreakStmt) P() Pos    { return s.Pos }
func (s *ContinueStmt) P() Pos { return s.Pos }
func (s *BlockStmt) P() Pos    { return s.Block.Pos }
func (e *IntLit) P() Pos       { return e.Pos }
func (e *FloatLit) P() Pos     { return e.Pos }
func (e *StringLit) P() Pos    { return e.Pos }
func (e *BoolLit) P() Pos      { return e.Pos }
func (e *Ident) P() Pos        { return e.Pos }
func (e *UnaryExpr) P() Pos    { return e.Pos }
func (e *BinaryExpr) P() Pos   { return e.Pos }
func (e *CallExpr) P() Pos     { return e.Pos }
func (e *SelectorExpr) P() Pos { return e.X.P() }
func (e *IndexExpr) P() Pos    { return e.X.P() }
func (e *ArrayLit) P() Pos     { return e.Pos }
func (e *MapLit) P() Pos       { return e.Pos }
func (e *StructLit) P() Pos    { return e.Pos }

func (*LetStmt) stmtNode()      {}
func (*AssignStmt) stmtNode()   {}
func (*IfStmt) stmtNode()       {}
func (*WhileStmt) stmtNode()    {}
func (*ForStmt) stmtNode()      {}
func (*ReturnStmt) stmtNode()   {}
func (*PrintStmt) stmtNode()    {}
func (*ExprStmt) stmtNode()     {}
func (*BreakStmt) stmtNode()    {}
func (*ContinueStmt) stmtNode() {}
func (*BlockStmt) stmtNode()    {}

func (*IntLit) exprNode()       {}
func (*FloatLit) exprNode()     {}
func (*StringLit) exprNode()    {}
func (*BoolLit) exprNode()      {}
func (*Ident) exprNode()        {}
func (*UnaryExpr) exprNode()    {}
func (*BinaryExpr) exprNode()   {}
func (*CallExpr) exprNode()     {}
func (*SelectorExpr) exprNode() {}
func (*IndexExpr) exprNode()    {}
func (*ArrayLit) exprNode()     {}
func (*MapLit) exprNode()       {}
func (*StructLit) exprNode()    {}
