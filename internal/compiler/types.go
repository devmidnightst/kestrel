package compiler

import "sync"

type TypeKind int

const (
	KInvalid TypeKind = iota
	KVoid
	KInt
	KBool
	KFloat
	KString
	KArray
	KStruct
)

type TypeDesc struct {
	Kind   TypeKind
	Elem   *TypeDesc
	Struct *StructInfo
}

type Type = *TypeDesc

var (
	Invalid    = &TypeDesc{Kind: KInvalid}
	Void       = &TypeDesc{Kind: KVoid}
	IntType    = &TypeDesc{Kind: KInt}
	BoolType   = &TypeDesc{Kind: KBool}
	FloatType  = &TypeDesc{Kind: KFloat}
	StringType = &TypeDesc{Kind: KString}
)

var (
	arrayMu    sync.Mutex
	arrayTypes = map[Type]Type{}
)

func ArrayOf(elem Type) Type {
	if elem == Invalid {
		return Invalid
	}
	arrayMu.Lock()
	defer arrayMu.Unlock()
	if t, ok := arrayTypes[elem]; ok {
		return t
	}
	t := &TypeDesc{Kind: KArray, Elem: elem}
	arrayTypes[elem] = t
	return t
}

func (t *TypeDesc) String() string {
	switch t.Kind {
	case KVoid:
		return "void"
	case KInt:
		return "int"
	case KBool:
		return "bool"
	case KFloat:
		return "float"
	case KString:
		return "string"
	case KArray:
		return "[" + t.Elem.String() + "]"
	case KStruct:
		return t.Struct.Name
	}
	return "invalid"
}

func (t *TypeDesc) Managed() bool {
	return t.Kind == KString || t.Kind == KArray || t.Kind == KStruct
}

func (t *TypeDesc) Comparable() bool {
	return t.Kind == KInt || t.Kind == KBool || t.Kind == KFloat || t.Kind == KString
}

func (t *TypeDesc) Numeric() bool {
	return t.Kind == KInt || t.Kind == KFloat
}

var namedTypes = map[string]Type{
	"int":    IntType,
	"bool":   BoolType,
	"float":  FloatType,
	"string": StringType,
}

type StructInfo struct {
	ID       int
	Name     string
	CName    string
	Pub      bool
	Module   *Module
	Decl     *StructDecl
	Fields   []*Field
	FieldMap map[string]*Field
	Type     Type
}

type Field struct {
	Name string
	Type Type
	Pos  Pos
}

type Module struct {
	ID          int
	Path        string
	Display     string
	File        *File
	Imports     map[string]*Module
	Funcs       map[string]*Func
	Order       []*Func
	Structs     map[string]*StructInfo
	StructOrder []*StructInfo
	Headers     []string
}

type Func struct {
	Name   string
	CName  string
	Params []*Var
	Result Type
	Pub    bool
	Extern bool
	Decl   *FuncDecl
	Module *Module
}

type Var struct {
	Name    string
	CName   string
	Type    Type
	Mutable bool
	Param   bool
	Pos     Pos
}

type Info struct {
	Types    map[Expr]Type
	Uses     map[*Ident]*Var
	Defs     map[*LetStmt]*Var
	Calls    map[*CallExpr]*Func
	Builtins map[*CallExpr]string
	Fields   map[*SelectorExpr]*Field
	Literals map[*StructLit]*StructInfo
}

type Program struct {
	Modules []*Module
	Structs []*StructInfo
	Entry   *Module
	Main    *Func
	Info    *Info
}
