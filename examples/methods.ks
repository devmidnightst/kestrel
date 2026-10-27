struct item {
    name: string,
    qty: int,
}

fn (self: item) cost(price: int) -> int {
    return self.qty * price;
}

fn (var self: item) add(n: int) {
    self.qty = self.qty + n;
}

fn main() {
    var rope = item { name: "rope", qty: 3 };
    let before = rope;
    rope.add(2);
    print(before.cost(4), rope.cost(4));
    print(before.qty, rope.qty);
}
