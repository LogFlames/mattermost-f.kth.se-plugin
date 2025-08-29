import {connect} from 'react-redux';
import {bindActionCreators} from 'redux';

import {getUser} from 'mattermost-redux/actions/users';
import {getTeam} from 'mattermost-redux/actions/teams';
import {getChannel} from 'mattermost-redux/actions/channels';
import JoinLeaveFreeBotUserId from './join_leave_free_bot_user_id';

function mapDispatchToProps(dispatch) {
    return {
        actions: bindActionCreators({
            getUser,
            getTeam,
            getChannel,
        }, dispatch),
    };
}

export default connect(null, mapDispatchToProps)(JoinLeaveFreeBotUserId);
