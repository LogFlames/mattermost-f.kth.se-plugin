import manifest from './manifest';

// import NotificationFreeCS from './components/admin_settings/notification_free_cs.jsx';
import ModeratorCS from './components/admin_settings/moderator_cs';

// import StandardCategoriesCS from './components/admin_settings/standard_categories_cs.jsx';
// import StandardChannelsCS from './components/admin_settings/standard_channels_cs.jsx';
// import WebsitePublisherBotCS1 from './components/admin_settings/website_publisher_bot_cs1.jsx';
// import WebsitePublisherBotCS2 from './components/admin_settings/website_publisher_bot_cs2.jsx';
// import WebsitePublisherBotCS3 from './components/admin_settings/website_publisher_bot_cs3.jsx';
// import TranslatorBotCS from './components/admin_settings/translator_bot_cs.jsx';

export default class Plugin {
    initialize(registry) {
        // registry.registerAdminConsoleCustomSetting('CustomAttributes', AbstractSettings);
        // registry.registerAdminConsoleCustomSetting('NotificationFree_Custom', NotificationFreeCS);
        registry.registerAdminConsoleCustomSetting('ModeratorBot_Custom', AbstractSettings);
        /*registry.registerAdminConsoleCustomSetting('StandardCategories_Custom', StandardCategoriesCS);
        registry.registerAdminConsoleCustomSetting('StandardChannels_Custom', StandardChannelsCS);
        registry.registerAdminConsoleCustomSetting('WebsitePublisherBot_Custom_ChannelList', WebsitePublisherBotCS1);
        registry.registerAdminConsoleCustomSetting('WebsitePublisherBot_Custom_NamndList', WebsitePublisherBotCS2);
        registry.registerAdminConsoleCustomSetting('WebsitePublisherBot_Custom_AdminList', WebsitePublisherBotCS3);
        registry.registerAdminConsoleCustomSetting('TranslatorBot_Custom', TranslatorBotCS);*/
    }
}

window.registerPlugin(manifest.id, new Plugin());
