import React from 'react';
import PropTypes from 'prop-types';
import {connect} from 'react-redux';
import {bindActionCreators} from 'redux';

import {getUser} from 'mattermost-redux/actions/users';
import {getTeam} from 'mattermost-redux/actions/teams';
import {getChannel} from 'mattermost-redux/actions/channels';

import ChannelsInput from './inputs/channels_input';
import TeamsInput from './inputs/teams_input';
import UsersInput from './inputs/users_input';

const SELECT_TYPES = new Set(['channels', 'teams', 'users']);

const getId = (value) => value && typeof value === 'object' ? value.id : value;

const getError = (response) => response && (response.error || response.err);

const asArray = (value) => {
    if (value === null || value === undefined) {
        return [];
    }

    return Array.isArray(value) ? value : [value];
};

const hydrateValue = async (type, value, actions) => {
    const ids = asArray(value).filter(Boolean).map(getId);
    if (!ids.length) {
        return [];
    }

    if (type === 'users') {
        const responses = await Promise.all(ids.map(actions.getUser));
        return responses.filter((response) => !getError(response)).map((response) => response.data);
    }

    if (type === 'teams') {
        const responses = await Promise.all(ids.map(actions.getTeam));
        return responses.filter((response) => !getError(response)).map((response) => response.data);
    }

    const responses = await Promise.all(ids.map(async (channelId) => {
        const channelResponse = await actions.getChannel(channelId);
        if (getError(channelResponse)) {
            return channelResponse;
        }

        const teamResponse = await actions.getTeam(channelResponse.data.team_id);
        return {
            data: {
                ...channelResponse.data,
                team_display_name: getError(teamResponse) ? '<Team Not Found>' : teamResponse.data.display_name,
            },
        };
    }));

    return responses.filter((response) => !getError(response)).map((response) => response.data);
};

const SelectSetting = ({id, type, placeholder, isMulti, value, onChange, setSaveNeeded, actions}) => {
    const [selected, setSelected] = React.useState([]);

    React.useEffect(() => {
        let mounted = true;
        hydrateValue(type, value, actions).then((hydratedValue) => {
            if (mounted) {
                setSelected(hydratedValue);
            }
        });

        return () => {
            mounted = false;
        };
    }, [actions, type, value]);

    const handleChange = (nextValue) => {
        const nextSelected = asArray(nextValue);
        setSelected(nextSelected);
        const serializedValue = nextSelected.map(getId);
        onChange(id, isMulti ? serializedValue : serializedValue[0] || null);
        setSaveNeeded();
    };

    const inputProps = {
        isMulti,
        onChange: handleChange,
        placeholder,
    };

    if (type === 'channels') {
        return <ChannelsInput {...inputProps} channels={selected}/>;
    }

    if (type === 'teams') {
        return <TeamsInput {...inputProps} teams={selected}/>;
    }

    return <UsersInput {...inputProps} users={selected}/>;
};

SelectSetting.propTypes = {
    actions: PropTypes.object.isRequired,
    id: PropTypes.string.isRequired,
    isMulti: PropTypes.bool.isRequired,
    onChange: PropTypes.func.isRequired,
    placeholder: PropTypes.string.isRequired,
    setSaveNeeded: PropTypes.func.isRequired,
    type: PropTypes.oneOf(Array.from(SELECT_TYPES)).isRequired,
    value: PropTypes.any,
};

export default function createSelectSetting({type, placeholder, isMulti = false}) {
    if (!SELECT_TYPES.has(type)) {
        throw new Error(`Unsupported select setting type: ${type}`);
    }

    return connect(
        null,
        (dispatch) => ({
            actions: bindActionCreators({getUser, getTeam, getChannel}, dispatch),
        }),
    )(function ConnectedSelectSetting(props) {
        return <SelectSetting {...props} type={type} placeholder={placeholder} isMulti={isMulti}/>;
    });
}

export {SELECT_TYPES};