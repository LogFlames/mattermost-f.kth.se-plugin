import AbstractSelectInput from "../../abstract/abstract_select_input/abstract_select_input.jsx";

export default class SelectUsers extends AbstractSelectInput {
    constructor(props) {
        super(props, 'Select users', 'users', true);
    }

    render() {
        return super.render();
    }
}