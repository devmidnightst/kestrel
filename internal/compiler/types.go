package compiler

type Type int

const (
	Invalid Type = iota
	Void
	IntType
	BoolType
	StringType
)

func (t Type) String() string {
	switch t {
	case Void:
		return "void"
	case IntType:
		return "int"
	case BoolType:
		return "bool"
	case StringType:
		return "string"
	}
	return "invalid"
}

var namedTypes = map[string]Type{
	"int":    IntType,
	"bool":   BoolType,
	"string": StringType,
}

type Module struct {
	ID      int
	Path    string
	Display string
	File    *File
	Imports map[string]*Module
	Funcs   map[string]*Func
	Order   []*Func
	Headers []string
}

type Func struct {
	Name    string
	CName   string
	Params  []*Var
	Result  Type
	Pub     bool
	Extern  bool
	Decl    *FuncDecl
	Module  *Module
	Builtin bool
}

type Var struct {
	Name    string
	CName   string
	Type    Type
	Mutable bool
	Pos     Pos
}

type Info struct {
	Types   map[Expr]Type
	Uses    map[*Ident]*Var
	Defs    map[*LetStmt]*Var
	Calls   map[*CallExpr]*Func
	Assigns map[*AssignStmt]*Var
}

type Program struct {
	Modules []*Module
	Entry   *Module
	Main    *Func
	Info    *Info
}

var builtins = map[string]*Func{
	"len": {Name: "len", CName: "ks_len", Builtin: true, Params: []*Var{{Name: "s", Type: StringType}}, Result: IntType},
	"str": {Name: "str", CName: "ks_str", Builtin: true, Params: []*Var{{Name: "n", Type: IntType}}, Result: StringType},
}
