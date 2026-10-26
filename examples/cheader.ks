import c "stdlib.h" {
    fn llabs(n: int) -> int;
}

fn main() {
    print("llabs(-42) =", llabs(-42));
}
