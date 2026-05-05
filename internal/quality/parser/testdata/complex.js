export function branchy(a, b, c) {
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

export function trivial(n) {
  return n + 1;
}
