import React from 'react';

import AbstractSettings from '../abstract/abstract_cs.jsx';
import ModeratorEntry from './moderator_entry/index.jsx';

export default class ModeratorSettings extends AbstractSettings {
    render() {
        const key = 0;
        const value = this.state.attributes.get(key) || {};
        return (
            <div>
                <strong>{'Moderator: Channels to Moderate (complain at messages without titles)'}</strong>
                <div>
                    <ModeratorEntry
                        key={key}
                        id={key}
                        is_active={value.IsActive}
                        string1={value.String1}
                        string2={value.String2}
                        string3={value.String3}
                        string4={value.String4}
                        string5={value.String5}
                        bool={value.Bool}
                        int={value.Int}
                        users={value.UserIDs}
                        team={value.TeamID}
                        teams={value.TeamIDs}
                        channels={value.ChannelIDs}
                        groups={value.GroupIDs ? value.GroupIDs.join(' ') : ''}
                        onChange={this.handleChange}
                        onDelete={this.triggerDeleteModal}
                    />
                </div>
            </div>
        );
    }
}

const styles = {
    alertDiv: {
        borderRadius: '4px',
        backgroundColor: 'rgba(0, 0, 0, .04)',
        padding: '12px',
        margin: '8px 0',
    },
    alertText: {
        opacity: '0.6',
    },
};
