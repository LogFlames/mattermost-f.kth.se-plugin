import React, {useEffect, useMemo, useRef, useState} from 'react';
import PropTypes from 'prop-types';
import {connect} from 'react-redux';
import Client4 from 'mattermost-redux/client/client4';
import Select from 'react-select';
import AsyncSelect from 'react-select/async';

import manifest from '../../manifest';

import StatsCharts from './stats_charts';
import {dateRange, readStatsFilters, statsFilterQuery} from './stats_filters';
import styles from './styles';

export function StatsPage({siteURL = '', currentUserID}) {
    const history = window.WebappUtils.browserHistory;
    const client = useMemo(() => {
        const instance = new Client4();
        instance.setUrl(siteURL.replace(/\/$/, ''));
        return instance;
    }, [siteURL]);
    const [me, setMe] = useState(null);
    const [teams, setTeams] = useState([]);
    const [loading, setLoading] = useState(true);
    const [error, setError] = useState('');
    const [scope, setScope] = useState('all');
    const [team, setTeam] = useState('');
    const [channel, setChannel] = useState(null);
    const [channels, setChannels] = useState(null);
    const [includeDMs, setIncludeDMs] = useState(false);
    const [usersMode, setUsersMode] = useState('choose');
    const [users, setUsers] = useState([]);
    const [memberChannel, setMemberChannel] = useState(null);
    const [thread, setThread] = useState('');
    const [range, setRange] = useState(() => dateRange(30));
    const [preset, setPreset] = useState('all');
    const [cumulative, setCumulative] = useState(true);
    const [navigation, setNavigation] = useState(0);
    const [busy, setBusy] = useState(false);
    const [report, setReport] = useState(null);
    const generation = useRef(0);
    const restoring = useRef(true);
    const writtenSearch = useRef(null);

    useEffect(() => {
        const restore = (location, action) => {
            if (action !== 'REPLACE' || location.search !== writtenSearch.current) {
                setNavigation((value) => value + 1);
            }
        };
        return history.listen(restore);
    }, [history]);

    useEffect(() => {
        let active = true;
        restoring.current = true;
        const filters = readStatsFilters(history.location.search);
        generation.current += 1;
        setMe(null);
        setReport(null);
        setScope(filters.scope);
        setTeam(filters.team);
        setChannel(filters.channel);
        setChannels(null);
        setMemberChannel(filters.memberChannel);
        setThread(filters.thread);
        setIncludeDMs(filters.includeDMs);
        setUsersMode(filters.usersMode);
        setRange(filters.range);
        setPreset(filters.preset);
        setCumulative(filters.cumulative);
        setUsers([]);
        setBusy(false);
        setLoading(true);
        setError('');
        if (!currentUserID) {
            setLoading(false);
            return () => {
                active = false;
            };
        }
        client.
            getMe().
            then(async (viewer) => {
                const availableTeams = await client.getTeamsForUser(viewer.id);
                let selectedUsers = [viewer];
                if (filters.userIDs !== null) {
                    let profiles = [];
                    try {
                        if (filters.userIDs.length > 200) {
                            throw new Error('Choose up to 200 users.');
                        }
                        if (filters.userIDs.length > 0) {
                            profiles = await client.getProfilesByIds(filters.userIDs);
                        }
                    } catch (err) {
                        if (active) {
                            setError(err.message || 'Could not load the selected users.');
                        }
                    }
                    selectedUsers = [];
                    for (const id of filters.userIDs) {
                        selectedUsers.push(profiles.find((profile) => profile.id === id) || {id});
                    }
                }
                if (active) {
                    setMe(viewer);
                    setUsers(selectedUsers);
                    setTeams(
                        availableTeams.
                            filter((entry) => !entry.delete_at).
                            sort((a, b) => a.display_name.localeCompare(b.display_name)),
                    );
                }
            }).
            catch((err) => {
                if (active) {
                    setError(err.message || 'Could not load your account. Reload to try again.');
                }
            }).
            finally(() => {
                if (active) {
                    restoring.current = false;
                    setLoading(false);
                }
            });
        return () => {
            active = false;
            generation.current += 1;
        };
    }, [client, currentUserID, history, navigation]);

    const needsChannels = scope === 'channel' || usersMode === 'channel';
    useEffect(() => {
        let active = true;
        if (me && needsChannels && channels === null) {
            client.doFetch(`${client.getUrl()}/plugins/${manifest.id}/stats/channels`, {method: 'get'}).
                then((matches) => {
                    if (active) {
                        setChannels(matches);
                        const publicIDs = new Set();
                        for (const entry of matches) {
                            publicIDs.add(entry.id);
                        }
                        for (const id of [channel, memberChannel]) {
                            if (id && !publicIDs.has(id)) {
                                setError('A linked channel is unavailable or is not public. Choose a public channel.');
                            }
                        }
                    }
                }).
                catch((err) => {
                    if (active) {
                        setError(err.message || 'Could not load public channels.');
                    }
                });
        }
        return () => {
            active = false;
        };
    }, [client, me, needsChannels, channels]);

    const searchUsers = async (term) => {
        try {
            if (!term.trim()) {
                return await client.getProfiles(0, 100);
            }
            return await client.searchUsers(term, {limit: 100});
        } catch (err) {
            setError(err.message || 'Could not search users.');
            return [];
        }
    };

    const request = {
        scope,
        team_id: scope === 'team' ? team : '',
        channel_id: scope === 'channel' ? channel || '' : '',
        include_dms: scope === 'all' && includeDMs,
        users_mode: usersMode,
        user_ids: usersMode === 'choose' ? users.map((user) => user.id) : [],
        member_channel: usersMode === 'channel' ? memberChannel || '' : '',
        thread: usersMode === 'thread' ? thread : '',
        ...(preset === 'all' ? {all_time: true} : range),
    };
    const dirty = report && JSON.stringify(request) !== report.key;
    const query = statsFilterQuery(request, preset, cumulative);
    useEffect(() => {
        if (!restoring.current && !loading && me && currentUserID) {
            writtenSearch.current = `?${query}`;
            if (history.location.search !== writtenSearch.current) {
                history.replace({...history.location, search: writtenSearch.current});
            }
        }
    }, [query, loading, me, currentUserID, history]);

    const show = async (event) => {
        event.preventDefault();
        const currentGeneration = generation.current;
        setError('');
        setBusy(true);
        setReport(null);
        try {
            const data = await client.doFetch(`${client.getUrl()}/plugins/${manifest.id}/stats`, {
                method: 'post',
                body: JSON.stringify(request),
            });
            if (currentGeneration !== generation.current) {
                return;
            }
            const scopeName = {
                all: 'All server channels',
                team: teams.find((entry) => entry.id === team)?.display_name || 'Selected team',
                channel: channels?.find((entry) => entry.id === channel)?.display_name || 'Selected channel',
            }[scope];
            setReport({
                data,
                request,
                start: request.all_time ? data.days[0]?.date : request.start,
                end: request.all_time ? data.days[data.days.length - 1]?.date : request.end,
                key: JSON.stringify(request),
                title: `${scopeName}${request.include_dms ? ' + all direct and group messages' : ''}`,
            });
        } catch (err) {
            if (currentGeneration !== generation.current) {
                return;
            }
            if (err.status_code === 401) {
                setMe(null);
            }
            setError(err.message || 'Could not load statistics. Try again.');
        } finally {
            if (currentGeneration === generation.current) {
                setBusy(false);
            }
        }
    };

    const channelPicker = (id, value, onChange) => (
        <Select
            inputId={id}
            classNamePrefix='stats-select'
            options={channels || []}
            isLoading={channels === null}
            value={channels?.find((entry) => entry.id === value) || null}
            onChange={(entry) => onChange(entry?.id || '')}
            getOptionValue={(entry) => entry.id}
            getOptionLabel={(entry) =>
                `${entry.team_display_name || teams.find((entryTeam) => entryTeam.id === entry.team_id)?.display_name || entry.team_name || ''} › ${entry.display_name}`
            }
            placeholder='Search all public channels…'
            noOptionsMessage={() => 'No public channels found.'}
        />
    );

    if (loading) {
        return (
            <main className='fkth-stats'>
                <style>{styles}</style>
                <p role='status'>{'Loading your account…'}</p>
            </main>
        );
    }
    if (!currentUserID || !me) {
        return (
            <main className='fkth-stats'>
                <style>{styles}</style>
                <h1>{'Message statistics'}</h1>
                <p>{'Sign in to this Mattermost server to view statistics.'}</p>
                {error && <p role='alert'>{error}</p>}
                <a href={`${client.getUrl()}/login?redirect_to=${encodeURIComponent(history.location.pathname + history.location.search + history.location.hash)}`}>{'Sign in'}</a>
            </main>
        );
    }

    return (
        <main className='fkth-stats'>
            <style>{styles}</style>
            <div className='stats-content'>
                <header className='stats-header'>
                    <div>
                        <span className='stats-eyebrow'>{'ACTIVITY'}</span>
                        <h1>{'Message statistics'}</h1>
                    </div>
                    <span className='stats-badge'>{'Daily aggregates · UTC'}</span>
                </header>
                <p className='stats-muted'>
                    {'Compare message activity and writing length over time.'}
                </p>
                <form
                    onSubmit={show}
                    className='stats-card'
                >
                    <fieldset disabled={busy}>
                        <div className='stats-filter-grid'>
                            <div className='stats-field'>
                                <label htmlFor='stats-scope'>{'Filter'}</label>
                                <select
                                    id='stats-scope'
                                    value={scope}
                                    onChange={(event) => setScope(event.target.value)}
                                >
                                    <option value='all'>{'All'}</option>
                                    <option value='team'>{'Specific team'}</option>
                                    <option value='channel'>{'Specific channel'}</option>
                                </select>
                                {scope === 'all' && (
                                    <label className='stats-check'>
                                        <input
                                            type='checkbox'
                                            checked={includeDMs}
                                            onChange={(event) =>
                                                setIncludeDMs(event.target.checked)
                                            }
                                        />
                                        {'Include DMs'}
                                    </label>
                                )}
                                {scope === 'team' && (
                                    <>
                                        <label htmlFor='stats-team'>{'Team'}</label>
                                        <select
                                            id='stats-team'
                                            required={true}
                                            value={team}
                                            onChange={(event) => setTeam(event.target.value)}
                                        >
                                            <option value=''>{'Choose a team…'}</option>
                                            {team && !teams.some((entry) => entry.id === team) && (
                                                <option value={team}>{'Selected team (not in your teams)'}</option>
                                            )}
                                            {teams.map((entry) => (
                                                <option
                                                    key={entry.id}
                                                    value={entry.id}
                                                >
                                                    {entry.display_name}
                                                </option>
                                            ))}
                                        </select>
                                    </>
                                )}
                                {scope === 'channel' && (
                                    <>
                                        <label htmlFor='stats-channel'>{'Channel'}</label>
                                        {channelPicker('stats-channel', channel, setChannel)}
                                    </>
                                )}
                            </div>
                            <div className='stats-field'>
                                <label htmlFor='stats-users-mode'>{'Users'}</label>
                                <select
                                    id='stats-users-mode'
                                    value={usersMode}
                                    onChange={(event) => setUsersMode(event.target.value)}
                                >
                                    <option value='choose'>{'Choose users'}</option>
                                    <option value='channel'>{'Channel members'}</option>
                                    <option value='thread'>{'Thread members'}</option>
                                </select>
                                {usersMode === 'choose' && (
                                    <>
                                        <label htmlFor='stats-users'>{'Selected users'}</label>
                                        <AsyncSelect
                                            inputId='stats-users'
                                            classNamePrefix='stats-select'
                                            isMulti={true}
                                            defaultOptions={true}
                                            loadOptions={searchUsers}
                                            value={users}
                                            onChange={(value) => setUsers(value || [])}
                                            getOptionValue={(user) => user.id}
                                            getOptionLabel={(user) => (user.username ? `@${user.username}` : `Unavailable user (${user.id})`)}
                                            placeholder='Search users…'
                                        />
                                    </>
                                )}
                                {usersMode === 'channel' && (
                                    <>
                                        <label htmlFor='stats-member-channel'>
                                            {'Members of channel'}
                                        </label>
                                        {channelPicker(
                                            'stats-member-channel',
                                            memberChannel,
                                            setMemberChannel,
                                        )}
                                    </>
                                )}
                                {usersMode === 'thread' && (
                                    <>
                                        <label htmlFor='stats-thread'>
                                            {'Root message ID or URL'}
                                        </label>
                                        <input
                                            id='stats-thread'
                                            required={true}
                                            value={thread}
                                            onChange={(event) => setThread(event.target.value)}
                                            placeholder='Paste a root message ID or permalink'
                                        />
                                        <small className='stats-muted'>
                                            {
                                                'Selects the root author and reply authors, not thread followers.'
                                            }
                                        </small>
                                    </>
                                )}
                            </div>
                            <div className='stats-field'>
                                <label htmlFor='stats-range'>{'Time range'}</label>
                                <select
                                    id='stats-range'
                                    value={preset}
                                    onChange={(event) => {
                                        setPreset(event.target.value);
                                        if (!['custom', 'all'].includes(event.target.value)) {
                                            setRange(dateRange(Number(event.target.value)));
                                        }
                                    }}
                                >
                                    <option value='all'>{'All time'}</option>
                                    <option value='7'>{'Last 7 days'}</option>
                                    <option value='30'>{'Last 30 days'}</option>
                                    <option value='90'>{'Last 90 days'}</option>
                                    <option value='365'>{'Last year'}</option>
                                    <option value='custom'>{'Custom dates'}</option>
                                </select>
                                {preset !== 'all' && <div className='stats-dates'>
                                    {['start', 'end'].map((key) => (
                                        <div key={key}>
                                            <label htmlFor={`stats-${key}`}>
                                                {key === 'start' ? 'From' : 'Through'}
                                            </label>
                                            <input
                                                id={`stats-${key}`}
                                                type='date'
                                                required={true}
                                                value={range[key]}
                                                onChange={(event) => {
                                                    setPreset('custom');
                                                    setRange({
                                                        ...range,
                                                        [key]: event.target.value,
                                                    });
                                                }}
                                            />
                                        </div>
                                    ))}
                                </div>}
                            </div>
                        </div>
                        <div className='stats-form-footer'>
                            <button
                                type='submit'
                                className='stats-show'
                                disabled={busy}
                            >
                                {busy ? 'Loading…' : 'Show'}
                            </button>
                        </div>
                    </fieldset>
                </form>
                {error && (
                    <p
                        className='stats-error'
                        role='alert'
                    >
                        {error}
                    </p>
                )}
                {busy && <p role='status'>{'Calculating daily statistics…'}</p>}
                {dirty && (
                    <p
                        role='status'
                        className='stats-notice'
                    >
                        {'Filters changed. Press Show to update the results below.'}
                    </p>
                )}
                {report && (
                    <section aria-label='Statistics results'>
                        <div className='stats-results-heading'>
                            <h2>{report.title}</h2>
                            <span className='stats-muted'>
                                {report.request.all_time && 'All time · '}
                                {report.start && `${report.start} – ${report.end} · `}
                                {`${report.data.users.length} users`}
                            </span>
                        </div>
                        {report.data.days.length > 0 ? (
                            <StatsCharts
                                data={report.data}
                                start={report.start}
                                end={report.end}
                                cumulative={cumulative}
                                setCumulative={setCumulative}
                            />
                        ) : (
                            <div
                                className='stats-card stats-empty'
                                role='status'
                            >
                                <h2>{'No messages in this selection'}</h2>
                                <p className='stats-muted'>
                                    {'Try another date range, filter, or group of users.'}
                                </p>
                            </div>
                        )}
                    </section>
                )}
                {!report && !busy && (
                    <div className='stats-empty'>
                        <h2>{'Choose a comparison'}</h2>
                        <p className='stats-muted'>
                            {'Select users and a time range, then press Show.'}
                        </p>
                    </div>
                )}
            </div>
        </main>
    );
}

StatsPage.propTypes = {siteURL: PropTypes.string, currentUserID: PropTypes.string};

export default connect((state) => ({
    siteURL: state.entities.general.config.SiteURL || '',
    currentUserID: state.entities.users.currentUserId,
}))(StatsPage);
