import React from 'react';
import PropTypes from 'prop-types';
import Client4 from 'mattermost-redux/client/client4';

import manifest from '../../manifest';

function formatUTC(value) {
    const date = new Date(value);
    if (Number.isNaN(date.getTime())) {
        return 'Invalid timestamp';
    }
    return date.toISOString().replace('T', ' ').replace('.000Z', ' UTC').replace('Z', ' UTC');
}

export default class GoogleGroupsSyncOverview extends React.PureComponent {
    static propTypes = {
        config: PropTypes.object.isRequired,
    };

    constructor(props) {
        super(props);
        this.client = new Client4();
        this.client.setUrl((props.config.ServiceSettings?.SiteURL || '').replace(/\/$/, ''));
        this.url = `${this.client.getUrl()}/plugins/${manifest.id}/google-groups/syncs`;
        this.state = {syncs: [], cycle: '', busy: true, error: ''};
    }

    componentDidMount() {
        this.refresh();
    }

    componentWillUnmount() {
        this.unmounted = true;
    }

    refresh = async () => {
        this.setState({busy: true, error: ''});
        try {
            const result = await this.client.doFetch(this.url, {method: 'get'});
            if (!Array.isArray(result.syncs) || typeof result.cycle !== 'string' || !result.cycle) {
                throw new Error('Invalid Google Group sync response. Please retry.');
            }
            if (!this.unmounted) {
                this.setState({syncs: result.syncs, cycle: result.cycle});
            }
        } catch (error) {
            if (!this.unmounted) {
                this.setState({error: error.message || 'Could not load Google Group syncs. Please retry.'});
            }
        } finally {
            if (!this.unmounted) {
                this.setState({busy: false});
            }
        }
    };

    nextSyncLabel = (sync) => {
        if (sync.next_sync_at) {
            return formatUTC(sync.next_sync_at);
        }
        if (sync.deadline_at <= Date.now()) {
            return 'None (expired)';
        }
        if (sync.sync_mode === 'continuous') {
            return 'Pending in continuous queue';
        }
        return sync.last_synced_at ? 'None before deadline' : 'Pending first sync';
    };

    renderSync = (sync) => (
        <li
            key={sync.group_email}
            style={styles.sync}
        >
            <strong>{sync.group_name || sync.group_email}</strong>
            {sync.group_name && <span>{` (${sync.group_email})`}</span>}
            {/* <div>{sync.channel_id ? 'Channel sync' : 'Team sync'}</div> */}
            <div>{`Creator: ${sync.creator_name ? `@${sync.creator_name}` : `Unknown user (${sync.created_by || 'no ID'})`}`}</div>
            <div>{`Scheduled Google account: ${sync.acting_email || 'Unknown'}`}</div>
            <div>{`Deadline: ${formatUTC(sync.deadline_at)}${sync.deadline_at <= Date.now() ? ' · Expired, awaiting cleanup' : ''}`}</div>
            {/* <div>{sync.last_synced_at ? `Last synced: ${formatUTC(sync.last_synced_at)}` : 'First sync pending'}</div> */}
            {/* <div>{`Next sync: ${this.nextSyncLabel(sync)}`}</div> */}
        </li>
    );

    renderChannel = (channel) => (
        <div
            key={channel.id}
            style={styles.channel}
        >
            <strong>{channel.name}</strong>
            <small style={styles.meta}>{`${channel.channelName ? ` ~${channel.channelName}` : ''}${channel.deleted ? ' · Archived' : ''}`}</small>
            <ul style={styles.list}>{channel.syncs.sort((a, b) => a.group_email.localeCompare(b.group_email)).map(this.renderSync)}</ul>
        </div>
    );

    renderTeam = (team) => (
        <details
            key={team.id}
            style={styles.team}
            aria-label={`Team: ${team.name}`}
        >
            <summary
                style={styles.teamHeading}
                className='fkth-google-sync-team-heading'
            >
                {team.name}
            </summary>
            {team.syncs.length > 0 && (
                <div style={styles.teamSyncs}>
                    <strong>{'Team syncs'}</strong>
                    <ul style={styles.list}>{team.syncs.sort((a, b) => a.group_email.localeCompare(b.group_email)).map(this.renderSync)}</ul>
                </div>
            )}
            {Array.from(team.channels.values()).sort((a, b) => a.name.localeCompare(b.name)).map(this.renderChannel)}
        </details>
    );

    render() {
        const teams = new Map();
        for (const sync of this.state.syncs) {
            if (!teams.has(sync.team_id)) {
                teams.set(sync.team_id, {id: sync.team_id, name: sync.team_name || `Unknown team (${sync.team_id || 'no ID'})`, syncs: [], channels: new Map()});
            }
            const team = teams.get(sync.team_id);
            if (!sync.channel_id) {
                team.syncs.push(sync);
                continue;
            }
            if (!team.channels.has(sync.channel_id)) {
                team.channels.set(sync.channel_id, {
                    id: sync.channel_id,
                    name: sync.channel_display_name || sync.channel_name || `Unknown channel (${sync.channel_id || 'no ID'})`,
                    channelName: sync.channel_name,
                    deleted: sync.channel_deleted,
                    syncs: [],
                });
            }
            team.channels.get(sync.channel_id).syncs.push(sync);
        }

        return (
            <div aria-busy={this.state.busy}>
                <style>
                    {'.fkth-google-sync-team-heading::marker {content: "";} .fkth-google-sync-team-heading::-webkit-details-marker {display: none;}'}
                </style>
                <div style={styles.heading}>
                    <strong>{'Google Group Syncs'}</strong>
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
                    {'Read-only overview of configured syncs across teams. Team syncs are listed before channel syncs. Use /google group sync commands to change them. Expired syncs are shown until they are cleaned up.'}
                </p>
                {!this.state.busy && !this.state.error && <p>{this.state.cycle}</p>}
                {this.state.busy && <p role='status'>{'Loading Google Group syncs…'}</p>}
                {this.state.error && (
                    <p
                        role='alert'
                        style={{...styles.notice, ...styles.error}}
                    >
                        {this.state.error}
                    </p>
                )}
                {!this.state.busy && !this.state.error && teams.size === 0 && <p>{'No Google Group syncs configured.'}</p>}
                {Array.from(teams.values()).sort((a, b) => a.name.localeCompare(b.name)).map(this.renderTeam)}
            </div>
        );
    }
}

const styles = {
    heading: {display: 'flex', alignItems: 'center', justifyContent: 'space-between'},
    team: {border: '1px solid #ddd', borderRadius: 4, margin: '16px 0', overflowWrap: 'anywhere'},
    teamHeading: {display: 'block', margin: 0, padding: '12px 16px 12px 28px', background: 'rgba(0, 0, 0, .03)', fontWeight: 600, cursor: 'pointer'},
    teamSyncs: {margin: '16px 16px 12px', paddingLeft: 12, borderLeft: '2px solid #ddd'},
    channel: {margin: '16px 16px 12px', paddingLeft: 12, borderLeft: '2px solid #ddd'},
    list: {listStyle: 'none', padding: 0, margin: '4px 0 0'},
    sync: {marginBottom: 8, padding: '8px 0 8px 12px', borderLeft: '2px solid #ddd'},
    meta: {opacity: 0.7},
    button: {minHeight: 44, flexShrink: 0},
    notice: {margin: '20px 0', padding: '14px 16px', border: '1px solid rgba(22, 109, 224, .3)', borderRadius: 4, background: 'rgba(22, 109, 224, .06)'},
    error: {borderColor: 'rgba(194, 48, 48, .4)', background: 'rgba(194, 48, 48, .06)'},
};
