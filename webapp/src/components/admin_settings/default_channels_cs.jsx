import React from 'react';

import ConfirmModal from '../widgets/confirmation_modal.tsx';

import AbstractSettings from './abstract_cs.jsx';

import DefaultChannels from './default_channels_entry/default_channels_entry.jsx';
import DefaultChannelsAddEntry from './default_channels_add_entry.jsx';

export default class DefaultChannelsSettings extends AbstractSettings {
    getAttributesList() {
        if (this.state.attributes.size === 0) {
            return (
                <div style={styles.alertDiv}>
                    <div style={styles.alertText}>{'You have no entries for this setting yet.'}</div>
                </div>
            );
        }

        return Array.from(this.state.attributes, ([key, value]) => {
            return (
                <DefaultChannels
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
            );
        });
    }

    render() {
        return (
            <div>
                <strong>{'Default Channels: Default Channels and Categories'}</strong>
                <div>
                    {this.getAttributesList()}
                    <DefaultChannelsAddEntry
                        onChange={this.handleChange}
                        id={this.state.attributes.size}
                    />
                </div>
                <ConfirmModal
                    show={this.state.showDeleteModal}
                    title={'Delete Entry'}
                    message={
                        'Are you sure you want to remove the entry?'
                    }
                    confirmButtonText={'Delete entry'}
                    onConfirm={() => {
                        this.handleDelete(this.state.deleteModalData.id);
                    }}
                    onCancel={() => this.setState({showDeleteModal: false})}
                />
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
