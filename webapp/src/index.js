import manifest from './manifest';

import DefaultChannelsSettings from './components/admin_settings/default_channels/default_channels_cs';
import ModeratorBotChannels from './components/admin_settings/single_type_input/moderator_bot_channels';
import JoinLeaveFreeBotUserId from './components/admin_settings/single_type_input/join_leave_free_bot_user_id';
import LoggingChannelId from './components/admin_settings/single_type_input/logging_channel_id';

export default class Plugin {
    initialize(registry) {
        registry.registerAdminConsoleCustomSetting('DefaultChannels_Custom', DefaultChannelsSettings);
        registry.registerAdminConsoleCustomSetting('ModeratorBot_Channels', ModeratorBotChannels, {showTitle: true});
        registry.registerAdminConsoleCustomSetting('JoinLeaveFree_BotUserId', JoinLeaveFreeBotUserId, {showTitle: true});
        registry.registerAdminConsoleCustomSetting('LoggingChannelId', LoggingChannelId, {showTitle: true});
    }
}

window.registerPlugin(manifest.id, new Plugin());
