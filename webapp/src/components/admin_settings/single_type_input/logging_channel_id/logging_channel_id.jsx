import AbstractSelectInput from "../../abstract/abstract_select_input/abstract_select_input.jsx";

export default class LoggingChannelId extends AbstractSelectInput {
    constructor(props) {
        super(props, 'Select logging channel', 'channels', false);
    }

    render() {
        return super.render();
    }
}