import AbstractSelectInput from "../../abstract/abstract_select_input/abstract_select_input.jsx";

export default class JoinLeaveFreeBotUserId extends AbstractSelectInput {
    constructor(props) {
        super(props, 'Select bot user id', 'users', false);
    }

    render() {
        return super.render();
    }
}