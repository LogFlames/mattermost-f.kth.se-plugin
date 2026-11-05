import React, {useEffect, useMemo, useRef, useState} from 'react';
import PropTypes from 'prop-types';
import {connect} from 'react-redux';
import Client4 from 'mattermost-redux/client/client4';
import {isCurrentUserSystemAdmin} from 'mattermost-redux/selectors/entities/users';

import manifest from '../../manifest';

import styles from './styles';

const actions = [
    {id: 'categories', label: 'Sync categories', command: '/force_sync_categories', description: 'Move channels to their default categories, including those in Favorites.'},
    {id: 'internationals', label: 'Sync internationals', command: '/force_sync_internationals', description: 'Add tracked international students to their configured channels.'},
    {id: 'default_set', label: 'Set default channel', command: '/default_channel set', description: 'Add current and future team members automatically.'},
    {id: 'default_unset', label: 'Unset default channel', command: '/default_channel unset', description: 'Stop automatic additions without removing existing members.'},
    {id: 'mass_add', label: 'Add team members', command: '/mass_add', description: 'Add current team members to one channel.'},
    {id: 'keycloak_new_user', label: 'Create Keycloak account', command: '/keycloak new_user', description: 'Create a non-international account with a temporary password.'},
];
const pageSize = 25;

export function AdminPage({siteURL = '', currentUserID, systemAdmin = false}) {
    const client = useMemo(() => {
        const instance = new Client4();
        instance.setUrl(siteURL.replace(/\/$/, ''));
        return instance;
    }, [siteURL]);
    const baseURL = `${client.getUrl()}/plugins/${manifest.id}/admin`;
    const [input, setInput] = useState({action: 'categories', team_id: '', channel_id: '', email: '', username: '', first_name: '', last_name: ''});
    const [options, setOptions] = useState({teams: [], channels: [], system_admin: false});
    const [loading, setLoading] = useState(true);
    const [busy, setBusy] = useState('');
    const [error, setError] = useState('');
    const [preview, setPreview] = useState(null);
    const [result, setResult] = useState(null);
    const [confirmed, setConfirmed] = useState(false);
    const [password, setPassword] = useState('');
    const [search, setSearch] = useState('');
    const [page, setPage] = useState(0);
    const [now, setNow] = useState(Date.now());
    const [refresh, setRefresh] = useState(0);
    const generation = useRef(0);
    const resultHeading = useRef(null);

    useEffect(() => {
        generation.current += 1;
        setPreview(null);
        setPassword('');
        setConfirmed(false);
        setResult(null);
        setBusy('');
        return () => {
            generation.current += 1;
        };
    }, [currentUserID, systemAdmin]);

    useEffect(() => {
        let active = true;
        if (!currentUserID || !systemAdmin) {
            setLoading(false);
            return () => {
                active = false;
            };
        }
        setLoading(true);
        client.doFetch(`${baseURL}/options?team_id=${encodeURIComponent(input.team_id)}`, {method: 'get'}).
            then((data) => {
                if (active) {
                    data.teams.sort((a, b) => a.name.localeCompare(b.name));
                    data.channels.sort((a, b) => a.name.localeCompare(b.name));
                    setOptions(data);
                    if (!input.team_id && data.teams.length) {
                        setInput((value) => ({...value, team_id: data.teams[0].id}));
                    }
                }
            }).
            catch((err) => {
                if (active) {
                    setOptions({teams: [], channels: [], system_admin: false});
                    setError(err.message || 'Could not load available actions. Reload to retry.');
                }
            }).
            finally(() => {
                if (active) {
                    setLoading(false);
                }
            });
        return () => {
            active = false;
        };
    }, [client, currentUserID, systemAdmin, input.team_id, refresh]);

    useEffect(() => {
        const timer = preview ? setInterval(() => setNow(Date.now()), 1000) : null;
        return () => {
            if (timer) {
                clearInterval(timer);
            }
        };
    }, [preview]);

    useEffect(() => {
        if ((preview || result) && resultHeading.current) {
            resultHeading.current.focus();
        }
    }, [preview, result]);

    const changeInput = (values) => {
        generation.current += 1;
        setInput((value) => ({...value, ...values}));
        setPreview(null);
        setResult(null);
        setConfirmed(false);
        setPassword('');
        setError('');
        setSearch('');
        setPage(0);
    };
    const requestPreview = async (event) => {
        event.preventDefault();
        const version = ++generation.current;
        setBusy('previewing');
        setError('');
        setPreview(null);
        setConfirmed(false);
        setResult(null);
        setPage(0);
        setSearch('');
        try {
            const data = await client.doFetch(`${baseURL}/preview`, {method: 'post', body: JSON.stringify(input)});
            if (version === generation.current) {
                setPreview(data);
                setNow(Date.now());
            }
        } catch (err) {
            if (version === generation.current) {
                setError(err.message || 'Could not generate a preview. Nothing was applied.');
            }
        } finally {
            if (version === generation.current) {
                setBusy('');
            }
        }
    };
    const execute = async () => {
        if (!preview || !confirmed || busy || now >= preview.expires_at) {
            return;
        }
        const version = ++generation.current;
        setBusy('applying');
        setError('');
        setConfirmed(false);
        setPreview(null);
        try {
            const data = await client.doFetch(`${baseURL}/execute`, {method: 'post', body: JSON.stringify({preview_id: preview.id, password})});
            if (version === generation.current) {
                setResult(data);
                setRefresh((value) => value + 1);
            }
        } catch (err) {
            if (version === generation.current) {
                setError(err.message || 'The request failed; some changes may have been applied. Generate a new preview to check the current state.');
            }
        } finally {
            if (version === generation.current) {
                setPassword('');
                setBusy('');
            }
        }
    };

    const action = actions.find((entry) => entry.id === input.action);
    const account = input.action === 'keycloak_new_user';
    const team = options.teams.find((entry) => entry.id === input.team_id);
    const permitted = account ? options.system_admin : input.action === 'mass_add' || (team && team.can_manage);
    const needsChannel = !account && input.action !== 'internationals';
    const channels = options.channels.filter((entry) => {
        if (input.action === 'categories') {
            return Boolean(entry.category);
        }
        if (input.action === 'mass_add') {
            return entry.can_add;
        }
        return !entry.built_in && entry.is_default === (input.action === 'default_unset');
    });
    const expired = preview && now >= preview.expires_at;
    const changes = preview ? preview.changes.filter((entry) => Object.values(entry).join(' ').toLowerCase().includes(search.toLowerCase())) : [];
    const pages = Math.max(1, Math.ceil(changes.length / pageSize));

    return (
        <main className='fkth-admin'>
            <style>{styles}</style>
            <div className='admin-content'>
                <header className='admin-header'>
                    <div>
                        <h1>{'Administration'}</h1>
                        <p className='admin-muted'>{'Review changes before running the plugin’s admin commands.'}</p>
                    </div>
                    <span className='admin-badge'>{'Preview → confirm → apply'}</span>
                </header>
                {!currentUserID && <p role='alert'>{'Sign in to use the admin dashboard.'}</p>}
                {currentUserID && !systemAdmin && <p role='alert'>{'Only system admins can access the admin dashboard.'}</p>}
                {currentUserID && systemAdmin && (
                    <div className='admin-layout'>
                        <nav aria-label='Administrative actions'>
                            {actions.map((entry) => (
                                <button
                                    key={entry.id}
                                    type='button'
                                    aria-pressed={input.action === entry.id}
                                    disabled={Boolean(busy)}
                                    onClick={() => changeInput({action: entry.id, channel_id: ''})}
                                >
                                    <span>{entry.label}</span>
                                    <small>{entry.command}</small>
                                </button>
                            ))}
                        </nav>
                        <div className='admin-workspace'>
                            <form
                                className='admin-panel'
                                onSubmit={requestPreview}
                            >
                                <h2>{action.label}</h2>
                                <p className='admin-muted'>{action.description}</p>
                                <fieldset disabled={Boolean(busy) || loading}>
                                    <div className='admin-fields'>
                                        {!account && (
                                            <label htmlFor='admin-team'>{'Team'}
                                                <select
                                                    id='admin-team'
                                                    value={input.team_id}
                                                    required={true}
                                                    onChange={(event) => changeInput({team_id: event.target.value, channel_id: ''})}
                                                >
                                                    <option value=''>{loading ? 'Loading teams…' : 'Choose a team'}</option>
                                                    {options.teams.map((entry) => (
                                                        <option
                                                            key={entry.id}
                                                            value={entry.id}
                                                        >
                                                            {entry.name}
                                                        </option>
                                                    ))}
                                                </select>
                                            </label>
                                        )}
                                        {needsChannel && (
                                            <label htmlFor='admin-channel'>{'Channel'}
                                                <select
                                                    id='admin-channel'
                                                    value={input.channel_id}
                                                    required={input.action !== 'categories'}
                                                    onChange={(event) => changeInput({channel_id: event.target.value})}
                                                >
                                                    <option value=''>{input.action === 'categories' ? 'All channels with a default category' : 'Choose a channel'}</option>
                                                    {channels.map((entry) => (
                                                        <option
                                                            key={entry.id}
                                                            value={entry.id}
                                                        >
                                                            {`${entry.name}${entry.private ? ' (private)' : ''}${entry.category ? ` · ${entry.category}` : ''}`}
                                                        </option>
                                                    ))}
                                                </select>
                                            </label>
                                        )}
                                        {account && ['email', 'username', 'first_name', 'last_name'].map((field) => (
                                            <label
                                                key={field}
                                                htmlFor={`admin-${field}`}
                                            >{({email: 'Email', username: 'Mattermost username', first_name: 'First name', last_name: 'Last name'})[field]}
                                                <input
                                                    id={`admin-${field}`}
                                                    type={field === 'email' ? 'email' : 'text'}
                                                    required={true}
                                                    autoComplete='off'
                                                    value={input[field]}
                                                    onChange={(event) => changeInput({[field]: event.target.value})}
                                                />
                                            </label>
                                        ))}
                                    </div>
                                    {input.action.startsWith('default_') && (
                                        <details className='admin-defaults'>
                                            <summary>{'Current default channels'}</summary>
                                            <ul>{options.channels.filter((entry) => entry.is_default || entry.built_in).map((entry) => (
                                                <li key={entry.id}>{`${entry.name}${entry.built_in ? ' (built in)' : ''} — ${entry.category || 'Channels'}`}</li>
                                            ))}</ul>
                                        </details>
                                    )}
                                    {!permitted && !loading && <p className='admin-notice'>{account ? 'Only system admins can create Keycloak accounts.' : 'Only team admins can run this action for the selected team.'}</p>}
                                    <div className='admin-footer'>
                                        <span className='admin-muted'>{'Generating a preview does not apply changes.'}</span>
                                        <button
                                            className='admin-primary'
                                            type='submit'
                                            disabled={!permitted || loading || Boolean(busy)}
                                        >{busy === 'previewing' ? 'Generating preview…' : 'Generate preview'}</button>
                                    </div>
                                </fieldset>
                            </form>
                            {error && (
                                <p
                                    className='admin-error'
                                    role='alert'
                                >{error}</p>
                            )}
                            {busy === 'applying' && (
                                <p
                                    className='admin-notice'
                                    role='status'
                                >{'Applying reviewed changes… Do not submit the action again.'}</p>
                            )}
                            {result && (
                                <section
                                    className='admin-panel'
                                    aria-label='Action result'
                                >
                                    <h2
                                        ref={resultHeading}
                                        tabIndex={-1}
                                    >{result.failed ? 'Completed with errors' : 'Request accepted'}</h2>
                                    <p role='status'>{result.message}</p>
                                    <p className='admin-muted'>{`${result.completed} completed · ${result.queued} queued · ${result.failed} failed`}</p>
                                </section>
                            )}
                            {preview && (
                                <section
                                    className='admin-panel'
                                    aria-label='Change preview'
                                >
                                    <div className='admin-preview-header'>
                                        <h2
                                            ref={resultHeading}
                                            tabIndex={-1}
                                        >{`${preview.changes.length} proposed ${preview.changes.length === 1 ? 'change' : 'changes'}`}</h2>
                                        <span className='admin-badge'>{'Nothing applied yet'}</span>
                                    </div>
                                    <p>{preview.summary}</p>
                                    {preview.warnings.length > 0 && <ul className='admin-notice'>{preview.warnings.map((warning) => <li key={warning}>{warning}</li>)}</ul>}
                                    {preview.changes.length === 0 && <p role='status'>{'Everything in this selection is already up to date.'}</p>}
                                    {preview.changes.length > 0 && (
                                        <React.Fragment>
                                            <label htmlFor='admin-search'>{'Filter proposed changes'}
                                                <input
                                                    id='admin-search'
                                                    type='search'
                                                    value={search}
                                                    placeholder='Username, channel, or category'
                                                    onChange={(event) => {
                                                        setSearch(event.target.value);
                                                        setPage(0);
                                                    }}
                                                />
                                            </label>
                                            <div className='admin-table-wrap'>
                                                <table>
                                                    <thead><tr><th scope='col'>{'User / channel'}</th><th scope='col'>{'Before'}</th><th scope='col'>{'After'}</th></tr></thead>
                                                    <tbody>{changes.slice(page * pageSize, (page + 1) * pageSize).map((entry) => (
                                                        <tr key={`${entry.kind}:${entry.user_id || ''}:${entry.channel_id || ''}`}>
                                                            <td><strong>{entry.username ? `@${entry.username}` : entry.channel || 'Account'}</strong>{entry.username && entry.channel && <small>{entry.channel}</small>}</td>
                                                            <td>{entry.before}</td><td>{entry.after}</td>
                                                        </tr>
                                                    ))}</tbody>
                                                </table>
                                                {changes.length === 0 && <p>{'No matching changes.'}</p>}
                                            </div>
                                            {pages > 1 && (
                                                <div className='admin-pagination'>
                                                    <button
                                                        type='button'
                                                        disabled={page === 0}
                                                        onClick={() => setPage(page - 1)}
                                                    >{'Previous'}</button>
                                                    <span>{`Page ${page + 1} of ${pages} · ${changes.length} matches`}</span>
                                                    <button
                                                        type='button'
                                                        disabled={page + 1 >= pages}
                                                        onClick={() => setPage(page + 1)}
                                                    >{'Next'}</button>
                                                </div>
                                            )}
                                            <div className='admin-confirm'>
                                                {account && (
                                                    <label htmlFor='admin-password'>
                                                        {'Temporary password'}
                                                        <input
                                                            id='admin-password'
                                                            type='password'
                                                            autoComplete='new-password'
                                                            value={password}
                                                            onChange={(event) => setPassword(event.target.value)}
                                                        />
                                                        <small className='admin-muted'>{'Sent only when applying; never included in the preview.'}</small>
                                                    </label>
                                                )}
                                                <label className='admin-check'>
                                                    <input
                                                        type='checkbox'
                                                        checked={confirmed}
                                                        disabled={Boolean(expired)}
                                                        onChange={(event) => setConfirmed(event.target.checked)}
                                                    />
                                                    {`I reviewed ${preview.changes.length} proposed ${preview.changes.length === 1 ? 'change' : 'changes'} and the warnings above.`}
                                                </label>
                                                <div className='admin-footer'>
                                                    <span className='admin-muted'>{expired ? 'Preview expired. Generate a new preview.' : 'Expires in five minutes. State is checked again before applying.'}</span>
                                                    <button
                                                        className='admin-primary'
                                                        type='button'
                                                        disabled={!confirmed || Boolean(expired) || Boolean(busy) || (account && !password.trim())}
                                                        onClick={execute}
                                                    >{account ? 'Create account' : 'Apply reviewed changes'}</button>
                                                </div>
                                            </div>
                                        </React.Fragment>
                                    )}
                                </section>
                            )}
                            {!preview && !result && !busy && <p className='admin-empty'>{'Choose a team and action, then generate a preview to see the affected users and channels.'}</p>}
                        </div>
                    </div>
                )}
            </div>
        </main>
    );
}

AdminPage.propTypes = {siteURL: PropTypes.string, currentUserID: PropTypes.string, systemAdmin: PropTypes.bool};

export default connect((state) => ({
    siteURL: state.entities.general.config.SiteURL || '',
    currentUserID: state.entities.users.currentUserId,
    systemAdmin: isCurrentUserSystemAdmin(state),
}))(AdminPage);
