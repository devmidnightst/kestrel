fn square(n: int) -> int {
    return n * n;
}

fn greet(who: string) -> string {
    return "hello, " + who + "!";
}

fn factorial(n: int) -> int {
    if n <= 1 {
        return 1;
    }
    return n * factorial(n - 1);
}

fn is_even(n: int) -> bool {
    return n % 2 == 0;
}

fn report(label: string, value: int) {
    print(label, "=", value);
}

fn main() {
    report("square(12)", square(12));
    print(greet("world"));
    report("factorial(20)", factorial(20));
    print("is_even(10):", is_even(10), "is_even(7):", is_even(7));
}
