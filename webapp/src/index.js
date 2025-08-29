import manifest from './manifest';

import ModeratorBotChannels from './components/admin_settings/single_type_input/moderator_bot_channels';
import DefaultChannelsSettings from './components/admin_settings/default_channels/default_channels_cs';

export default class Plugin {
    initialize(registry) {
        registry.registerAdminConsoleCustomSetting('ModeratorBot_Channels', ModeratorBotChannels);
        registry.registerAdminConsoleCustomSetting('DefaultChannels_Custom', DefaultChannelsSettings);
    }
}

window.registerPlugin(manifest.id, new Plugin());
