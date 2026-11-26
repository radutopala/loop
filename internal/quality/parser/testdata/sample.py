import os
import os.path as osp
from . import sibling
from ..pkg.mod import thing, other
from pkg import x


class Widget(Base):
    def greet(self, name, *args, **kw):
        return helper(name)


@decorated
def make_widget(x: int, *, y: str = "") -> None:
    obj.build(x)
