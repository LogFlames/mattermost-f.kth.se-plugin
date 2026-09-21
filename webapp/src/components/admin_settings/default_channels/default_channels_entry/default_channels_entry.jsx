import AbstractEntry from '../../abstract/abstract_entry/abstract_entry.jsx';
import ChannelsInput from '../../inputs/channels_input/index.jsx';

export default class DefaultChannelsEntry extends AbstractEntry {
    render() {
        return (
            <div
                style={styles.attributeRow}
            >
                <ChannelsInput
                    placeholder='Search for channels'
                    channels={this.state.channels}
                    onChange={this.handleChannelsInput}
                    isMulti={true}
                />
                {this.state.error && (
                    <div style={styles.errorLabel}>
                        {this.state.error}
                    </div>
                )}
            </div>);
    }
}

const styles = {
    attributeRow: {
        margin: '12px 0',
        borderBottom: '1px solid #ccc',
        padding: '4px 0 12px',
    },
    errorLabel: {
        margin: '8px 0 0',
        color: '#EB5757',
    },
};
