import AbstractSelectInput from "../../abstract/abstract_select_input/abstract_select_input.jsx";

export default class ModeratorBotChannels extends AbstractSelectInput {
    constructor(props) {
        super(props, 'Select channels', 'channels', true);
    }

    render() {
        return super.render();
    }
}