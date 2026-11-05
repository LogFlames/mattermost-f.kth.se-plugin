import React from 'react';
import ReactDOM from 'react-dom';
import {act, Simulate} from 'react-dom/test-utils';
import Client4 from 'mattermost-redux/client/client4';

import {AdminPage} from './admin_page';

jest.mock('mattermost-redux/client/client4');

let container;
let request;
let preview;

function button(text) {
    return Array.from(container.querySelectorAll('button')).find((entry) => entry.textContent.startsWith(text));
}

function change(selector, value) {
    act(() => {
        const field = container.querySelector(selector);
        field.value = value;
        Simulate.change(field);
    });
}

async function generate() {
    await act(async () => Simulate.submit(container.querySelector('form')));
}

beforeEach(async () => {
    container = document.createElement('div');
    document.body.appendChild(container);
    preview = {
        id: 'preview-id',
        expires_at: Date.now() + 300000,
        summary: 'Move channels to their default categories.',
        warnings: ['Overrides Favorites.'],
        changes: Array.from({length: 26}, (_, index) => ({kind: 'category', user_id: `user-${index}`, username: `member-${index}`, channel_id: 'news', channel: 'News', before: 'Favorites', after: 'Reception'})),
    };
    request = jest.fn(async (url) => {
        if (url.includes('/options')) {
            return {system_admin: true, teams: [{id: 'team', name: 'Fysiksektionen', can_manage: true}], channels: [{id: 'news', name: 'News', category: 'Reception', can_add: true, is_default: false}]};
        }
        if (url.endsWith('/preview')) {
            return preview;
        }
        return {completed: 0, queued: 26, failed: 0, message: 'Reviewed category moves queued in the background.'};
    });
    Client4.mockImplementation(() => ({setUrl: jest.fn(), getUrl: () => '', doFetch: request}));
    await act(async () => {
        ReactDOM.render(
            <AdminPage
                currentUserID='admin'
                systemAdmin={true}
            />,
            container,
        );
    });
});

afterEach(() => {
    act(() => {
        ReactDOM.unmountComponentAtNode(container);
    });
    container.remove();
});

it('hides the dashboard and discards reviewed changes when system admin access is removed', async () => {
    await generate();
    expect(container.querySelector('[aria-label="Change preview"]')).not.toBe(null);
    request.mockClear();
    await act(async () => {
        ReactDOM.render(
            <AdminPage
                currentUserID='admin'
                systemAdmin={false}
            />,
            container,
        );
    });
    expect(container.querySelector('[role=alert]').textContent).toBe('Only system admins can access the admin dashboard.');
    expect(container.querySelector('nav, form, button, input, select')).toBe(null);
    expect(container.querySelector('[aria-label="Change preview"]')).toBe(null);
    expect(request).not.toHaveBeenCalled();
    await act(async () => {
        ReactDOM.render(
            <AdminPage
                currentUserID='admin'
                systemAdmin={true}
            />,
            container,
        );
    });
    expect(container.querySelector('form')).not.toBe(null);
    expect(container.querySelector('[aria-label="Change preview"]')).toBe(null);
});

it('requires explicit review, supports paging and search, and clears the preview after a scope edit', async () => {
    await generate();
    expect(container.querySelectorAll('tbody tr')).toHaveLength(25);
    expect(button('Apply reviewed').disabled).toBe(true);
    expect(document.activeElement.textContent).toBe('26 proposed changes');
    act(() => button('Next').click());
    expect(container.querySelector('tbody').textContent).toContain('@member-25');
    change('#admin-search', 'member-17');
    expect(container.querySelectorAll('tbody tr')).toHaveLength(1);
    expect(container.querySelector('tbody').textContent).toContain('@member-17');
    act(() => container.querySelector('input[type=checkbox]').click());
    expect(button('Apply reviewed').disabled).toBe(false);
    change('#admin-channel', 'news');
    expect(container.querySelector('[aria-label="Change preview"]')).toBe(null);
    expect(request.mock.calls.filter(([url]) => url.endsWith('/execute'))).toHaveLength(0);
    await generate();
    expect(button('Apply reviewed').disabled).toBe(true);
    act(() => container.querySelector('input[type=checkbox]').click());
    await act(async () => button('Apply reviewed').click());
    expect(container.querySelector('[role=status]').textContent).toContain('queued in the background');
    expect(container.textContent).toContain('0 completed · 26 queued · 0 failed');
    expect(button('Apply reviewed')).toBeFalsy();
});

it('does not allow expired previews or hide a stale-state rejection', async () => {
    preview.expires_at = Date.now() - 1000;
    await generate();
    expect(button('Apply reviewed').disabled).toBe(true);
    expect(container.textContent).toContain('Preview expired.');
    preview.expires_at = Date.now() + 300000;
    await generate();
    act(() => container.querySelector('input[type=checkbox]').click());
    request.mockRejectedValueOnce(new Error('The affected state changed. Nothing was applied. Generate a new preview.'));
    await act(async () => button('Apply reviewed').click());
    expect(container.querySelector('[role=alert]').textContent).toContain('state changed');
    expect(button('Apply reviewed')).toBeFalsy();
});

it('asks for a temporary password only after previewing a Keycloak account', async () => {
    act(() => button('Create Keycloak account').click());
    for (const [field, value] of Object.entries({email: 'test@example.org', username: 'test-user', first_name: 'Test', last_name: 'User'})) {
        change(`#admin-${field}`, value);
    }
    expect(container.querySelector('input[type=password]')).toBe(null);
    preview.changes = [{kind: 'create_account', username: 'test-user', before: 'No account', after: 'Test User · test@example.org'}];
    await generate();
    const previewRequest = JSON.parse(request.mock.calls.find(([url]) => url.endsWith('/preview'))[1].body);
    expect(previewRequest.password).toBeFalsy();
    act(() => container.querySelector('input[type=checkbox]').click());
    expect(button('Create account').disabled).toBe(true);
    change('#admin-password', 'temporary-test-password');
    expect(button('Create account').disabled).toBe(false);
    await act(async () => button('Create account').click());
    const executeRequest = JSON.parse(request.mock.calls.find(([url]) => url.endsWith('/execute'))[1].body);
    expect(executeRequest).toEqual({preview_id: 'preview-id', password: 'temporary-test-password'});
    expect(container.textContent).not.toContain('temporary-test-password');
});
