import {connect} from 'react-redux';
import {bindActionCreators} from 'redux';

import {searchAllChannels as reduxSearchAllChannels} from 'mattermost-redux/actions/channels';

import ChannelsInput from './channels_input.jsx';

const searchChannels = (term, options = {}) => {
    if (!term) {
        return null // might cause error
    }
    return reduxSearchAllChannels(term, options);
};

function mapDispatchToProps(dispatch) {
    return {
        actions: bindActionCreators({
            searchChannels,
        }, dispatch),
    };
}

export default connect(null, mapDispatchToProps)(ChannelsInput);
