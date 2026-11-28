import React from 'react';
import ReactDOM from 'react-dom';
import {act} from 'react-dom/test-utils';

import GoogleUsergroupSyncs, {validate} from './google_usergroup_syncs';

jest.mock('react-redux', () => ({
    connect: () => (Component) => (props) => {
        // eslint-disable-next-line global-require
        const mockReact = require('react');
        return mockReact.createElement(Component, {
            ...props,
            actions: {getProfilesByIds: () => Promise.resolve({data: []})},
        });
    },
}));

jest.mock('./inputs/users_input', () => {
    // eslint-disable-next-line global-require
    const mockReact = require('react');
    return ({onChange}) => mockReact.createElement('button', {
        type: 'button',
        onClick: () => onChange([{id: 'excluded-user'}, {id: 'second-user'}]),
    }, 'Select excluded users');
});

test('validates usergroup mappings and rejects duplicates and other domains', () => {
    const entry = {usergroup: '@example', group_email: 'group@fysiksektionen.se', excluded_users: ['user-id']};
    expect(validate([entry])).toBe('');
    expect(validate([entry, entry])).toMatch(/duplicates/);
    expect(validate([{...entry, usergroup: 'example'}])).toMatch(/@usergroup/);
    expect(validate([{...entry, group_email: 'group@example.org'}])).toMatch(/fysiksektionen/);
    expect(validate([{...entry, group_email: 'group'}])).toMatch(/fysiksektionen/);
});

test('emits a mapping with multi-selected user IDs', async () => {
    const container = document.createElement('div');
    document.body.appendChild(container);
    const onChange = jest.fn();
    const setSaveNeeded = jest.fn();
    await act(async () => {
        ReactDOM.render(
            <GoogleUsergroupSyncs
                id='GoogleGroupsUsergroupSyncs'
                value={[{usergroup: '@example', group_email: 'group@fysiksektionen.se', excluded_users: []}]}
                onChange={onChange}
                setSaveNeeded={setSaveNeeded}
            />,
            container,
        );
        await Promise.resolve();
    });
    act(() => container.querySelector('button').click());
    expect(onChange).toHaveBeenLastCalledWith('GoogleGroupsUsergroupSyncs', [{
        usergroup: '@example',
        group_email: 'group@fysiksektionen.se',
        excluded_users: ['excluded-user', 'second-user'],
    }]);
    expect(setSaveNeeded).toHaveBeenCalled();
    ReactDOM.unmountComponentAtNode(container);
    container.remove();
});
