import React from 'react';
import ReactDOM from 'react-dom';
import {act} from 'react-dom/test-utils';
import Client4 from 'mattermost-redux/client/client4';

import GoogleGroupsSyncOverview from './google_groups_sync_overview';

jest.mock('mattermost-redux/client/client4');

let container;
let client;

async function mount() {
    await act(async () => {
        ReactDOM.render(<GoogleGroupsSyncOverview config={{ServiceSettings: {SiteURL: 'https://example.org/'}}}/>, container);
    });
}

beforeEach(() => {
    container = document.createElement('div');
    document.body.appendChild(container);
    client = {setUrl: jest.fn(), getUrl: () => 'https://example.org', doFetch: jest.fn()};
    Client4.mockImplementation(() => client);
});

afterEach(() => {
    ReactDOM.unmountComponentAtNode(container);
    container.remove();
});

it('indents team and channel sync entries with a second vertical bar', async () => {
    client.doFetch.mockResolvedValue({cycle: 'Sync cycle: 1 hour (hourly schedule).', syncs: [
        {team_id: 'team', team_name: 'Physics', channel_id: '', group_email: 'team@example.org'},
        {team_id: 'team', team_name: 'Physics', channel_id: 'channel', channel_name: 'general', group_email: 'channel@example.org'},
    ]});
    await mount();

    const entries = Array.from(container.querySelectorAll('li'));
    expect(entries).toHaveLength(2);
    entries.forEach((entry) => {
        expect(entry.style.borderLeftWidth).toBe('2px');
        expect(entry.style.paddingLeft).toBe('12px');
        expect(entry.style.marginBottom).toBe('8px');
        expect(entry.parentElement.parentElement.style.borderLeftWidth).toBe('2px');
    });
});

it('groups configured syncs by team and channel and shows their accounts and deadlines', async () => {
    client.doFetch.mockResolvedValue({cycle: 'Minimum sync cycle: 20 seconds, plus operation time.', syncs: [
        {
            team_id: 'team',
            team_name: 'Physics',
            channel_id: 'channel',
            channel_name: 'general',
            channel_display_name: 'General',
            group_name: 'People',
            group_email: 'people@example.org',
            created_by: 'creator',
            creator_name: 'alice',
            acting_email: 'actor@example.org',
            deadline_at: Date.UTC(2099, 0, 1, 23, 59, 59, 999),
            last_synced_at: Date.UTC(2026, 0, 1, 12),
            next_sync_at: Date.UTC(2026, 0, 1, 12, 23),
        },
        {
            team_id: 'team',
            team_name: 'Physics',
            channel_id: '',
            group_name: 'Everyone',
            group_email: 'everyone@example.org',
            created_by: 'creator',
            creator_name: 'alice',
            acting_email: 'actor@example.org',
            deadline_at: Date.UTC(2099, 0, 1, 23, 59, 59, 999),
            last_synced_at: Date.UTC(2026, 0, 1, 12),
            next_sync_at: Date.UTC(2026, 0, 1, 12, 23),
        },
        {
            team_id: 'missing',
            team_name: '',
            channel_id: 'unknown',
            channel_name: '',
            group_email: 'expired@example.org',
            created_by: 'missing-user',
            acting_email: '',
            deadline_at: 1,
            last_synced_at: 0,
        },
    ]});
    await mount();
    expect(client.setUrl).toHaveBeenCalledWith('https://example.org');
    expect(client.doFetch).toHaveBeenCalledWith(expect.stringContaining('/google-groups/syncs'), {method: 'get'});
    expect(container.textContent).toContain('Minimum sync cycle: 20 seconds, plus operation time.');
    const teams = Array.from(container.querySelectorAll('details'));
    expect(teams.map((team) => team.querySelector('summary').textContent)).toEqual(['Physics', 'Unknown team (missing)']);
    expect(teams[0].textContent).toContain('General');
    expect(teams[0].textContent).toContain('~general');
    expect(teams[0].textContent).toContain('People (people@example.org)');
    expect(teams[0].textContent).toContain('Creator: @alice');
    expect(teams[0].textContent).toContain('Scheduled Google account: actor@example.org');
    expect(teams[0].textContent).toContain('2099-01-01 23:59:59.999 UTC');
    expect(teams[0].textContent).toContain('Team syncs');
    expect(teams[0].textContent).toContain('Everyone (everyone@example.org)');
    expect(teams[0].textContent).toContain('Team sync');
    expect(teams[0].textContent.indexOf('Team syncs')).toBeLessThan(teams[0].textContent.indexOf('General'));
    expect(teams[1].textContent).toContain('Unknown channel (unknown)');
    expect(teams[1].textContent).toContain('Unknown user (missing-user)');
    expect(teams[1].textContent).toContain('Expired, awaiting cleanup');
});

it('shows empty and error states and can refresh after an error', async () => {
    client.doFetch.mockRejectedValueOnce(new Error('Unavailable')).mockResolvedValueOnce({syncs: [], cycle: 'Sync cycle: no active team or channel syncs.'});
    await mount();
    expect(container.querySelector('[role="alert"]').textContent).toBe('Unavailable');
    await act(async () => {
        container.querySelector('button').click();
    });
    expect(container.textContent).toContain('No Google Group syncs configured.');
    expect(container.textContent).toContain('Sync cycle: no active team or channel syncs.');
    expect(container.querySelector('[role="alert"]')).toBeNull();
    expect(client.doFetch).toHaveBeenCalledTimes(2);
});

it('reports an invalid response instead of showing an empty list', async () => {
    client.doFetch.mockResolvedValue({});
    await mount();
    expect(container.querySelector('[role="alert"]').textContent).toContain('Invalid Google Group sync response');
    expect(container.textContent).not.toContain('No Google Group syncs configured.');
});

it('labels continuous syncs as queued instead of reporting an hourly time', () => {
    const overview = new GoogleGroupsSyncOverview({config: {ServiceSettings: {}}});
    expect(overview.nextSyncLabel({sync_mode: 'continuous', deadline_at: Date.now() + 10000, last_synced_at: Date.now()})).toBe('Pending in continuous queue');
    expect(overview.nextSyncLabel({sync_mode: 'continuous', deadline_at: 1})).toBe('None (expired)');
});
