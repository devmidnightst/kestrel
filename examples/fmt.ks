struct pair {
    a: int,
    b: string,
}

fn main() {
    print(fmt("{} is {}", "kestrel", 2026));
    print(fmt("braces {{ }} and {}", true));
    print(fmt("pi {}", 3.5));
    print(fmt("none"));
    print(fmt("{}", "a{b}"));
    print(1, [1, 2], pair { a: 1, b: "x" });
}
