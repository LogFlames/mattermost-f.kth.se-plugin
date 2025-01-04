import {connect} from 'react-redux';

import mapDispatchToProps from '../abstract_attribute/index.jsx';
import AttributeModerator from './attribute_moderator';

export default connect(null, mapDispatchToProps)(AttributeModerator);
