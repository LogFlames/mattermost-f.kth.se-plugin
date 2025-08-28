import AbstractEntry from '../abstract_entry/abstract_entry.jsx';

export default class DefaultChannels extends AbstractEntry {
    render() {
        return super.render(
            enableString1=1,
            string1Name='Enter a category name',
            enableChannels=1,
        );
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
