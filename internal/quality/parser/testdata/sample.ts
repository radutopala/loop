import { x } from './x';
import './side-effect';
export { y } from './y';

interface IFoo {
	n: number;
}

type Bar = number;

class Widget {
	hello(): string {
		return greet();
	}
}

function makeWidget(): Widget {
	return new Widget();
}
