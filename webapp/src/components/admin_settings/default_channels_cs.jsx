import AbstractSettings from './abstract_cs.jsx';

import DefaultChannels from './default_channels_entry/default_channels_entry.jsx';
import DefaultChannelsAddEntry from './default_channels_add_entry.jsx';

export default class DefaultChannelsSettings extends AbstractSettings {
    getAttributesList() {
        return super.getAttributesList(EntryType=DefaultChannels);
    }

    render() {
        return super.render(AddEntryType=DefaultChannelsAddEntry, Title='Default Channels: Default Channels and Categories');
    }
}

const styles = {
    alertDiv: {
        borderRadius: '4px',
        backgroundColor: 'rgba(0, 0, 0, .04)',
        padding: '12px',
        margin: '8px 0',
    },
    alertText: {
        opacity: '0.6',
    },
};
