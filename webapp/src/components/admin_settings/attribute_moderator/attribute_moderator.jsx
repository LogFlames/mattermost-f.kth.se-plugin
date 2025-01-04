import React from 'react';
import AbstractAttribute from '../abstract_attribute/abstract_attribute';

import UsersInput from '../users_input';
import TeamsInput from '../teams_input';
import ChannelsInput from '../channels_input';

export default class AttributeModerator extends AbstractAttribute {
    render() {
        let deleteButton = null;
        if (!this.props.hideDelete) {
            deleteButton = (
                <div
                    className='col-xs-12 col-sm-1'
                >
                    <a
                        style={styles.deleteIcon}
                        onClick={this.handleDelete}
                    >
                        <i className='fa fa-trash'/>
                    </a>
                </div>
            );
        }

        let errorLabel = null;
        if (this.state.error) {
            errorLabel = this.state.error;
        }

        return (
            <div
                style={styles.attributeRow}
            >
                <div
                    className='row'
                >
                    <div className='col-xs-12 col-sm-3'>
                        <ChannelsInput
                            placeholder='~channel1 ~channel2'
                            channels={this.state.channels}
                            onChange={this.handleChannelsInput}
                        />
                    </div>
                    {deleteButton}
                </div>
                <div style={styles.errorLabel}>
                    {errorLabel}
                </div>
            </div>);
    }
}

const styles = {
    attributeRow: {
        margin: '12px 0',
        borderBottom: '1px solid #ccc',
        padding: '4px 0 12px',
    },
    deleteIcon: {
        textDecoration: 'none',
        height: '20px',
        width: '24px',
        color: '#DB1C34',
        fontFamily: 'material',
        fontSize: '20px',
        lineHeight: '32px',
        margin: '0 0 0 -12px',
    },
    errorLabel: {
        margin: '8px 0 0',
        color: '#EB5757',
    },
};
