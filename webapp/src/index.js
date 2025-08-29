import manifest from './manifest';

import DefaultChannelsSettings from './components/admin_settings/default_channels/default_channels_cs';
import ModeratorBotChannels from './components/admin_settings/single_type_input/moderator_bot_channels';

export default class Plugin {
    initialize(registry) {
        registry.registerAdminConsoleCustomSetting('DefaultChannels_Custom', DefaultChannelsSettings);
        registry.registerAdminConsoleCustomSetting('ModeratorBot_Channels', ModeratorBotChannels, {showTitle: true});
    }
}

window.registerPlugin(manifest.id, new Plugin());
