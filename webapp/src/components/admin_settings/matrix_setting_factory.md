<!-- Written by GPT-5.6 Luna -->

# Matrix setting factory

`matrix_setting_factory.jsx` creates an Admin Console custom setting from a list of named fields.

```jsx
import createMatrixSetting from '../matrix_setting_factory.jsx';

const MessageSettings = createMatrixSetting({
    title: 'Messages',
    fields: [
        {name: 'messageString', type: 'string', placeholder: 'Message'},
        {name: 'enabled', type: 'boolean', label: 'Enabled'},
        {name: 'team', type: 'teams', isMulti: false},
        {name: 'channels', type: 'channels', isMulti: true},
        {name: 'recipients', type: 'users', isMulti: true},
    ],
});
```

Supported field types are `string`, `number`, `int`, `boolean`, `bool`, `channels`, `teams`, and `users`.
Select fields are hydrated for display through Mattermost Redux actions and are emitted as IDs. The generated component calls the Admin Console `onChange(settingID, entries)` callback with the configured field names instead of positional names such as `String1` or `String2`.