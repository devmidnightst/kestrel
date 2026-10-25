package compiler

import (
	"os"
	"path/filepath"
	"testing"
)

func addFuzzSeeds(f *testing.F) {
	f.Add("fn main() { print(1 + 2 * 3); }")
	f.Add("fn f(a: int) -> int { if a > 0 { return a; } else { return -a; } }\nfn main() { var x = f(-3); while x > 0 { x = x - 1; } }")
	f.Add("import c \"stdlib.h\" { fn llabs(n: int) -> int; }\nfn main() { print(llabs(-1), \"s\\n\", true && !false); }")
	f.Add("fn main() { let s = \"a\" + str(len(\"bc\")); { let s = 1; } break; }")
	f.Add("fn main() { ((((((1)))))); }")
	paths, _ := filepath.Glob(filepath.Join("..", "..", "examples", "*.ks"))
	for _, p := range paths {
		if data, err := os.ReadFile(p); err == nil {
			f.Add(string(data))
		}
	}
}

func FuzzLex(f *testing.F) {
	addFuzzSeeds(f)
	f.Fuzz(func(t *testing.T, src string) {
		Lex("fuzz.ks", []byte(src))
	})
}

func FuzzParse(f *testing.F) {
	addFuzzSeeds(f)
	f.Fuzz(func(t *testing.T, src string) {
		Parse("fuzz.ks", []byte(src))
	})
}

func FuzzCheckAndGenerate(f *testing.F) {
	addFuzzSeeds(f)
	f.Fuzz(func(t *testing.T, src string) {
		prog, err := CheckSource("fuzz.ks", []byte(src))
		if err == nil {
			GenerateC(prog)
		}
	})
}
