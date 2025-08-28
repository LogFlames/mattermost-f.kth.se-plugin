import React from 'react';
import PropTypes from 'prop-types';

import UsersInput from '../inputs/users_input';
import TeamsInput from '../inputs/teams_input';
import ChannelsInput from '../inputs/channels_input';

export default class AbstractEntry extends React.Component {
    static propTypes = {
        id: PropTypes.string.isRequired,
        is_active: PropTypes.bool,
        string1: PropTypes.string,
        string2: PropTypes.string,
        string3: PropTypes.string,
        string4: PropTypes.string,
        string5: PropTypes.string,
        bool: PropTypes.bool,
        int: PropTypes.number,
        users: PropTypes.array,
        team: PropTypes.string,
        teams: PropTypes.array,
        groups: PropTypes.array,
        channels: PropTypes.array,
        hideDelete: PropTypes.bool,
        onDelete: PropTypes.func,
        onChange: PropTypes.func.isRequired,
        actions: PropTypes.shape({
            getProfilesByIds: PropTypes.func.isRequired,
            getTeam: PropTypes.func.isRequired,
            getTeams: PropTypes.func.isRequired,
            getChannel: PropTypes.func.isRequired,
            getCustomEmojisInText: PropTypes.func.isRequired,
        }).isRequired,
    };

    constructor(props) {
        super(props);

        this.state = {
            is_active: this.props.is_active,
            string1: this.props.string1,
            string2: this.props.string2,
            string3: this.props.string3,
            string4: this.props.string4,
            string5: this.props.string5,
            bool: this.props.bool,
            int: this.props.int,
            groups: this.props.groups,
            error: null,
        };

        this.initUsers();
        this.initTeam();
        this.initTeams();
        this.initChannels();
    }

    // initUsers fetches user profiles for the users ids passed in props
    async initUsers() {
        if (!this.props.users || !this.props.users.length) {
            return;
        }

        const profiles = await this.props.actions.getProfilesByIds(this.props.users);

        let users = profiles.data;

        if (users.length !== this.props.users.length) {
            // Check if all ids were returned.
            // mattermost-redux removes the current admin user from the result at:
            // https://github.com/mattermost/mattermost-redux/blob/5f5a8a5007661f6d54533c2b51299748338b5a65/src/actions/users.ts#L340
            const unknownIds = this.props.users.filter((userId) =>
                !users.find((user) => user.id === userId),
            );

            // Add the unknown ids directly to display on the input
            users = users.concat(unknownIds);
        }

        this.setState({users});
    }

    // initTeam fetches team informations for the team id passed in props
    async initTeam() {
        if (!this.props.team) {
            return;
        }

        const team = await this.props.actions.getTeam(this.props.team);

        this.setState({team: team.data});
    }

    // initTeams fetches teams informations for the teams ids passed in props
    async initTeams() {
        if (!this.props.teams || !this.props.teams.length) {
            return;
        }

        const teamPromises = this.props.teams.map(this.props.actions.getTeam);
        const responses = await Promise.all(teamPromises);
        const teams = responses.filter((res) => !res.error).map((res) => res.data);

        this.setState({teams});
    }

    // initChannels fetches channel informations for the channels ids passed in props
    async initChannels() {
        if (!this.props.channels || !this.props.channels.length) {
            return;
        }

        const channelPromises = this.props.channels.map(this.props.actions.getChannel);
        const teamPromise = this.props.actions.getTeams(0, 60);
        const channelResponses = await Promise.all(channelPromises);
        const teamResponse = await teamPromise;
        const channelsData = channelResponses.filter((res) => !res.error).map((res) => res.data);
        const teamsData = teamResponse.data;
        
        if (teamResponse.error) {
            // eslint-disable-next-line no-console
            console.error('Error getting team for channel in setting. ' + teamResponse.error);
            callback([]);
            return;
        }

        const channels = channelsData.map((channel) => {
            return {
                ...channel,
                // eslint-disable-next-line max-nested-callbacks
                team_display_name: teamsData.find((team) => team.id === channel.team_id).display_name,
            };
        });

        this.setState({channels});
    }

    handleIsActiveInput = (e) => {
        this.setState({is_active: e.target.checked});
        this.props.onChange({id: this.props.id, is_active: e.target.checked, string1: this.state.string1, string2: this.state.string2, string3: this.state.string3, string4: this.state.string4, string5: this.state.string5, bool: this.state.bool, int: this.state.int, users: this.state.users, team: this.state.team, teams: this.state.teams, groups: this.state.groups, channels: this.state.channels});
    };

    handleString1Input = (e) => {
        this.setState({string1: e.target.value});
        this.props.onChange({id: this.props.id, is_active: this.state.is_active, string1: e.target.value, string2: this.state.string2, string3: this.state.string3, string4: this.state.string4, string5: this.state.string5, bool: this.state.bool, int: this.state.int, users: this.state.users, team: this.state.team, teams: this.state.teams, groups: this.state.groups, channels: this.state.channels});
    };

    handleString2Input = (e) => {
        this.setState({string2: e.target.value});
        this.props.onChange({id: this.props.id, is_active: this.state.is_active, string1: this.state.string1, string2: e.target.value, string3: this.state.string3, string4: this.state.string4, string5: this.state.string5, bool: this.state.bool, int: this.state.int, users: this.state.users, team: this.state.team, teams: this.state.teams, groups: this.state.groups, channels: this.state.channels});
    };

    handleString3Input = (e) => {
        this.setState({string3: e.target.value});
        this.props.onChange({id: this.props.id, is_active: this.state.is_active, string1: this.state.string1, string2: this.state.string2, string3: e.target.value, string4: this.state.string4, string5: this.state.string5, bool: this.state.bool, int: this.state.int, users: this.state.users, team: this.state.team, teams: this.state.teams, groups: this.state.groups, channels: this.state.channels});
    };

    handleString4Input = (e) => {
        this.setState({string4: e.target.value});
        this.props.onChange({id: this.props.id, is_active: this.state.is_active, string1: this.state.string1, string2: this.state.string2, string3: this.state.string3, string4: e.target.value, string5: this.state.string5, bool: this.state.bool, int: this.state.int, users: this.state.users, team: this.state.team, teams: this.state.teams, groups: this.state.groups, channels: this.state.channels});
    };

    handleString5Input = (e) => {
        this.setState({string5: e.target.value});
        this.props.onChange({id: this.props.id, is_active: this.state.is_active, string1: this.state.string1, string2: this.state.string2, string3: this.state.string3, string4: this.state.string4, string5: e.target.value, bool: this.state.bool, int: this.state.int, users: this.state.users, team: this.state.team, teams: this.state.teams, groups: this.state.groups, channels: this.state.channels});
    };

    handleBoolInput = (e) => {
        this.setState({bool: e.target.checked});
        this.props.onChange({id: this.props.id, is_active: this.state.is_active, string1: this.state.string1, string2: this.state.string2, string3: this.state.string3, string4: this.state.string4, string5: this.state.string5, bool: e.target.checked, int: this.state.int, users: this.state.users, team: this.state.team, teams: this.state.teams, groups: this.state.groups, channels: this.state.channels});
    };

    handleIntInput = (e) => {
        this.setState({int: parseInt(e.target.value, 10)});
        this.props.onChange({id: this.props.id, is_active: this.state.is_active, string1: this.state.string1, string2: this.state.string2, string3: this.state.string3, string4: this.state.string4, string5: this.state.string5, bool: this.state.bool, int: parseInt(e.target.value, 10), users: this.state.users, team: this.state.team, teams: this.state.teams, groups: this.state.groups, channels: this.state.channels});
    };

    handleUsersInput = (userIds) => {
        this.setState({users: userIds});
        this.props.onChange({id: this.props.id, is_active: this.state.is_active, string1: this.state.string1, string2: this.state.string2, string3: this.state.string3, string4: this.state.string4, string5: this.state.string5, bool: this.state.bool, int: this.state.int, users: userIds, team: this.state.team, teams: this.state.teams, groups: this.state.groups, channels: this.state.channels});
    };

    handleTeamInput = (teamId) => {
        this.setState({team: teamId});
        this.props.onChange({id: this.props.id, is_active: this.state.is_active, string1: this.state.string1, string2: this.state.string2, string3: this.state.string3, string4: this.state.string4, string5: this.state.string5, bool: this.state.bool, int: this.state.int, users: this.state.users, team: teamId, teams: this.state.teams, groups: this.state.groups, channels: this.state.channels});
    };

    handleTeamsInput = (teamsIds) => {
        this.setState({teams: teamsIds});
        this.props.onChange({id: this.props.id, is_active: this.state.is_active, string1: this.state.string1, string2: this.state.string2, string3: this.state.string3, string4: this.state.string4, string5: this.state.string5, bool: this.state.bool, int: this.state.int, users: this.state.users, team: this.state.team, teams: teamsIds, groups: this.state.groups, channels: this.state.channels});
    };

    handleGroupsInput = (e) => {
        this.setState({groups: e.target.value});
        this.props.onChange({id: this.props.id, is_active: this.state.is_active, string1: this.state.string1, string2: this.state.string2, string3: this.state.string3, string4: this.state.string4, string5: this.state.string5, bool: this.state.bool, int: this.state.int, users: this.state.users, team: this.state.team, teams: this.state.teams, groups: e.target.value, channels: this.state.channels});
    };

    handleChannelsInput = (channelsIds) => {
        this.setState({channels: channelsIds});
        this.props.onChange({id: this.props.id, is_active: this.state.is_active, string1: this.state.string1, string2: this.state.string2, string3: this.state.string3, string4: this.state.string4, string5: this.state.string5, bool: this.state.bool, int: this.state.int, users: this.state.users, team: this.state.team, teams: this.state.teams, groups: this.state.groups, channels: channelsIds});
    };

    handleDelete = () => {
        this.props.onDelete(this.props.id);
    };

    render({
        enableBool, 
        enableInt, 
        intPlaceholder, 
        enableString1, 
        string1Placeholder, 
        enableString2, 
        string2Placeholder, 
        enableString3, 
        string3Placeholder, 
        enableString4, 
        string4Placeholder, 
        enableString5, 
        string5Placeholder, 
        enableUsers, 
        enableTeam, 
        enableTeams, 
        enableGroups, 
        enableChannels
    }={}) {
        let deleteButton = null;
        if (!this.props.hideDelete) {
            deleteButton = (
                <div
                    className='col-xs-12 col-sm-1'
                >
                    <a
                        style={styles.deleteIcon}
                        onClick={this.handleDelete}
                    >
                        <i className='fa fa-trash'/>
                    </a>
                </div>
            );
        }

        let errorLabel = null;
        if (this.state.error) {
            errorLabel = this.state.error;
        }

        return (
            <div>
            <div style={styles.attributeRow}>
            <div className='row'></div>
                {enableString1 && (
                <div className='col-xs-12 col-sm-3'>
                    <input
                    id={`string1-${this.props.id}`}
                    className='form-control'
                    type='text'
                    placeholder={string1Placeholder || 'string1'}
                    value={this.state.string1}
                    onChange={this.handleString1Input}
                    />
                </div>
                )}
                {enableString2 && (
                <div className='col-xs-12 col-sm-3'>
                    <input
                    id={`string2-${this.props.id}`}
                    className='form-control'
                    type='text'
                    placeholder={string2Placeholder || 'String2'}
                    value={this.state.string2}
                    onChange={this.handleString2Input}
                    />
                </div>
                )}
                {enableString3 && (
                <div className='col-xs-12 col-sm-3'>
                    <input
                    id={`string3-${this.props.id}`}
                    className='form-control'
                    type='text'
                    placeholder={string3Placeholder || 'String3'}
                    value={this.state.string3}
                    onChange={this.handleString3Input}
                    />
                </div>
                )}
                {enableString4 && (
                <div className='col-xs-12 col-sm-3'>
                    <input
                    id={`string4-${this.props.id}`}
                    className='form-control'
                    type='text'
                    placeholder={string4Placeholder || 'String4'}
                    value={this.state.string4}
                    onChange={this.handleString4Input}
                    />
                </div>
                )}
                {enableString5 && (
                <div className='col-xs-12 col-sm-3'>
                    <input
                    id={`string5-${this.props.id}`}
                    className='form-control'
                    type='text'
                    placeholder={string5Placeholder || 'String5'}
                    value={this.state.string5}
                    onChange={this.handleString5Input}
                    />
                </div>
                )}
                {enableBool && (
                <div className='col-xs-12 col-sm-2'>
                    <label style={{marginRight: '8px'}}>
                    <input
                        id={`bool-${this.props.id}`}
                        type='checkbox'
                        checked={!!this.state.bool}
                        onChange={this.handleBoolInput}
                        style={{marginRight: '4px'}}
                    />
                    </label>
                </div>
                )}
                {enableInt && (
                <div className='col-xs-12 col-sm-2'>
                    <input
                    id={`int-${this.props.id}`}
                    className='form-control'
                    type='number'
                    placeholder={intPlaceholder || 'Int'}
                    value={this.state.int || ''}
                    onChange={this.handleIntInput}
                    />
                </div>
                )}
                {enableUsers && (
                <div className='col-xs-12 col-sm-3'>
                    <UsersInput
                    placeholder='@username1 @username2'
                    users={this.state.users}
                    onChange={this.handleUsersInput}
                    />
                </div>
                )}
                {enableTeam && (
                <div className='col-xs-12 col-sm-3'>
                    <TeamsInput
                    placeholder='teamName'
                    teams={this.state.team ? [this.state.team] : []}
                    onChange={([team]) => this.handleTeamsInput([team])}
                    isMulti={false}
                    />
                </div>
                )}
                {enableTeams && (
                <div className='col-xs-12 col-sm-3'>
                    <TeamsInput
                    placeholder='teamName1 teamName2'
                    teams={this.state.teams}
                    onChange={this.handleTeamsInput}
                    />
                </div>
                )}
                {enableChannels && (
                <div className='col-xs-12 col-sm-3'>
                    <ChannelsInput
                    placeholder='~channel1 ~channel2'
                    channels={this.state.channels}
                    onChange={this.handleChannelsInput}
                    />
                </div>
                )}
                {enableGroups && (
                <div className='col-xs-12 col-sm-3'>
                    <input
                    id={`groups-${this.props.id}`}
                    className='form-control'
                    type='text'
                    placeholder='GroupID1 GroupID2'
                    value={this.state.groups}
                    onChange={this.handleGroupsInput}
                    />
                </div>
                )}
                {deleteButton}
            </div>
                <div style={styles.errorLabel}>
                    {errorLabel}
                </div>
            </div>
        );   
    }
}

const styles = {
    attributeRow: {
        margin: '12px 0',
        borderBottom: '1px solid #ccc',
        padding: '4px 0 12px',
    },
    deleteIcon: {
        textDecoration: 'none',
        height: '20px',
        width: '24px',
        color: '#DB1C34',
        fontFamily: 'material',
        fontSize: '20px',
        lineHeight: '32px',
        margin: '0 0 0 -12px',
    },
    errorLabel: {
        margin: '8px 0 0',
        color: '#EB5757',
    },
};
