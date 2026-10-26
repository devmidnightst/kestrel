struct Count {
    word: string,
    n: int,
}

fn bump(counts: [Count], word: string) -> [Count] {
    var out = counts;
    var i = 0;
    while i < len(out) {
        if out[i].word == word {
            out[i].n = out[i].n + 1;
            return out;
        }
        i = i + 1;
    }
    push(out, Count { word: word, n: 1 });
    return out;
}

fn main() {
    var counts: [Count] = [];
    var lines = 0;
    var line = read_line();
    while !eof() {
        lines = lines + 1;
        let words = split(lower(trim(line)), " ");
        var i = 0;
        while i < len(words) {
            if len(words[i]) > 0 {
                counts = bump(counts, words[i]);
            }
            i = i + 1;
        }
        line = read_line();
    }
    print("lines:", lines);
    var i = 0;
    while i < len(counts) {
        print(counts[i].word, counts[i].n);
        i = i + 1;
    }
}
