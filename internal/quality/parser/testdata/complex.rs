// branchy exercises if/for/while/loop/match/&&/|| in Rust.
fn branchy(&self, a: i32, b: i32, c: bool) -> i32 {
    let mut x = 0;
    if a > 0 && b > 0 {
        for i in 0..a {
            if c || i > 5 {
                x += i;
            }
        }
    } else {
        while x < 3 {
            loop {
                break;
            }
            x += 1;
        }
    }
    match a {
        1 => x = 1,
        _ => x = -1,
    }
    x
}

fn trivial(n: i32) -> i32 {
    n + 1
}
