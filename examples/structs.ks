struct Item {
    name: string,
    price: float,
    qty: int,
}

struct Inventory {
    owner: string,
    items: [Item],
}

fn value(inv: Inventory) -> float {
    var total = 0.0;
    var i = 0;
    while i < len(inv.items) {
        let it = inv.items[i];
        total = total + it.price * float(it.qty);
        i = i + 1;
    }
    return total;
}

fn main() {
    var shop = Inventory { owner: "kes", items: [] };
    push(shop.items, Item { name: "rope", price: 4.5, qty: 3 });
    push(shop.items, Item { name: "lamp", price: 12.25, qty: 1 });

    let yesterday = shop;
    shop.items[0].qty = 10;

    print(shop.items[0]);
    print("today:", value(shop), "yesterday:", value(yesterday));
    print(yesterday);
}
