fn main() {
    var s = 0;
    for i in 0..5 {
        s = s + i;
    }
    print(s);

    var t = 0;
    for i in 1..=4 {
        if i == 2 {
            continue;
        }
        if i == 4 {
            break;
        }
        t = t + i;
    }
    print(t);

    var words = ["go", "to", "the"];
    var n = 0;
    for w in words {
        n = n + len(w);
    }
    print(n);

    var nums = [1, 2];
    for x in nums {
        push(nums, x);
    }
    print(len(nums));
}
