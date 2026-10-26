fn main() {
    let name = "kestrel";
    let year: int = 2026;
    var count = 0;
    count = count + 1;
    count = count * 10;
    let ready: bool = count > 5 && !false;
    print("name:", name);
    print("year:", year, "count:", count);
    print("ready:", ready);
    print("length of name:", len(name));
    print("joined: " + name + " " + str(year));
    {
        let name = "shadowed";
        print("inner:", name);
    }
    print("outer:", name);
    print("precedence:", 2 + 3 * 4, (2 + 3) * 4, 17 % 5, -7 / 2);
}
