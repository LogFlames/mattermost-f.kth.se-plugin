import createMatrixSetting from '../matrix_setting_factory.jsx';

export default createMatrixSetting({
    title: 'Default Channels: Default Channels and Categories',
    fields: [
        {
            name: 'String1',
            type: 'string',
            placeholder: 'categoryName',
        },
        {
            name: 'ChannelIDs',
            type: 'channels',
            isMulti: true,
            placeholder: '~channel1 ~channel2',
        },
    ],
});
