// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import PropTypes from 'prop-types';
import React from 'react';

import ConfirmModal from '../widgets/confirmation_modal.tsx';

import AddAttribute from './abstract_add_attribute.jsx';
import AbstractAttribute from './abstract_attribute/abstract_attribute.jsx';

AttributeClass = AbstractAttribute;
AddAttributeClass = AddAttribute;

export default class AbstractSettings extends React.Component {
    static propTypes = {
        id: PropTypes.string.isRequired,
        label: PropTypes.string.isRequired,
        helpText: PropTypes.node,
        value: PropTypes.any,
        disabled: PropTypes.bool.isRequired,
        config: PropTypes.object.isRequired,
        currentState: PropTypes.object.isRequired,
        license: PropTypes.object.isRequired,
        setByEnv: PropTypes.bool.isRequired,
        onChange: PropTypes.func.isRequired,
        registerSaveAction: PropTypes.func.isRequired,
        setSaveNeeded: PropTypes.func.isRequired,
        unRegisterSaveAction: PropTypes.func.isRequired,
    };

    constructor(props) {
        super(props);

        this.state = {
            attributes: this.initAttributes(props.value),
            showDeleteModal: false,
            deleteModalData: {},
        };
    }

    initAttributes(attributes) {
        if (!attributes) {
            return new Map();
        }

        // Store the attributes in a map indexed by position
        return new Map(attributes.map((a, index) => [index, a]));
    }

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
                <AttributeClass
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

    triggerDeleteModal = (id) => {
        this.setState({showDeleteModal: true, deleteModalData: {id, Name: Array.from(this.state.attributes.values())[id].Name}});
    };

    handleDelete = (id) => {
        this.state.attributes.delete(id);
        this.props.onChange(this.props.id, Array.from(this.state.attributes.values()));
        this.props.setSaveNeeded();
        this.setState({showDeleteModal: false});
    };

    handleChange = ({id, users, team, teams, channels, groups}) => {
        let userIds = [];
        if (users) {
            userIds = users.map((v) => {
                if (v.id) {
                    return v.id;
                }

                return v;
            });
        }
        
        let teamId = '';
        if (team) {
            teamId = team.id;
        }

        let teamIds = [];
        if (teams) {
            teamIds = teams.map((team) => {
                if (team.id) {
                    return team.id;
                }
                return team;
            });
        }

        let channelIds = [];
        if (channels) {
            channelIds = channels.map((channel) => {
                if (channel.id) {
                    return channel.id;
                }
                return channel;
            });
        }

        this.state.attributes.set(id, {
            UserIDs: userIds,
            TeamID: teamId,
            TeamIDs: teamIds,
            ChannelIDs: channelIds,
            GroupIDs: groups ? groups.split(' ') : [],
        });

        this.props.onChange(this.props.id, Array.from(this.state.attributes.values()));
        this.props.setSaveNeeded();
    };

    render() {
        return (
            <div>
                <strong>{'Entries'}</strong>
                <div>
                    {this.getAttributesList()}
                    <AddAttributeClass
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
