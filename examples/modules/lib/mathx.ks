pub fn gcd(a: int, b: int) -> int {
    var x = abs(a);
    var y = abs(b);
    while y != 0 {
        let r = x % y;
        x = y;
        y = r;
    }
    return x;
}

pub fn pow(base: int, exp: int) -> int {
    var result = 1;
    var i = 0;
    while i < exp {
        result = result * base;
        i = i + 1;
    }
    return result;
}

fn abs(n: int) -> int {
    if n < 0 {
        return -n;
    }
    return n;
}
