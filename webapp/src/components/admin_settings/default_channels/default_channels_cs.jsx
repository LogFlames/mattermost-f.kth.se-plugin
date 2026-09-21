import React from 'react';
import PropTypes from 'prop-types';
import debounce from 'lodash/debounce';
import Client4 from 'mattermost-redux/client/client4';
import AsyncSelect from 'react-select/async';

import manifest from '../../../manifest';

export default class DefaultChannelsSettings extends React.PureComponent {
    static propTypes = {
        id: PropTypes.string.isRequired,
        config: PropTypes.object.isRequired,
        disabled: PropTypes.bool,
        setByEnv: PropTypes.bool,
        onChange: PropTypes.func.isRequired,
    };

    constructor(props) {
        super(props);
        this.client = new Client4();
        this.client.setUrl((props.config.ServiceSettings?.SiteURL || '').replace(/\/$/, ''));
        this.url = `${this.client.getUrl()}/plugins/${manifest.id}/default-channels`;
        this.state = {channels: [], enabled: false, busy: true, error: '', message: '', selected: null};
    }

    componentDidMount() {
        this.refresh();
    }

    componentWillUnmount() {
        this.search.cancel();
        this.unmounted = true;
    }

    request = async (change) => {
        this.setState({busy: true, error: '', message: ''});
        try {
            let message = '';
            if (change) {
                const saved = await this.client.doFetch(this.url, {method: 'post', body: JSON.stringify(change)});

                // Sync before fetching metadata: a refresh failure must not let
                // the enclosing console form save its pre-edit value later.
                this.props.onChange(this.props.id, saved.value);
                message = saved.message;
            }
            const result = await this.client.doFetch(this.url, {method: 'get'});
            if (!this.unmounted) {
                this.setState({channels: result.channels, enabled: result.enabled, message, selected: null});
            }
        } catch (error) {
            if (!this.unmounted) {
                this.setState({error: error.message || 'Could not load default channels. Refresh to check the saved state.'});
            }
        } finally {
            if (!this.unmounted) {
                this.setState({busy: false});
            }
        }
    };

    refresh = () => this.request();

    search = debounce((term, callback) => {
        if (!term.trim()) {
            callback([]);
            return;
        }
        const existing = new Set(this.state.channels.map((channel) => channel.id));
        this.client.searchAllChannels(term, {public: true, private: true, include_deleted: false}).then((channels) => {
            const available = [];
            for (const channel of channels) {
                if (channel.team_id && !channel.delete_at && channel.name !== 'town-square' && !existing.has(channel.id)) {
                    available.push(channel);
                }
            }
            callback(available);
        }).catch((error) => {
            if (!this.unmounted) {
                this.setState({error: `Channel search failed: ${error.message}`});
            }
            callback([]);
        });
    }, 200);

    renderChannel = (channel) => (
        <li
            key={channel.id}
            style={styles.channel}
        >
            <span style={styles.name}>
                {channel.display_name}
                <small style={styles.meta}>{` ~${channel.name}${channel.type === 'P' ? ' · Private' : ''}${channel.delete_at ? ' · Archived' : ''}`}</small>
            </span>
            <button
                type='button'
                className='btn btn-link'
                style={styles.button}
                aria-label={`Remove ${channel.display_name} as a default channel`}
                disabled={this.isDisabled()}
                onClick={() => this.request({channel_id: channel.id, action: 'unset'})}
            >
                {'Remove'}
            </button>
        </li>
    );

    isDisabled = () => Boolean(this.state.busy || !this.state.enabled || this.props.disabled || this.props.setByEnv);

    renderTeam = (team) => (
        <section
            key={team.id}
            style={styles.team}
            aria-label={`Team: ${team.name}`}
        >
            <h4 style={styles.teamHeading}>{team.name}</h4>
            {Array.from(team.categories).sort(([a], [b]) => a.localeCompare(b)).map(([category, channels]) => (
                <div
                    key={category}
                    style={styles.category}
                >
                    <strong>{category}</strong>
                    <ul style={styles.list}>{channels.sort((a, b) => a.display_name.localeCompare(b.display_name)).map(this.renderChannel)}</ul>
                </div>
            ))}
            {team.townSquare && (
                <p style={styles.builtIn}>
                    <strong>{'Default channel (town-square): '}</strong>
                    {`${team.townSquare.display_name} (category: ${team.townSquare.default_category_name || 'Channels'})`}
                    <small style={styles.meta}>{' · Managed by Mattermost'}</small>
                </p>
            )}
        </section>
    );

    render() {
        const teams = new Map();
        for (const channel of this.state.channels) {
            if (!teams.has(channel.team_id)) {
                teams.set(channel.team_id, {id: channel.team_id, name: channel.team_display_name, categories: new Map()});
            }
            const team = teams.get(channel.team_id);
            if (channel.name === 'town-square') {
                team.townSquare = channel;
            } else {
                const category = channel.default_category_name || 'Channels';
                if (!team.categories.has(category)) {
                    team.categories.set(category, []);
                }
                team.categories.get(category).push(channel);
            }
        }
        return (
            <div aria-busy={this.state.busy}>
                <div style={styles.heading}>
                    <strong>{'Default Channels'}</strong>
                    <button
                        type='button'
                        className='btn btn-link'
                        style={styles.button}
                        disabled={this.state.busy}
                        onClick={this.refresh}
                    >
                        {'Refresh'}
                    </button>
                </div>
                <p className='help-text'>
                    {'Default channels add all current and new team members. Add and Remove apply immediately; removing a default keeps its members. Categories come from each channel’s settings.'}
                </p>
                {!this.state.busy && !this.state.enabled && <p role='status'>{'Enable and save this module, then refresh to manage default channels.'}</p>}
                {this.state.error && <p role='alert'>{this.state.error}</p>}
                {this.state.message && <p role='status'>{this.state.message}</p>}
                {!this.state.busy && !this.state.error && teams.size === 0 && <p>{'No additional default channels configured. Town Square is managed by Mattermost.'}</p>}
                {Array.from(teams.values()).sort((a, b) => a.name.localeCompare(b.name)).map(this.renderTeam)}
                <label htmlFor='default-channel-search'>{'Add a default channel'}</label>
                <AsyncSelect
                    inputId='default-channel-search'
                    placeholder='Search channels across teams…'
                    loadOptions={this.search}
                    getOptionValue={(channel) => channel.id}
                    getOptionLabel={(channel) => `${channel.team_display_name} › ${channel.default_category_name || 'Channels'} › ${channel.display_name}${channel.type === 'P' ? ' (Private)' : ''}`}
                    value={this.state.selected}
                    onChange={(selected) => this.setState({selected})}
                    isDisabled={this.isDisabled()}
                    noOptionsMessage={() => 'Type to search for a channel that is not already a default.'}
                />
                <button
                    type='button'
                    className='btn btn-primary'
                    style={styles.add}
                    disabled={this.isDisabled() || !this.state.selected}
                    onClick={() => this.request({channel_id: this.state.selected.id, action: 'set'})}
                >
                    {'Add default channel'}
                </button>
            </div>
        );
    }
}

const styles = {
    heading: {display: 'flex', alignItems: 'center', justifyContent: 'space-between'},
    team: {border: '1px solid #ddd', borderRadius: 4, margin: '16px 0', overflowWrap: 'anywhere'},
    teamHeading: {margin: 0, padding: '12px 16px', background: 'rgba(0, 0, 0, .03)', borderBottom: '1px solid #ddd'},
    category: {margin: '16px 16px 12px', paddingLeft: 12, borderLeft: '2px solid #ddd'},
    list: {listStyle: 'none', padding: 0, margin: '4px 0 0'},
    channel: {display: 'flex', alignItems: 'center', justifyContent: 'space-between', gap: 8, padding: '4px 0'},
    name: {minWidth: 0},
    meta: {opacity: 0.7},
    builtIn: {margin: 0, padding: '12px 16px', borderTop: '1px solid #ddd'},
    button: {minHeight: 44, flexShrink: 0},
    add: {marginTop: 12, minHeight: 44},
};
