import AbstractEntry from '../abstract_entry/abstract_entry.jsx';

export default class ModeratorEntry extends AbstractEntry {
    render() {
        let errorLabel = null;
        if (this.state.error) {
            errorLabel = this.state.error;
        }

        return (
            <div
                style={styles.attributeRow}
            >
                <div>
                    <div className=''>
                        <ChannelsInput
                            placeholder='~channel1 ~channel2'
                            channels={this.state.channels}
                            onChange={this.handleChannelsInput}
                            multi={true}
                        />
                    </div>
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
