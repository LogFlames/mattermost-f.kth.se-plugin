import React, {useEffect, useMemo, useState} from 'react';
import PropTypes from 'prop-types';
import Client4 from 'mattermost-redux/client/client4';

import manifest from '../../manifest';

export default function MistralModelSetting({id, value, config, disabled, setByEnv, onChange, setSaveNeeded}) {
    const [models, setModels] = useState([]);
    const [busy, setBusy] = useState(true);
    const [error, setError] = useState('');
    const [refresh, setRefresh] = useState(0);
    const siteURL = config.ServiceSettings?.SiteURL || '';
    const client = useMemo(() => {
        const instance = new Client4();
        instance.setUrl(siteURL.replace(/\/$/, ''));
        return instance;
    }, [siteURL]);

    useEffect(() => {
        let active = true;
        setBusy(true);
        setError('');
        client.doFetch(`${client.getUrl()}/plugins/${manifest.id}/mistral/models`, {method: 'get'}).then((result) => {
            if (active) {
                setModels(result.models);
            }
        }).catch((failure) => {
            if (active) {
                setError(failure.message || 'Could not load Mistral models.');
            }
        }).finally(() => {
            if (active) {
                setBusy(false);
            }
        });
        return () => {
            active = false;
        };
    }, [client, refresh]);

    const selection = typeof value === 'string' ? {id: value} : (value || {id: 'ministral-14b-2512'});
    const model = models.find((entry) => entry.id === selection.id);
    const efforts = model?.reasoning_efforts || [];
    const large4 = selection.id === 'mistral-large-4' || selection.id === 'mistral-large-4-0';
    const effort = selection.reasoning_effort || (large4 ? 'low' : '');
    const readOnly = disabled || setByEnv;
    const change = (next) => {
        if (!readOnly) {
            onChange(id, next);
            setSaveNeeded();
        }
    };
    let reasoningHelp = 'This model does not support adjustable reasoning.';
    if (large4) {
        reasoningHelp = 'Large 4 supports low, medium, and high. Thinking cannot be fully disabled; only final-answer text is posted.';
    } else if (!model) {
        reasoningHelp = 'Reasoning options will appear when the selected model is available.';
    } else if (efforts.length) {
        reasoningHelp = 'Only the selected model’s documented reasoning levels are offered.';
    } else if (model?.capabilities.reasoning) {
        reasoningHelp = 'Reasoning is supported, but its available levels are not documented here. The provider default is used.';
    }

    return (
        <div>
            <div style={{display: 'flex', gap: 8, alignItems: 'center'}}>
                <select
                    id={id}
                    className='form-control'
                    aria-label='Mistral model'
                    value={selection.id}
                    disabled={readOnly || busy || models.length === 0}
                    onChange={(event) => {
                        const next = models.find((entry) => entry.id === event.target.value);
                        change({id: next.id, reasoning_effort: next.reasoning_efforts.includes('low') ? 'low' : ''});
                    }}
                >
                    {!model && <option value={selection.id}>{selection.id}</option>}
                    {models.map((entry) => (
                        <option
                            key={entry.id}
                            value={entry.id}
                        >
                            {entry.id}
                        </option>
                    ))}
                </select>
                <button
                    type='button'
                    className='btn btn-default'
                    disabled={busy}
                    onClick={() => setRefresh(refresh + 1)}
                >
                    {'Refresh models'}
                </button>
            </div>
            {busy && <p role='status'>{'Loading models…'}</p>}
            {error && (
                <p
                    className='text-danger'
                    role='alert'
                >
                    {error}
                </p>
            )}
            {!busy && !error && models.length === 0 && <p role='status'>{'No chat-completion models are available for the saved API key.'}</p>}
            {!busy && !error && !model && models.length > 0 && <p role='alert'>{'The saved model is not in the available model list. Its value has not been changed.'}</p>}
            <div style={{marginTop: 16}}>
                <label htmlFor={`${id}-reasoning`}>{'Reasoning effort'}</label>
                <select
                    id={`${id}-reasoning`}
                    className='form-control'
                    value={effort}
                    disabled={readOnly || busy || efforts.length === 0}
                    onChange={(event) => change({id: selection.id, reasoning_effort: event.target.value})}
                >
                    {!large4 && <option value=''>{'Provider default'}</option>}
                    {effort && !efforts.includes(effort) && <option value={effort}>{effort}</option>}
                    {efforts.map((level) => (
                        <option
                            key={level}
                            value={level}
                        >
                            {level}
                        </option>
                    ))}
                </select>
                <p className='help-text'>{reasoningHelp}</p>
            </div>
            {model && <div style={{marginTop: 16, overflowWrap: 'anywhere'}}>
                <p>{model.description}</p>
                <dl style={{marginBottom: 8}}>
                    <dt>{'Context window'}</dt>
                    <dd>{model.max_context_length ? `${model.max_context_length.toLocaleString('en-US')} tokens` : 'Not provided'}</dd>
                    <dt>{'Capabilities'}</dt>
                    <dd>{['Chat', model.capabilities.reasoning && 'Reasoning', model.capabilities.vision && 'Vision'].filter(Boolean).join(' · ')}</dd>
                    {(model.pricing || model.cost) && <>
                        <dt>{'Pricing'}</dt>
                        <dd>{JSON.stringify(model.pricing || model.cost)}</dd>
                    </>}
                </dl>
            </div>}
            <p className='help-text'>{'Uses the saved API key. Save key changes before refreshing models. Model and reasoning changes take effect after Save.'}</p>
        </div>
    );
}

MistralModelSetting.propTypes = {
    id: PropTypes.string.isRequired,
    value: PropTypes.oneOfType([PropTypes.string, PropTypes.object]),
    config: PropTypes.object.isRequired,
    disabled: PropTypes.bool,
    setByEnv: PropTypes.bool,
    onChange: PropTypes.func.isRequired,
    setSaveNeeded: PropTypes.func.isRequired,
};
