import AbstractAddEntry from './abstract_add_entry.jsx';
import DefaultChannelsEntry from './default_channels_entry/default_channels_entry.jsx';

export default class DefaultChannelsAddEntry extends AbstractAddEntry {
    render() {
        return super.render(DefaultChannelsEntry);
    }
}

const styles = {
    buttonRow: {
        marginTop: '8px',
    },
};
