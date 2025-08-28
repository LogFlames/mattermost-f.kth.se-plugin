import manifest from './manifest';

import AbstractSettings from './components/admin_settings/abstract_cs.jsx';
import ModeratorSettings from './components/admin_settings/moderator_cs';
import DefaultChannelsSettings from './components/admin_settings/default_channels_cs';

export default class Plugin {
    initialize(registry) {
        registry.registerAdminConsoleCustomSetting('ModeratorBot_Custom', ModeratorSettings);
        registry.registerAdminConsoleCustomSetting('DefaultChannels_Custom', DefaultChannelsSettings);
    }
}

window.registerPlugin(manifest.id, new Plugin());
