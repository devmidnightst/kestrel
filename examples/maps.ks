fn main() {
    var counts: {string: int} = {};
    counts["go"] = 2;
    counts["to"] = 1;
    counts["go"] = counts["go"] + 1;
    var copy = counts;
    let removed = delete(counts, "to");
    var total = 0;
    for word in counts {
        total = total + counts[word];
    }
    print(removed, total, len(counts), copy["to"]);
}
