import React, {useState} from 'react';
import ReactDOM from 'react-dom';
import {act, Simulate} from 'react-dom/test-utils';
import Client4 from 'mattermost-redux/client/client4';

import MistralModelSetting from './mistral_model';

jest.mock('mattermost-redux/client/client4');

const models = [
    {id: 'ministral-14b-2512', description: 'Ministral 14B', max_context_length: 262144, capabilities: {}, reasoning_efforts: []},
    {id: 'mistral-large-4', description: 'Mistral Large 4', max_context_length: 1048576, capabilities: {reasoning: true, vision: true}, reasoning_efforts: ['low', 'medium', 'high']},
    {id: 'mistral-small-latest', description: 'Mistral Small 4', max_context_length: 262144, capabilities: {reasoning: true}, reasoning_efforts: ['none', 'high'], pricing: {input_per_million: 0.3}},
];

let container;
let fetchModels;
let changes;

async function mount(value = 'ministral-14b-2512', flags = {}) {
    function Form() {
        const [draft, setDraft] = useState(value);
        return (
            <MistralModelSetting
                id='model'
                value={draft}
                config={{}}
                onChange={(id, next) => {
                    changes(next);
                    setDraft(next);
                }}
                setSaveNeeded={jest.fn()}
                {...flags}
            />
        );
    }
    await act(async () => {
        ReactDOM.render(<Form/>, container);
    });
}

function select(id, value) {
    act(() => {
        const control = container.querySelector(`#${id}`);
        control.value = value;
        Simulate.change(control);
    });
}

beforeEach(() => {
    container = document.createElement('div');
    document.body.appendChild(container);
    changes = jest.fn();
    fetchModels = jest.fn().mockResolvedValue({models});
    Client4.mockImplementation(() => ({setUrl: jest.fn(), getUrl: () => '', doFetch: fetchModels}));
});

afterEach(() => {
    ReactDOM.unmountComponentAtNode(container);
    container.remove();
});

it('changes reasoning options and metadata with the draft model, retaining only compatible effort', async () => {
    await mount();
    expect(changes).not.toHaveBeenCalled();
    expect(container.querySelector('#model-reasoning').disabled).toBe(true);
    select('model', 'mistral-large-4');
    expect(Array.from(container.querySelector('#model-reasoning').options, (option) => option.value)).toEqual(['low', 'medium', 'high']);
    expect(container.textContent).toContain('1,048,576 tokens');
    expect(container.textContent).not.toContain('Pricing');
    expect(container.textContent).not.toContain('Not provided by the /models API.');
    select('model-reasoning', 'medium');
    expect(changes).toHaveBeenLastCalledWith({id: 'mistral-large-4', reasoning_effort: 'medium'});
    select('model', 'mistral-small-latest');
    expect(Array.from(container.querySelector('#model-reasoning').options, (option) => option.value)).toEqual(['', 'none', 'high']);
    expect(container.textContent).toContain('Pricing');
    expect(container.textContent).toContain('0.3');
    select('model-reasoning', 'none');
    expect(changes).toHaveBeenLastCalledWith({id: 'mistral-small-latest', reasoning_effort: 'none'});
    select('model', 'ministral-14b-2512');
    expect(changes).toHaveBeenLastCalledWith({id: 'ministral-14b-2512', reasoning_effort: ''});
    expect(container.querySelector('#model-reasoning').disabled).toBe(true);
});

it('restores a saved selection without changing it and preserves it when refresh fails', async () => {
    await mount({id: 'mistral-large-4', reasoning_effort: 'high'});
    expect(container.querySelector('#model-reasoning').value).toBe('high');
    fetchModels.mockRejectedValue(new Error('Save the Mistral API key before loading models.'));
    await act(async () => container.querySelector('button').click());
    expect(container.querySelector('[role="alert"]').textContent).toContain('Save the Mistral API key');
    expect(container.querySelector('#model').value).toBe('mistral-large-4');
    expect(changes).not.toHaveBeenCalled();
});

it('shows the saved reasoning level when the initial model lookup fails', async () => {
    fetchModels.mockRejectedValue(new Error('Mistral is unavailable.'));
    await mount({id: 'mistral-small-latest', reasoning_effort: 'none'});
    expect(container.querySelector('#model-reasoning').value).toBe('none');
    expect(container.querySelector('#model-reasoning').disabled).toBe(true);
    expect(changes).not.toHaveBeenCalled();
});

it.each([{disabled: true}, {setByEnv: true}])('keeps read-only selections disabled: %p', async (flags) => {
    await mount({id: 'mistral-large-4', reasoning_effort: 'low'}, flags);
    expect(container.querySelector('#model').disabled).toBe(true);
    expect(container.querySelector('#model-reasoning').disabled).toBe(true);
    expect(changes).not.toHaveBeenCalled();
});
