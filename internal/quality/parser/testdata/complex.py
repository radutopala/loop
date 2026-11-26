# branchy exercises if/elif/for/while/except/match/ternary/comprehension/and/or.
def branchy(self, a, b, *, c=None):
    x = 0
    if a > 0 and b > 0:
        for i in range(a):
            if c or i > 5:
                x += i
    elif a < 0:
        while b:
            b -= 1
    try:
        x = 1 if a else 2
    except ValueError:
        pass
    match a:
        case 1:
            x = 1
        case _:
            x = -1
    return [i for i in range(x) if i]


def trivial(n):
    return n + 1
