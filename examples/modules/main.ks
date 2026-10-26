import "lib/mathx.ks";
import "lib/text.ks" as t;

fn main() -> int {
    print("gcd(84, 36) =", mathx.gcd(84, 36));
    print("pow(3, 4) =", mathx.pow(3, 4));
    print(t.banner("modules"));
    return 0;
}
