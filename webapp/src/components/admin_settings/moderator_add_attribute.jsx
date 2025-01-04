import AbstractAddAttribute from './abstract_add_attribute.jsx';
import AttributeModerator from './attribute_moderator/attribute_moderator.jsx';

export default class ModeratorAddAttribute extends AbstractAddAttribute {
    render() {
        if (this.state.collapsed) {
            return (
                <div>
                    <a
                        style={styles.addLink}
                        onClick={() => {
                            this.setState({collapsed: false});
                        }}
                    ><strong>{'+ Add Entry'}</strong></a>
                </div>
            );
        }

        let errorBanner = null;
        if (this.state.error) {
            errorBanner = (
                <div style={styles.alertDiv}>
                    <p style={styles.alertText}> {'Something is wrong with your input.'}
                    </p>
                </div>
            );
        }

        return (
            <div>
                <AttributeModerator
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

                <div
                    className='row'
                    style={styles.buttonRow}
                >
                    <div className='col-sm-12'>
                        <button
                            className='btn btn-primary'
                            style={styles.buttonBorder}
                            onClick={this.handleSave}
                        >
                            {'Add Entry'}
                        </button>
                        <button
                            className='btn btn-link'
                            onClick={this.handleCancel}
                        >
                            <a
                                style={styles.button}
                            >
                                {'Cancel'}
                            </a>
                        </button>
                    </div>
                </div>
                {errorBanner}
            </div>

        );
    }
}

const styles = {
    buttonRow: {
        marginTop: '8px',
    },
};
