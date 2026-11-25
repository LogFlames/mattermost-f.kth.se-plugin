import React from 'react';
import ReactDOM from 'react-dom';
import {act} from 'react-dom/test-utils';

import ChannelsInput from './channels_input';

jest.mock('react-select/async', () => {
    // eslint-disable-next-line global-require
    const mockReact = require('react');
    return ({isClearable, onChange}) => mockReact.createElement('button', {
        disabled: !isClearable,
        onClick: () => onChange(null),
        type: 'button',
    }, 'Clear');
});

it.each([
    {isMulti: false, clearable: true},
    {isMulti: true, clearable: false},
    {clearable: false},
])('allows clearing only an explicit single channel selection (multi: $isMulti)', ({isMulti, clearable}) => {
    const container = document.createElement('div');
    document.body.appendChild(container);
    const onChange = jest.fn();
    const actions = {searchChannels: jest.fn(), getTeams: jest.fn()};

    act(() => {
        ReactDOM.render(
            <ChannelsInput
                actions={actions}
                channels={[{id: 'channel'}]}
                isMulti={isMulti}
                onChange={onChange}
            />,
            container,
        );
    });
    const clear = container.querySelector('button');
    expect(clear.disabled).toBe(!clearable);
    act(() => clear.click());
    expect(onChange).toHaveBeenCalledTimes(clearable ? 1 : 0);
    if (clearable) {
        expect(onChange).toHaveBeenCalledWith(null);
    }

    ReactDOM.unmountComponentAtNode(container);
    container.remove();
});
