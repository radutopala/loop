import { x } from './x';
export { y } from './y';

class Widget {
	hello() {
		return greet();
	}
}

function makeWidget() {
	return new Widget();
}
