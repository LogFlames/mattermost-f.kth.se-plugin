import React from 'react';
import ReactDOM from 'react-dom';
import {act} from 'react-dom/test-utils';
import Client4 from 'mattermost-redux/client/client4';

import DefaultChannelsSettings from './default_channels_cs';

jest.mock('mattermost-redux/client/client4');

let container;
let client;
let editor;
let saveAction;
let onChange;
let unregister;

const channel = (id) => ({id, name: id, display_name: id.toUpperCase(), team_id: 'team', team_display_name: 'Team', type: 'O'});

async function mount(value = ['b', 'a'], flags = {}) {
    const ref = React.createRef();
    await act(async () => {
        ReactDOM.render(
            <DefaultChannelsSettings
                ref={ref}
                id='defaultchannels_custom'
                config={{}}
                value={value}
                onChange={onChange}
                registerSaveAction={(action) => {
                    saveAction = action;
                }}
                unRegisterSaveAction={unregister}
                {...flags}
            />,
            container,
        );
    });
    editor = ref.current;
}

function add(id) {
    act(() => editor.setState({selected: channel(id)}));
    act(() => container.querySelector('.btn-primary').click());
}

function remove(id) {
    act(() => container.querySelector(`[aria-label="Remove ${id.toUpperCase()} as a default channel"]`).click());
}

beforeEach(() => {
    container = document.createElement('div');
    document.body.appendChild(container);
    onChange = jest.fn();
    unregister = jest.fn();
    client = {
        setUrl: jest.fn(),
        getUrl: () => '',
        doFetch: jest.fn().mockResolvedValue({channels: [channel('b'), channel('a')], value: ['b', 'a'], enabled: true}),
    };
    Client4.mockImplementation(() => client);
});

afterEach(() => {
    ReactDOM.unmountComponentAtNode(container);
    container.remove();
});

it.each([
    ['stale', ['b'], {}, true],
    ['current', ['b', 'a'], {}, false],
    ['disabled', ['b'], {disabled: true}, false],
    ['environment override', ['b'], {setByEnv: true}, false],
])('syncs only writable stale settings: %s', async (name, value, flags, shouldStage) => {
    await mount(value, flags);
    if (shouldStage) {
        expect(onChange).toHaveBeenCalledWith('defaultchannels_custom', ['b', 'a']);
    } else {
        expect(onChange).not.toHaveBeenCalled();
    }
    expect(client.doFetch).toHaveBeenCalledTimes(1);
    expect(client.doFetch).toHaveBeenCalledWith(expect.any(String), {method: 'get'});
});

it('does not change the form if the saved configuration cannot be read', async () => {
    client.doFetch.mockRejectedValue(new Error('Unavailable'));
    await mount(['keep']);
    expect(onChange).not.toHaveBeenCalled();
    expect(container.querySelector('[role="alert"]').textContent).toBe('Unavailable');
});

it('stages additions and removals and queues only new channels on Save', async () => {
    await mount();
    add('c');
    remove('a');
    expect(onChange).toHaveBeenLastCalledWith('defaultchannels_custom', ['b', 'c']);
    expect(client.doFetch).toHaveBeenCalledTimes(1);
    expect(container.textContent).toContain('Unsaved changes');
    expect(container.querySelector('button').disabled).toBe(true);
    await act(async () => {
        await editor.refresh();
    });
    expect(client.doFetch).toHaveBeenCalledTimes(1);

    client.doFetch.mockResolvedValue({status: 'OK'});
    let result;
    await act(async () => {
        result = await saveAction();
    });
    expect(result).toEqual({});
    const request = client.doFetch.mock.calls[1][1];
    expect(request.method).toBe('post');
    expect(JSON.parse(request.body)).toEqual({save_id: expect.any(String), expected_channel_ids: ['b', 'c'], added_channel_ids: ['c']});
    expect(container.textContent).not.toContain('Unsaved changes');
    await act(async () => {
        await saveAction();
    });
    expect(client.doFetch).toHaveBeenCalledTimes(2);
});

it('keeps pending changes and the same request ID when Save fails', async () => {
    await mount();
    add('c');
    client.doFetch.mockRejectedValue(new Error('The channel list was not saved'));
    let result;
    await act(async () => {
        result = await saveAction();
    });
    expect(result).toEqual({error: {message: 'The channel list was not saved'}});
    expect(container.textContent).toContain('Unsaved changes');
    expect(editor.state.ids).toEqual(['b', 'a', 'c']);
    const firstAttempt = client.doFetch.mock.calls[1][1];
    client.doFetch.mockResolvedValue({status: 'OK'});
    await act(async () => {
        result = await saveAction();
    });
    expect(result).toEqual({});
    expect(client.doFetch.mock.calls[2][1]).toEqual(firstAttempt);
});

it('does not backfill canceled additions or save on unmount', async () => {
    await mount();
    add('c');
    remove('c');
    await act(async () => {
        await saveAction();
    });
    expect(client.doFetch).toHaveBeenCalledTimes(1);
    add('d');
    await act(async () => {
        ReactDOM.unmountComponentAtNode(container);
    });
    expect(unregister).toHaveBeenCalledWith(saveAction);
    expect(client.doFetch).toHaveBeenCalledTimes(1);
});

it('saves removals without queueing additions', async () => {
    await mount();
    remove('a');
    client.doFetch.mockResolvedValue({status: 'OK'});
    await act(async () => {
        await saveAction();
    });
    expect(JSON.parse(client.doFetch.mock.calls[1][1].body)).toEqual({save_id: expect.any(String), expected_channel_ids: ['b'], added_channel_ids: []});
});
