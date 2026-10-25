fn classify(n: int) -> string {
    if n < 0 {
        return "negative";
    } else if n == 0 {
        return "zero";
    } else if n < 10 {
        return "small";
    } else {
        return "large";
    }
}

fn fizzbuzz(n: int) -> string {
    if n % 15 == 0 {
        return "fizzbuzz";
    }
    if n % 3 == 0 {
        return "fizz";
    }
    if n % 5 == 0 {
        return "buzz";
    }
    return str(n);
}

fn main() {
    print(classify(-5), classify(0), classify(7), classify(42));
    var i = 1;
    while i <= 15 {
        print(fizzbuzz(i));
        i = i + 1;
    }
    let a = 3;
    let b = 4;
    if a < b || a == b {
        print("a is at most b");
    }
    if "kestrel" != "falcon" {
        print("strings compare by value");
    }
}
