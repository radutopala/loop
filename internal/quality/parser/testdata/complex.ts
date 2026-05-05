// branchy exercises if/for/while/switch/ternary/catch/&&/|| in TS.
export function branchy(a: number, b: number, c: boolean): number {
  let x = 0;
  if (a > 0 && b > 0) {
    for (let i = 0; i < a; i++) {
      if (c || i > 5) {
        x += i;
      }
    }
  } else {
    switch (a) {
      case 1:
        x = 1;
        break;
      case 2:
        x = 2;
        break;
      default:
        x = -1;
    }
  }
  return x > 0 ? x : -x;
}

export function trivial(n: number): number {
  return n + 1;
}
