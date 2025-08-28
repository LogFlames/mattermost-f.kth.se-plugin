import React from 'react';
import PropTypes from 'prop-types';

import AbstractEntry from './abstract_entry';
export default class AbstractAddEntry extends React.Component {
    static propTypes = {
        id: PropTypes.string,
        users: PropTypes.array,
        teams: PropTypes.array,
        groups: PropTypes.array,
        onChange: PropTypes.func.isRequired,
    };

    constructor(props) {
        super(props);

        this.state = {
            collapsed: true,
            is_active: this.props.is_active,
            string1: this.props.string1,
            string2: this.props.string2,
            string3: this.props.string3,
            string4: this.props.string4,
            string5: this.props.string5,
            bool: this.props.bool,
            int: this.props.int,
            users: this.props.users,
            team: this.props.team,
            teams: this.props.teams,
            groups: this.props.groups,
            channels: this.props.channels,
            error: false,
        };
    }

    handleCancel = () => {
        this.setState({
            collapsed: true,
            name: null,
            users: null,
            teams: null,
            groups: null,
            error: false,
        });
    };

    onInput = ({id, is_active, string1, string2, string3, string4, string5, bool, int, users, team, teams, groups, channels}) => {
        this.setState({id, is_active, string1, string2, string3, string4, string5, bool, int, users, team, teams, groups, channels, error: false});
    };

    handleSave = () => {
        this.props.onChange({id: this.props.id, is_active: this.state.is_active, string1: this.state.string1, string2: this.state.string2, string3: this.state.string3, string4: this.state.string4, string5: this.state.string5, bool: this.state.bool, int: this.state.int, users: this.state.users, team: this.state.team, teams: this.state.teams, groups: this.state.groups, channels: this.state.channels});
        this.setState({
            collapsed: true,
            is_active: null,
            string1: null,
            string2: null,
            string3: null,
            string4: null,
            string5: null,
            bool: null,
            int: null,
            users: null,
            team: null,
            teams: null,
            groups: null,
            channels: null,
        });
    };

    render() {
        if (this.state.collapsed) {
            return (
                <div>
                    <a
                        style={styles.addLink}
                        onClick={() => {
                            this.setState({collapsed: false});
                        }}
                    ><strong>{'+ Add Entry'}</strong></a>
                </div>
            );
        }

        let errorBanner = null;
        if (this.state.error) {
            errorBanner = (
                <div style={styles.alertDiv}>
                    <p style={styles.alertText}> {'Something is wrong with your input.'}
                    </p>
                </div>
            );
        }

        return (
            <div>
                <AbstractEntry
                    is_active={this.props.is_active}
                    string1={this.props.string1}
                    string2={this.props.string2}
                    string3={this.props.string3}
                    string4={this.props.string4}
                    string5={this.props.string5}
                    bool={this.props.bool}
                    int={this.props.int}
                    users={this.props.users}
                    team={this.props.team}
                    teams={this.props.teams}
                    groups={this.props.groups}
                    channels={this.props.channels}
                    onChange={this.onInput}
                    hideDelete={true}
                />

                <div
                    className='row'
                    style={styles.buttonRow}
                >
                    <div className='col-sm-12'>
                        <button
                            className='btn btn-primary'
                            style={styles.buttonBorder}
                            onClick={this.handleSave}
                        >
                            {'Add Entry'}
                        </button>
                        <button
                            className='btn btn-link'
                            onClick={this.handleCancel}
                        >
                            <a
                                style={styles.button}
                            >
                                {'Cancel'}
                            </a>
                        </button>
                    </div>
                </div>
                {errorBanner}
            </div>

        );
    }
}

const styles = {
    buttonRow: {
        marginTop: '8px',
    },
};
