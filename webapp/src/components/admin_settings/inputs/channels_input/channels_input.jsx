import React from 'react';
import PropTypes from 'prop-types';

import debounce from 'lodash/debounce';
import AsyncSelect from 'react-select/async';

// ChannelsInput searches and selects channels displayed by display_name.
// Channels prop can handle the channel object or strings directly if the channel object is not available.
// Returns the selected channel ids in the `OnChange` value parameter.
export default class ChannelsInput extends React.PureComponent {
    static propTypes = {
        placeholder: PropTypes.string,
        channels: PropTypes.array,
        onChange: PropTypes.func,
        isMulti: PropTypes.bool,
        actions: PropTypes.shape({
            searchChannels: PropTypes.func.isRequired,
            getTeams: PropTypes.func.isRequired,
        }).isRequired,
    };

    onChange = (value) => {
        if (this.props.onChange) {
            this.props.onChange(value);
        }
    };

    getOptionValue = (channel) => {
        if (channel.id) {
            return channel.id;
        }

        return channel;
    };

    formatOptionLabel = (option) => {
        if (option.display_name && option.team_display_name) {
            return (
                <React.Fragment>
                    { `${option.team_display_name}/${option.display_name}` }
                </React.Fragment>
            );
        }

        return (<React.Fragment>{"<Invalid option>"}</React.Fragment>);
    };

    searchChannels = debounce((term, callback) => {
        const channelPromise = this.props.actions.searchChannels(term);
        const teamPromise = this.props.actions.getTeams(0, 60);

        Promise.all([channelPromise, teamPromise]).then((values) => {
            const channelsData = values[0].data;
            const channelError = values[0].error;

            if (channelError) {
                // eslint-disable-next-line no-console
                console.error('Error searching channel in dropdown. ' + channelError.message);
                callback([]);
                return;
            }

            const teamsData = values[1].data;
            const teamError = values[1].error;

            if (teamError) {
                // eslint-disable-next-line no-console
                console.error('Error getting team for channel in dropdown. ' + teamError.message);
                callback([]);
                return;
            }

            // eslint-disable-next-line max-nested-callbacks
            const data = channelsData.map((channel) => {
                return {
                    ...channel,
                    // eslint-disable-next-line max-nested-callbacks
                    team_display_name: teamsData.find((team) => team.id === channel.team_id).display_name,
                };
            });

            callback(data);
        });
    }, 150);

    render() {
        return (
            <AsyncSelect
                isMulti={this.props.isMulti}
                cacheOptions={true}
                defaultOptions={false}
                loadOptions={this.searchChannels}
                onChange={this.onChange}
                getOptionValue={this.getOptionValue}
                formatOptionLabel={this.formatOptionLabel}
                defaultMenuIsOpen={false}
                openMenuOnClick={false}
                isClearable={false}
                placeholder={this.props.placeholder}
                value={this.props.channels}
                components={{DropdownIndicator: () => null, IndicatorSeparator: () => null}}
                styles={customStyles}
            />
        );
    }
}

const customStyles = {
    control: (provided) => ({
        ...provided,
        minHeight: 34,
    }),
};
