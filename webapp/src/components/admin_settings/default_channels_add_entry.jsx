import AbstractAddEntry from './abstract_add_entry.jsx';

export default class DefaultChannelsAddEntry extends AbstractAddEntry {
    render() {
        return super.render(DefaultChannels);
    }
}

const styles = {
    buttonRow: {
        marginTop: '8px',
    },
};
