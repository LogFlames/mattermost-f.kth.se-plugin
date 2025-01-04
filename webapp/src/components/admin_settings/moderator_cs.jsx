import PropTypes from 'prop-types';
import React from 'react';

import ConfirmModal from '../widgets/confirmation_modal.tsx';

import ModeratorAddAttribute from './moderator_add_attribute.jsx';

import AttributeModerator from './attribute_moderator/attribute_moderator.jsx';

AttributeClass = AttributeModerator;
AddAttributeClass = ModeratorAddAttribute;

export default class ModeratorCS extends AbstractSettings {
    getAttributesList() {
        _ = super.getAttributesList();
        return Array.from(this.state.attributes, ([key, value]) => {
            return (
                <AttributeClass
                    key={key}
                    id={key}
                    is_active={value.IsActive}
                    string1={value.String1}
                    string2={value.String2}
                    string3={value.String3}
                    string4={value.String4}
                    string5={value.String5}
                    bool={value.Bool}
                    int={value.Int}
                    users={value.UserIDs}
                    team={value.TeamID}
                    teams={value.TeamIDs}
                    channels={value.ChannelIDs}
                    groups={value.GroupIDs ? value.GroupIDs.join(' ') : ''}
                    onChange={this.handleChange}
                    onDelete={this.triggerDeleteModal}
                />
            );
        });
    }

    render() {
        return (
            <div>
                <strong>{'Entries'}</strong>
                <div>
                    {this.getAttributesList()}
                    <AddAttributeClass
                        onChange={this.handleChange}
                        id={this.state.attributes.size}
                    />
                </div>
                <ConfirmModal
                    show={this.state.showDeleteModal}
                    title={'Delete Entry'}
                    message={
                        'Are you sure you want to remove the entry?'
                    }
                    confirmButtonText={'Delete entry'}
                    onConfirm={() => {
                        this.handleDelete(this.state.deleteModalData.id);
                    }}
                    onCancel={() => this.setState({showDeleteModal: false})}
                />
            </div>
        );
    }
}