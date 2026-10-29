import React from 'react';
import PropTypes from 'prop-types';

import createMatrixSetting from './matrix_setting_factory';

const TranslatorChannelMatrix = createMatrixSetting({
    title: 'Translator channels',
    fields: [
        {name: 'name', label: 'Name', type: 'string'},
        {name: 'channel_ids', label: 'Monitored channels', type: 'channels', className: 'col-xs-12 col-sm-3'},
        {name: 'delivery', label: 'reply|dm', type: 'string', className: 'col-xs-12 col-sm-3'},
        {name: 'add_to_channel_ids', label: 'Add author to', type: 'channels', className: 'col-xs-12 col-sm-3'},
        {name: 'direct_message_prefix', label: 'DM prefix', type: 'string', className: 'col-xs-12 col-sm-3'},
    ],
});

const validate = (entries) => {
    if (!Array.isArray(entries)) {
        return 'Translator channels must be a JSON array.';
    }
    const channelIDs = new Set();
    for (const [index, entry] of entries.entries()) {
        if (!entry || typeof entry !== 'object' || typeof entry.channel_ids !== 'string' || !entry.channel_ids.trim()) {
            return `Configuration ${index + 1} must have a channel.`;
        }
        if (!entry.name || !entry.name.trim()) {
            return `Configuration ${index + 1} must have an identifying name.`;
        }
        if (channelIDs.has(entry.channel_ids)) {
            return `Configuration ${index + 1} duplicates another channel.`;
        }
        channelIDs.add(entry.channel_ids);
        if (entry.delivery !== 'reply' && entry.delivery !== 'dm') {
            return `Configuration ${index + 1} delivery must be reply or dm.`;
        }
        if (entry.delivery === 'dm' && (!entry.direct_message_prefix || !entry.direct_message_prefix.trim())) {
            return `Configuration ${index + 1} needs a direct-message prefix.`;
        }
    }
    return '';
};

export default function TranslatorChannels(props) {
    const error = validate(props.value || []);
    const onChange = (id, value) => {
        props.onChange(id, value);
        if (!validate(value)) {
            props.setSaveNeeded();
        }
    };

    return (
        <React.Fragment>
            <TranslatorChannelMatrix
                {...props}
                onChange={onChange}
                setSaveNeeded={() => null}
            />
            {error &&
                <div
                    className='has-error'
                    role='alert'
                >
                    {error}
                </div>}
        </React.Fragment>
    );
}

TranslatorChannels.propTypes = {
    id: PropTypes.string.isRequired,
    onChange: PropTypes.func.isRequired,
    setSaveNeeded: PropTypes.func.isRequired,
    value: PropTypes.array,
};
