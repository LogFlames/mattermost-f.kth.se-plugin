import React from 'react';
import ReactDOM from 'react-dom';
import {act} from 'react-dom/test-utils';

import createSelectSetting from './select_setting_factory';

jest.mock('react-redux', () => ({
    connect: () => (Component) => Component,
}));

jest.mock('./inputs/users_input', () => {
    // eslint-disable-next-line global-require
    const mockReact = require('react');
    return ({isMulti, onChange, users}) => mockReact.createElement(
        'button',
        {
            'data-selected': JSON.stringify(users),
            onClick: () => onChange(isMulti ? [{id: 'next-user'}, {id: 'other-user'}] : {id: 'next-user'}),
            type: 'button',
        },
        'Select users',
    );
});

jest.mock('./inputs/channels_input', () => {
    // eslint-disable-next-line global-require
    const mockReact = require('react');
    return ({channels, onChange}) => mockReact.createElement(
        mockReact.Fragment,
        null,
        mockReact.createElement(
            'button',
            {
                'data-selected': JSON.stringify(channels),
                onClick: () => onChange([{id: 'next-channel'}]),
                type: 'button',
            },
            'Select channels',
        ),
        mockReact.createElement('button', {onClick: () => onChange(null), type: 'button'}, 'Clear channel'),
    );
});

let container;

const renderSetting = async (Setting, props) => {
    await act(async () => {
        ReactDOM.render(<Setting {...props}/>, container);
        await Promise.resolve();
        await Promise.resolve();
    });
};

beforeEach(() => {
    container = document.createElement('div');
    document.body.appendChild(container);
});

afterEach(() => {
    ReactDOM.unmountComponentAtNode(container);
    container.remove();
});

it.each([
    {
        isMulti: false,
        value: 'saved-user',
        expected: 'next-user',
    },
    {
        isMulti: true,
        value: ['saved-user'],
        expected: ['next-user', 'other-user'],
    },
])('hydrates and serializes user selections (multi: $isMulti)', async ({isMulti, value, expected}) => {
    const Setting = createSelectSetting({type: 'users', placeholder: 'Select users', isMulti});
    const onChange = jest.fn();
    const setSaveNeeded = jest.fn();
    const actions = {
        getChannel: jest.fn(),
        getTeam: jest.fn(),
        getUser: jest.fn((id) => Promise.resolve({data: {id, username: id}})),
    };

    await renderSetting(Setting, {actions, id: 'users', onChange, setSaveNeeded, value});

    expect(container.querySelector('button').getAttribute('data-selected')).toBe(JSON.stringify([{id: 'saved-user', username: 'saved-user'}]));
    expect(actions.getUser).toHaveBeenCalledWith('saved-user');
    act(() => container.querySelector('button').click());
    expect(onChange).toHaveBeenCalledWith('users', expected);
    expect(setSaveNeeded).toHaveBeenCalledTimes(1);
});

it('hydrates channel labels and serializes selected channel IDs', async () => {
    const Setting = createSelectSetting({type: 'channels', placeholder: 'Select channels', isMulti: true});
    const onChange = jest.fn();
    const setSaveNeeded = jest.fn();
    const actions = {
        getChannel: jest.fn((id) => Promise.resolve({data: {id, team_id: 'team-id'}})),
        getTeam: jest.fn(() => Promise.resolve({data: {display_name: 'Team'}})),
        getUser: jest.fn(),
    };

    await renderSetting(Setting, {actions, id: 'channels', onChange, setSaveNeeded, value: ['saved-channel']});

    expect(container.querySelector('button').getAttribute('data-selected')).toBe(JSON.stringify([{
        id: 'saved-channel',
        team_id: 'team-id',
        team_display_name: 'Team',
    }]));
    act(() => container.querySelector('button').click());
    expect(onChange).toHaveBeenCalledWith('channels', ['next-channel']);
    expect(setSaveNeeded).toHaveBeenCalledTimes(1);
});

it('clears a single channel setting and marks it for saving', async () => {
    const Setting = createSelectSetting({type: 'channels', placeholder: 'Select channel'});
    const onChange = jest.fn();
    const setSaveNeeded = jest.fn();
    const actions = {
        getChannel: jest.fn((id) => Promise.resolve({data: {id, team_id: 'team-id'}})),
        getTeam: jest.fn(() => Promise.resolve({data: {display_name: 'Team'}})),
        getUser: jest.fn(),
    };

    await renderSetting(Setting, {actions, id: 'moderation', onChange, setSaveNeeded, value: 'saved-channel'});
    act(() => container.querySelectorAll('button')[1].click());
    expect(onChange).toHaveBeenCalledWith('moderation', null);
    expect(setSaveNeeded).toHaveBeenCalledTimes(1);
    expect(container.querySelector('button').getAttribute('data-selected')).toBe('[]');
});
