fn sort(values: [int]) -> [int] {
    var a = values;
    var i = 1;
    while i < len(a) {
        let key = a[i];
        var j = i - 1;
        while j >= 0 && a[j] > key {
            a[j + 1] = a[j];
            j = j - 1;
        }
        a[j + 1] = key;
        i = i + 1;
    }
    return a;
}

fn main() {
    let numbers = [42, 7, 19, 3, 88, 1];
    let sorted = sort(numbers);
    print("original:", numbers);
    print("sorted:", sorted);

    var stack: [string] = [];
    push(stack, "a");
    push(stack, "b");
    push(stack, "c");
    let top = pop(stack);
    print("popped", top, "left", stack);

    var grid = fill(fill(0, 3), 3);
    var r = 0;
    while r < 3 {
        grid[r][r] = 1;
        r = r + 1;
    }
    print("identity:", grid);
    print("middle:", slice(sorted, 1, 4));
}
