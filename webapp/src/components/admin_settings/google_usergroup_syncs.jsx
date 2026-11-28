import React from 'react';
import PropTypes from 'prop-types';

import createMatrixSetting from './matrix_setting_factory';

const Matrix = createMatrixSetting({
    title: 'Google usergroup syncs',
    fields: [
        {name: 'usergroup', type: 'string', placeholder: '@usergroup'},
        {name: 'group_email', type: 'string', placeholder: 'group@fysiksektionen.se'},
        {name: 'excluded_users', type: 'users', placeholder: 'Excluded users'},
    ],
});

export const validate = (entries) => {
    if (!Array.isArray(entries)) {
        return 'Configuration type error.';
    }
    const seen = new Set();
    for (const [index, entry] of entries.entries()) {
        const name = entry && entry.usergroup;
        if (typeof name !== 'string' || !(/^@[a-z0-9._-]{1,64}$/).test(name) || ['@all', '@channel', '@here'].includes(name)) {
            return `Entry ${index + 1} requires a valid @usergroup.`;
        }
        if (seen.has(name)) {
            return `Entry ${index + 1} duplicates a usergroup.`;
        }
        seen.add(name);
        if (typeof entry.group_email !== 'string' || !(/^[^\s@<>]+@fysiksektionen\.se$/i).test(entry.group_email)) {
            return `Entry ${index + 1} requires a group@fysiksektionen.se address.`;
        }
    }
    return '';
};

export default function GoogleUsergroupSyncs(props) {
    const error = validate(props.value || []);
    const onChange = (id, value) => {
        props.onChange(id, value);
        if (!validate(value)) {
            props.setSaveNeeded();
        }
    };
    return (
        <React.Fragment>
            <Matrix
                {...props}
                onChange={onChange}
                setSaveNeeded={() => null}
            />
            {error &&
                <div
                    className='has-error'
                    role='alert'
                    style={{color: 'red'}}
                >
                    {error}
                </div>}
        </React.Fragment>
    );
}

GoogleUsergroupSyncs.propTypes = {
    onChange: PropTypes.func.isRequired,
    setSaveNeeded: PropTypes.func.isRequired,
    value: PropTypes.array,
};
