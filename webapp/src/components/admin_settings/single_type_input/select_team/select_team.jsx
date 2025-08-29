import AbstractSelectInput from "../../abstract/abstract_select_input/abstract_select_input.jsx";

export default class SelectTeam extends AbstractSelectInput {
    constructor(props) {
        super(props, 'Select a team', 'teams', false);
    }

    render() {
        return super.render();
    }
}