fn is_prime(n: int) -> bool {
    if n < 2 {
        return false;
    }
    var d = 2;
    while d * d <= n {
        if n % d == 0 {
            return false;
        }
        d = d + 1;
    }
    return true;
}

fn main() {
    var sum = 0;
    var i = 1;
    while i <= 100 {
        sum = sum + i;
        i = i + 1;
    }
    print("sum 1..100 =", sum);

    var primes = "";
    var n = 0;
    var found = 0;
    while true {
        n = n + 1;
        if !is_prime(n) {
            continue;
        }
        primes = primes + str(n) + " ";
        found = found + 1;
        if found == 10 {
            break;
        }
    }
    print("first ten primes:", primes);

    var a = 0;
    var b = 1;
    var k = 0;
    while k < 10 {
        let next = a + b;
        a = b;
        b = next;
        k = k + 1;
    }
    print("fib(10) =", a);
}
