import AbstractSelectInput from "../../abstract/abstract_select_input/abstract_select_input.jsx";

export default class SelectUser extends AbstractSelectInput {
    constructor(props) {
        super(props, 'Select an user', 'users', false);
    }

    render() {
        return super.render();
    }
}