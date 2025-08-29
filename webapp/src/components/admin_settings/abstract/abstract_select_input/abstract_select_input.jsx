import PropTypes from 'prop-types';
import React from 'react';

import TeamsInput from '../../inputs/teams_input';
import ChannelsInput from '../../inputs/channels_input';
import UsersInput from '../../inputs/users_input';

export default class AbstractSelectInput extends React.Component {
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
        actions: PropTypes.shape({
            getUser: PropTypes.func.isRequired,
            getTeam: PropTypes.func.isRequired,
            getChannel: PropTypes.func.isRequired,
        }).isRequired,
    };

    constructor(props, placeholder, type, isMulti) {
        super(props);

        this.isMulti = isMulti;
        this.type = type;
        this.placeholder = placeholder;

        this.state = {
            value: [],
        }

        this.initValue(this.props.value).then((values) => {
            this.setState({value: values});
        });
    }

    async initValue(values) {
        if (!values) {
            return [];
        }

        if (!Array.isArray(values)) {
            values = [values];
        }

        var ret;
        if (this.type === "teams") {
            ret = await this.fetchTeams(values);
        } else if (this.type === "channels") {
            ret = await this.fetchChannels(values);
        } else if (this.type === "users") {
            ret = await this.fetchUsers(values);
        } else {
            return [];
        }

        return ret;
    }

    newValue = (newV) => {
        if (!newV) {
            this.setState({value: this.isMulti ? [] : null});
            this.props.onChange(this.props.id, this.isMulti ? [] : null);
            this.props.setSaveNeeded();
            return;
        }

        if (!Array.isArray(newV)) {
            newV = [newV];
        }

        this.setState({value: newV});
    
        if (this.type === "users") {
            newV = newV.map(user => user.id);
        } else if (this.type === "channels") {
            newV = newV.map(channel => channel.id);
        } else if (this.type === "teams") {
            newV = newV.map(team => team.id);
        }

        this.props.onChange(this.props.id, this.isMulti ? newV : newV[0]);
        this.props.setSaveNeeded();
    };

    async fetchTeams(teamIds) {
        const teams = await Promise.all(teamIds.filter(teamId => teamId).map(this.props.actions.getTeam));
        return teams.filter(team => !team.err).map(team => team.data);
    }

    async fetchChannels(channelIds) {
        const channels = await Promise.all(channelIds.filter(channelId => channelId).map(async (channelId) => {
            const {data: channelData, err: channelErr} = await this.props.actions.getChannel(channelId);
            if (channelErr) {
                return {err: channelErr};
            }

            const {data: teamData, err: teamErr} = await this.props.actions.getTeam(channelData.team_id);

            let team_display_name = '<Team Not Found>';
            if (!teamErr) {
                team_display_name = teamData.display_name;
            }

            return {...channelData, team_display_name};
        }));

        return channels;
    }

    async fetchUsers(userIds) {
        const users = await Promise.all(userIds.filter(userId => userId).map(this.props.actions.getUser));
        return users.filter(user => !user.err).map(user => user.data);
    }

    render() {
        return (
            <div>
                <strong>{this.title}</strong>
                <div>
                    {this.type === "teams" && 
                    <TeamsInput
                        placeholder={this.placeholder}
                        teams={this.state.value}
                        onChange={this.newValue}
                        isMulti={this.isMulti}
                    />}
                    {this.type === "channels" && 
                    <ChannelsInput
                        placeholder={this.placeholder}
                        channels={this.state.value}
                        onChange={this.newValue}
                        isMulti={this.isMulti}
                    />}
                    {this.type === "users" && 
                    <UsersInput
                        placeholder={this.placeholder}
                        users={this.state.value}
                        onChange={this.newValue}
                        isMulti={this.isMulti}
                    />}
                </div>
            </div>
        );
    }
}

const styles = {};
