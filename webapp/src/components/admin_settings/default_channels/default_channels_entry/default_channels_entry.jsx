import AbstractEntry from '../abstract/abstract_entry/abstract_entry.jsx';
import ChannelsInput from '../../inputs/channels_input/index.jsx';

export default class DefaultChannelsEntry extends AbstractEntry {
    render() {
        let errorLabel = null;
        if (this.state.error) {
            errorLabel = this.state.error;
        }

        return (
            <div
                style={styles.attributeRow}
            >
                <div className='col-xs-12 col-sm-3'>
                <input
                    id={`string1-${this.props.id}`}
                    className='form-control'
                    type='text'
                    placeholder='categoryName'
                    value={this.state.string1}
                    onChange={this.handleString1Input}
                />
                </div>
                <div className='col-xs-12 col-sm-3'>
                    <ChannelsInput
                        placeholder='~channel1 ~channel2'
                        channels={this.state.channels}
                        onChange={this.handleChannelsInput}
                        multi={true}
                    />
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
