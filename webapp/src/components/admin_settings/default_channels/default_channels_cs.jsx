import AbstractSettings from '../abstract/abstract_cs.jsx';

import DefaultChannelsEntry from './default_channels_entry';
import DefaultChannelsAddEntry from './default_channels_add_entry.jsx';

export default class DefaultChannelsSettings extends AbstractSettings {
    getAttributesList() {
        return super.getAttributesList(DefaultChannelsEntry);
    }

    render() {
        return (
            <div>
                {super.render(DefaultChannelsAddEntry, 'Default Channels')}
                <p className='help-text'>
                    {'Selected channels are defaults for their own teams. Set categories in each channel\'s settings.'}
                </p>
            </div>
        );
    }
}
