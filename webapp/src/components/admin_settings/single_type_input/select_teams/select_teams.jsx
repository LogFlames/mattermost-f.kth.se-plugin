import AbstractSelectInput from "../../abstract/abstract_select_input/abstract_select_input.jsx";

export default class SelectTeams extends AbstractSelectInput {
    constructor(props) {
        super(props, 'Select teams', 'teams', true);
    }

    render() {
        return super.render();
    }
}