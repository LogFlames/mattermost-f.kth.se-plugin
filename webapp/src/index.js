import manifest from './manifest';

import DefaultChannelsSettings from './components/admin_settings/default_channels/default_channels_cs';
import SelectChannels from './components/admin_settings/single_type_input/select_channels';
import SelectTeams from './components/admin_settings/single_type_input/select_teams';
import JoinLeaveFreeBotUserId from './components/admin_settings/single_type_input/join_leave_free_bot_user_id';
import LoggingChannelId from './components/admin_settings/single_type_input/logging_channel_id';
import SelectUsers from './components/admin_settings/single_type_input/select_users';
import StatsPage from './components/stats/stats_page';

export default class Plugin {
    initialize(registry) {
        registry.registerCustomRoute('/stats', StatsPage);
        registry.registerAdminConsoleCustomSetting('DefaultChannels_Custom', DefaultChannelsSettings);
        registry.registerAdminConsoleCustomSetting('ModeratorBot_Channels', SelectChannels, {showTitle: true});
        registry.registerAdminConsoleCustomSetting('JoinLeaveFree_BotUserId', JoinLeaveFreeBotUserId, {showTitle: true});
        registry.registerAdminConsoleCustomSetting('LoggingChannelId', LoggingChannelId, {showTitle: true});
        registry.registerAdminConsoleCustomSetting('InternationalStudents_Users', SelectUsers, {showTitle: true});
        registry.registerAdminConsoleCustomSetting('InternationalStudents_DefaultSwedishTeams', SelectTeams, {showTitle: true});
        registry.registerAdminConsoleCustomSetting('InternationalStudents_DefaultEnglishTeams', SelectTeams, {showTitle: true});
        registry.registerAdminConsoleCustomSetting('InternationalStudents_DefaultChannels', SelectChannels, {showTitle: true});
        registry.registerAdminConsoleCustomSetting('InternationalStudents_EnglishChannels', SelectChannels, {showTitle: true});
    }
}

window.registerPlugin(manifest.id, new Plugin());
