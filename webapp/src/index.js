import manifest from './manifest';

import ModeratorSettings from './components/admin_settings/moderator/moderator_cs';
import DefaultChannelsSettings from './components/admin_settings/default_channels/default_channels_cs';

export default class Plugin {
    initialize(registry) {
        registry.registerAdminConsoleCustomSetting('ModeratorBot_Custom', ModeratorSettings);
        registry.registerAdminConsoleCustomSetting('DefaultChannels_Custom', DefaultChannelsSettings);
    }
}

window.registerPlugin(manifest.id, new Plugin());
