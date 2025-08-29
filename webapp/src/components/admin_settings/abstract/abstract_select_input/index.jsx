import {connect} from 'react-redux';
import {bindActionCreators} from 'redux';

import {getUser} from 'mattermost-redux/actions/users';
import {getTeam} from 'mattermost-redux/actions/teams';
import {getChannel} from 'mattermost-redux/actions/channels';

import AbstractSelectInput from './abstract_select_input.jsx';

function mapDispatchToProps(dispatch) {
    return {
        actions: bindActionCreators({
            getUser,
            getTeam,
            getChannel,
        }, dispatch),
    };
}

export default connect(null, mapDispatchToProps)(AbstractSelectInput);
