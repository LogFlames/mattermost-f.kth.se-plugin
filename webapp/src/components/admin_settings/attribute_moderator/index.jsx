import {connect} from 'react-redux';
import {bindActionCreators} from 'redux';
import {getCustomEmojisInText} from 'mattermost-redux/actions/emojis';

import {getProfilesByIds} from 'mattermost-redux/actions/users';
import {getTeam, getTeams} from 'mattermost-redux/actions/teams';
import {getChannel} from 'mattermost-redux/actions/channels';

import AttributeModerator from './attribute_moderator.jsx';

function mapDispatchToProps(dispatch) {
    return {
        actions: bindActionCreators({
            getProfilesByIds,
            getTeam,
            getTeams,
            getChannel,
            getCustomEmojisInText,
        }, dispatch),
    };
}

export default connect(null, mapDispatchToProps)(AttributeModerator);
