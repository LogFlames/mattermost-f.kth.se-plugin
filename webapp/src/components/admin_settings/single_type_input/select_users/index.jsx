import {connect} from 'react-redux';
import {bindActionCreators} from 'redux';

import {getUser} from 'mattermost-redux/actions/users';
import {getTeam} from 'mattermost-redux/actions/teams';
import {getChannel} from 'mattermost-redux/actions/channels';

import SelectUsers from './select_users.jsx';

function mapDispatchToProps(dispatch) {
    return {
        actions: bindActionCreators({
            getUser,
            getTeam,
            getChannel,
        }, dispatch),
    };
}

export default connect(null, mapDispatchToProps)(SelectUsers);
