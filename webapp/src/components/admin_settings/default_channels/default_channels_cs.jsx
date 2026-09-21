import React from 'react';
import PropTypes from 'prop-types';
import debounce from 'lodash/debounce';
import Client4 from 'mattermost-redux/client/client4';
import {generateId} from 'mattermost-redux/utils/helpers';
import AsyncSelect from 'react-select/async';

import manifest from '../../../manifest';

export default class DefaultChannelsSettings extends React.PureComponent {
    static propTypes = {
        id: PropTypes.string.isRequired,
        config: PropTypes.object.isRequired,
        disabled: PropTypes.bool,
        setByEnv: PropTypes.bool,
        value: PropTypes.array,
        onChange: PropTypes.func.isRequired,
        registerSaveAction: PropTypes.func.isRequired,
        unRegisterSaveAction: PropTypes.func.isRequired,
    };

    constructor(props) {
        super(props);
        this.container = React.createRef();
        this.client = new Client4();
        this.client.setUrl((props.config.ServiceSettings?.SiteURL || '').replace(/\/$/, ''));
        this.url = `${this.client.getUrl()}/plugins/${manifest.id}/default-channels`;
        this.state = {channels: [], ids: [], savedIDs: [], enabled: false, busy: true, error: '', message: '', selected: null};
    }

    componentDidMount() {
        this.props.registerSaveAction(this.save);
        this.refresh();
    }

    componentWillUnmount() {
        this.props.unRegisterSaveAction(this.save);
        this.search.cancel();
        this.unmounted = true;
    }

    refresh = async () => {
        if (this.hasChanges()) {
            return;
        }
        this.setState({busy: true, error: '', message: ''});
        try {
            const result = await this.client.doFetch(this.url, {method: 'get'});
            if (!this.unmounted) {
                this.setState({channels: result.channels, ids: result.value, savedIDs: result.value, enabled: result.enabled, selected: null});

                // Keep the enclosing form in sync with the saved channel IDs,
                // including defaults changed by commands since the form opened.
                if (!this.props.disabled && !this.props.setByEnv && JSON.stringify(this.props.value || []) !== JSON.stringify(result.value)) {
                    this.props.onChange(this.props.id, result.value);
                }
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

    hasChanges = () => JSON.stringify(this.state.ids) !== JSON.stringify(this.state.savedIDs);

    stage = (channel, add) => {
        this.saveID = generateId();
        const ids = add ? [...this.state.ids, channel.id] : this.state.ids.filter((id) => id !== channel.id);
        const channels = this.state.channels.filter((entry) => entry.id !== channel.id);
        channels.push(channel);
        this.setState({ids, channels, selected: null, error: '', message: ''});
        this.props.onChange(this.props.id, ids);
    };

    // Mattermost calls this after saving the enclosing config form. The endpoint
    // verifies that save succeeded before queueing any membership changes.
    save = async () => {
        if (!this.hasChanges()) {
            return {};
        }
        const ids = this.state.ids;
        const added = ids.filter((id) => !this.state.savedIDs.includes(id));
        this.setState({busy: true, error: '', message: ''});
        try {
            await this.client.doFetch(this.url, {method: 'post', body: JSON.stringify({save_id: this.saveID, expected_channel_ids: ids, added_channel_ids: added})});
            if (!this.unmounted) {
                this.setState({savedIDs: ids, message: 'Default channels saved. New defaults will add current team members in the background.'});
            }
            return {};
        } catch (error) {
            const message = error.message || 'Could not finish saving default channels. Press Save to retry.';
            if (!this.unmounted) {
                this.setState({error: message});
            }
            return {error: {message}};
        } finally {
            if (!this.unmounted) {
                this.setState({busy: false});
            }
        }
    };

    search = debounce((term, callback) => {
        if (!term.trim()) {
            callback([]);
            return;
        }
        const existing = new Set(this.state.ids);
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
                onClick={() => this.stage(channel, false)}
            >
                {'Remove'}
            </button>
        </li>
    );

    isDisabled = () => Boolean(this.state.busy || !this.state.enabled || this.props.disabled || this.props.setByEnv);

    renderTeam = (team) => (
        <details
            key={team.id}
            style={styles.team}
            aria-label={`Team: ${team.name}`}
        >
            <summary
                style={styles.teamHeading}
                className='fkth-default-channel-team-heading'
            >
                {team.name}
            </summary>
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
                </p>
            )}
        </details>
    );

    render() {
        const teams = new Map();
        for (const channel of this.state.channels) {
            if (channel.name !== 'town-square' && !this.state.ids.includes(channel.id)) {
                continue;
            }
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
            <div
                ref={this.container}
                aria-busy={this.state.busy}
            >
                <style>
                    {'.fkth-default-channel-team-heading::marker {content: "";} .fkth-default-channel-team-heading::-webkit-details-marker {display: none;}'}
                </style>
                <div style={styles.heading}>
                    <strong>{'Default Channels'}</strong>
                    <button
                        type='button'
                        className='btn btn-link'
                        style={styles.button}
                        disabled={this.state.busy || this.hasChanges()}
                        onClick={this.refresh}
                    >
                        {'Refresh'}
                    </button>
                </div>
                <p className='help-text'>
                    {'Default channels add all current and new team members. Press Save to apply additions and removals. Removing a default keeps its members. Categories come from each channel’s settings.'}
                </p>
                {!this.state.busy && !this.state.enabled && (
                    <p
                        role='status'
                        style={styles.notice}
                    >
                        {'Enable and save this module, then refresh to manage default channels.'}
                    </p>
                )}
                {this.hasChanges() && (
                    <p
                        role='status'
                        style={styles.notice}
                    >
                        {'Unsaved changes. Press Save to apply.'}
                    </p>
                )}
                {this.state.error && (
                    <p
                        role='alert'
                        style={{...styles.notice, ...styles.error}}
                    >
                        {this.state.error}
                    </p>
                )}
                {this.state.message && (
                    <p
                        role='status'
                        style={styles.notice}
                    >
                        {this.state.message}
                    </p>
                )}
                {!this.state.busy && !this.state.error && teams.size === 0 && <p>{'No additional default channels configured. Town Square is managed by Mattermost.'}</p>}
                {Array.from(teams.values()).sort((a, b) => a.name.localeCompare(b.name)).map(this.renderTeam)}
                <label htmlFor='default-channel-search'>{'Add a default channel'}</label>
                <AsyncSelect
                    inputId='default-channel-search'
                    placeholder='Search channels across teams…'
                    menuPortalTarget={document.body}
                    menuPosition='fixed'
                    menuPlacement='top'
                    maxMenuHeight={240}
                    menuShouldScrollIntoView={false}
                    closeMenuOnScroll={(event) => event.target.contains(this.container.current)}
                    styles={selectStyles}
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
                    onClick={() => this.stage(this.state.selected, true)}
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
    teamHeading: {display: 'block', margin: 0, padding: '12px 16px 12px 28px', background: 'rgba(0, 0, 0, .03)', fontWeight: 600, cursor: 'pointer'},
    notice: {margin: '20px 0', padding: '14px 16px', border: '1px solid rgba(22, 109, 224, .3)', borderRadius: 4, background: 'rgba(22, 109, 224, .06)'},
    error: {borderColor: 'rgba(194, 48, 48, .4)', background: 'rgba(194, 48, 48, .06)'},
    category: {margin: '16px 16px 12px', paddingLeft: 12, borderLeft: '2px solid #ddd'},
    list: {listStyle: 'none', padding: 0, margin: '4px 0 0'},
    channel: {display: 'flex', alignItems: 'center', justifyContent: 'space-between', gap: 8, padding: '4px 0'},
    name: {minWidth: 0},
    meta: {opacity: 0.7},
    builtIn: {margin: 0, padding: '12px 16px', borderTop: '1px solid #ddd'},
    button: {minHeight: 44, flexShrink: 0},
    add: {marginTop: 12, minHeight: 44},
};

const selectStyles = {
    menuPortal: (base) => ({...base, zIndex: 9999}),
};
