// Written by GPT-5.6 Luna

import React from 'react';
import PropTypes from 'prop-types';
import {connect} from 'react-redux';
import {bindActionCreators} from 'redux';

import {getProfilesByIds} from 'mattermost-redux/actions/users';
import {getTeam, getTeams} from 'mattermost-redux/actions/teams';
import {getChannel} from 'mattermost-redux/actions/channels';

import ChannelsInput from './inputs/channels_input';
import TeamsInput from './inputs/teams_input';
import UsersInput from './inputs/users_input';

const SELECT_TYPES = new Set(['channels', 'teams', 'users']);
const FIELD_TYPES = new Set(['string', 'number', 'int', 'boolean', 'bool', ...SELECT_TYPES]);

const getId = (value) => {
    if (value && typeof value === 'object') {
        return value.id;
    }

    return value;
};

const getError = (response) => response && (response.error || response.err);

const asArray = (value) => {
    if (value === null || value === undefined) {
        return [];
    }

    return Array.isArray(value) ? value : [value];
};

const normalizeField = (field) => {
    const normalized = {
        isMulti: SELECT_TYPES.has(field.type) ? field.type !== 'teams' : false,
        ...field,
    };

    if (!normalized.name || !FIELD_TYPES.has(normalized.type)) {
        throw new Error(`Unsupported matrix setting field: ${normalized.name || '<unnamed>'}`);
    }

    return normalized;
};

const normalizeFields = (fields) => {
    if (!Array.isArray(fields) || fields.length === 0) {
        throw new Error('createMatrixSetting requires at least one field');
    }

    const normalized = fields.map(normalizeField);
    const names = normalized.map((field) => field.name);
    if (new Set(names).size !== names.length) {
        throw new Error('Matrix setting field names must be unique');
    }

    return normalized;
};

const getDefaultValue = (field) => {
    if (field.defaultValue !== undefined) {
        return field.defaultValue;
    }

    if (field.type === 'boolean' || field.type === 'bool') {
        return false;
    }

    if (field.type === 'channels' || field.type === 'users' || (field.type === 'teams' && field.isMulti)) {
        return [];
    }

    return field.type === 'teams' ? null : '';
};

const getRecordValue = (record, field) => {
    if (record && record[field.name] !== undefined) {
        return record[field.name];
    }

    return getDefaultValue(field);
};

const serializeValue = (field, value) => {
    if (!SELECT_TYPES.has(field.type)) {
        return value;
    }

    if (field.isMulti) {
        return asArray(value).map(getId).filter((id) => id !== undefined && id !== null && id !== '');
    }

    return value ? getId(value) : null;
};

const getInitialRecord = (record, fields) => fields.reduce((result, field) => {
    result[field.name] = getRecordValue(record, field);
    return result;
}, {});

const hydrateField = async (field, value, actions) => {
    if (!SELECT_TYPES.has(field.type)) {
        return value;
    }

    const ids = asArray(value).filter(Boolean).map(getId);
    if (!ids.length) {
        return field.isMulti ? [] : null;
    }

    if (field.type === 'users') {
        const response = await actions.getProfilesByIds(ids);
        return getError(response) ? value : response.data;
    }

    if (field.type === 'teams') {
        const responses = await Promise.all(ids.map(actions.getTeam));
        return responses.filter((response) => !getError(response)).map((response) => response.data);
    }

    const channelResponses = await Promise.all(ids.map(actions.getChannel));
    const channels = channelResponses.filter((response) => !getError(response)).map((response) => response.data);
    const teamsResponse = await actions.getTeams(0, 60);
    const teams = getError(teamsResponse) ? [] : teamsResponse.data;

    return channels.map((channel) => ({
        ...channel,
        team_display_name: (teams.find((team) => team.id === channel.team_id) || {}).display_name || '<Team Not Found>',
    }));
};

const MatrixEntry = ({fields, id, value, onChange, onDelete, hideDelete, actions}) => {
    const [hydratedValue, setHydratedValue] = React.useState(() => getInitialRecord(value, fields));

    React.useEffect(() => {
        let mounted = true;
        Promise.all(fields.map(async (field) => [field.name, await hydrateField(field, getRecordValue(value, field), actions)]))
            .then((entries) => {
                if (mounted) {
                    setHydratedValue(entries.reduce((result, [name, fieldValue]) => {
                        result[name] = fieldValue;
                        return result;
                    }, {}));
                }
            });

        return () => {
            mounted = false;
        };
    }, [actions, fields, value]);

    const updateField = (field, nextValue) => {
        const nextHydratedValue = {...hydratedValue, [field.name]: nextValue};
        setHydratedValue(nextHydratedValue);
        onChange(fields.reduce((record, currentField) => {
            record[currentField.name] = serializeValue(currentField, nextHydratedValue[currentField.name]);
            return record;
        }, {...value}));
    };

    return (
        <div style={styles.attributeRow}>
            <div className='row'>
                {fields.map((field) => (
                    <FieldInput
                        key={field.name}
                        field={field}
                        value={hydratedValue[field.name]}
                        onChange={(nextValue) => updateField(field, nextValue)}
                    />
                ))}
                {!hideDelete &&
                    <div className='col-xs-12 col-sm-1'>
                        <a style={styles.deleteIcon} onClick={() => onDelete(id)}>
                            <i className='fa fa-trash'/>
                        </a>
                    </div>}
            </div>
        </div>
    );
};

MatrixEntry.propTypes = {
    actions: PropTypes.object.isRequired,
    fields: PropTypes.array.isRequired,
    hideDelete: PropTypes.bool,
    id: PropTypes.number,
    onChange: PropTypes.func.isRequired,
    onDelete: PropTypes.func,
    value: PropTypes.object,
};

const FieldInput = ({field, value, onChange}) => {
    const commonProps = {
        className: 'form-control',
        id: field.name,
        placeholder: field.placeholder || field.label || field.name,
    };

    if (field.type === 'channels') {
        return <div className={field.className || 'col-xs-12 col-sm-3'}><ChannelsInput {...commonProps} channels={value} isMulti={field.isMulti} onChange={onChange}/></div>;
    }

    if (field.type === 'teams') {
        return <div className={field.className || 'col-xs-12 col-sm-3'}><TeamsInput {...commonProps} teams={asArray(value)} isMulti={field.isMulti} onChange={onChange}/></div>;
    }

    if (field.type === 'users') {
        return <div className={field.className || 'col-xs-12 col-sm-3'}><UsersInput {...commonProps} users={value} isMulti={field.isMulti} onChange={onChange}/></div>;
    }

    if (field.type === 'boolean' || field.type === 'bool') {
        return <div className={field.className || 'col-xs-12 col-sm-2'}><label><input {...commonProps} type='checkbox' checked={Boolean(value)} onChange={(event) => onChange(event.target.checked)}/>{field.label || field.name}</label></div>;
    }

    const type = field.type === 'number' || field.type === 'int' ? 'number' : 'text';
    return <div className={field.className || 'col-xs-12 col-sm-3'}><input {...commonProps} type={type} value={value === null || value === undefined ? '' : value} onChange={(event) => onChange(type === 'number' ? Number(event.target.value) : event.target.value)}/></div>;
};

FieldInput.propTypes = {
    field: PropTypes.object.isRequired,
    onChange: PropTypes.func.isRequired,
    value: PropTypes.any,
};

const createConnectedEntry = (fields) => connect(
    null,
    (dispatch) => ({
        actions: bindActionCreators({getProfilesByIds, getTeam, getTeams, getChannel}, dispatch),
    }),
)(function ConnectedMatrixEntry(props) {
    return <MatrixEntry {...props} fields={fields}/>;
});

export default function createMatrixSetting({fields, title}) {
    const normalizedFields = normalizeFields(fields);
    const Entry = createConnectedEntry(normalizedFields);

    class MatrixSetting extends React.Component {
        static propTypes = {
            id: PropTypes.string.isRequired,
            onChange: PropTypes.func.isRequired,
            setSaveNeeded: PropTypes.func.isRequired,
            value: PropTypes.array,
        };

        state = {
            entries: this.props.value || [],
            draft: {},
            showAddEntry: false,
        };

        emit = (entries) => {
            this.setState({entries});
            this.props.onChange(this.props.id, entries);
            this.props.setSaveNeeded();
        };

        renderEntry = (entry, index) => (
            <Entry
                key={index}
                id={index}
                value={entry}
                onChange={(nextEntry) => this.emit(this.state.entries.map((current, currentIndex) => currentIndex === index ? nextEntry : current))}
                onDelete={(deleteIndex) => this.emit(this.state.entries.filter((current, currentIndex) => currentIndex !== deleteIndex))}
            />
        );

        startAdding = () => {
            this.setState({draft: {}, showAddEntry: true});
        };

        addDraft = () => {
            this.emit([...this.state.entries, this.state.draft]);
            this.setState({draft: {}, showAddEntry: false});
        };

        cancelAdding = () => {
            this.setState({draft: {}, showAddEntry: false});
        };

        render() {
            return (
                <div>
                    <strong>{title}</strong>
                    {this.state.entries.length === 0 && <div style={styles.emptyState}>You have no entries for this setting yet.</div>}
                    {this.state.entries.map(this.renderEntry)}
                    {this.state.showAddEntry ?
                        <div>
                            <Entry
                                id={this.state.entries.length}
                                value={this.state.draft}
                                hideDelete={true}
                                onChange={(entry) => this.setState({draft: entry})}
                            />
                            <button className='btn btn-primary' onClick={this.addDraft}>Add Entry</button>
                            <button className='btn btn-link' onClick={this.cancelAdding}>Cancel</button>
                        </div> :
                        <a onClick={this.startAdding}><strong>+ Add Entry</strong></a>}
                </div>
            );
        }
    }

    return MatrixSetting;
}

const styles = {
    attributeRow: {
        margin: '12px 0',
        borderBottom: '1px solid #ccc',
        padding: '4px 0 12px',
    },
    deleteIcon: {
        color: '#DB1C34',
        fontSize: '20px',
    },
    emptyState: {
        backgroundColor: 'rgba(0, 0, 0, .04)',
        borderRadius: '4px',
        margin: '8px 0',
        opacity: '0.6',
        padding: '12px',
    },
};

export {FIELD_TYPES, SELECT_TYPES};