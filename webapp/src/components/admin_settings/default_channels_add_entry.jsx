import AbstractAddEntry from './abstract_add_entry.jsx';

export default class DefaultChannelsAddEntry extends AbstractAddEntry {
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
                <DefaultChannels
                    is_active={this.props.is_active}
                    string1={this.props.string1}
                    string2={this.props.string2}
                    string3={this.props.string3}
                    string4={this.props.string4}
                    string5={this.props.string5}
                    bool={this.props.bool}
                    int={this.props.int}
                    users={this.props.users}
                    team={this.props.team}
                    teams={this.props.teams}
                    groups={this.props.groups}
                    channels={this.props.channels}
                    onChange={this.onInput}
                    hideDelete={true}
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
