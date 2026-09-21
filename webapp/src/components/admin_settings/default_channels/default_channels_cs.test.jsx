import React from 'react';
import ReactDOM from 'react-dom';
import {act} from 'react-dom/test-utils';
import Client4 from 'mattermost-redux/client/client4';

import DefaultChannelsSettings from './default_channels_cs';

jest.mock('mattermost-redux/client/client4');

let container;
let client;

beforeEach(() => {
    container = document.createElement('div');
    document.body.appendChild(container);
    client = {
        setUrl: jest.fn(),
        getUrl: () => '',
        doFetch: jest.fn().mockResolvedValue({channels: [], value: ['b', 'a'], enabled: true}),
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
    const onChange = jest.fn();
    await act(async () => {
        ReactDOM.render(
            <DefaultChannelsSettings
                id='defaultchannels_custom'
                config={{}}
                value={value}
                onChange={onChange}
                {...flags}
            />,
            container,
        );
    });
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
    const onChange = jest.fn();
    await act(async () => {
        ReactDOM.render(
            <DefaultChannelsSettings
                id='defaultchannels_custom'
                config={{}}
                value={['keep']}
                onChange={onChange}
            />,
            container,
        );
    });
    expect(onChange).not.toHaveBeenCalled();
    expect(container.querySelector('[role="alert"]').textContent).toBe('Unavailable');
});
