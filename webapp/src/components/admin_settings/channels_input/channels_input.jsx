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
        actions: PropTypes.shape({
            searchChannels: PropTypes.func.isRequired,
            getTeam: PropTypes.func.isRequired,
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

    async formatOptionLabel(option) {
        if (option.display_name) {
            console.log(option)
            const team = await this.props.actions.getTeam(option.team_id);
            console.log(team)
            return (
                <React.Fragment>
                    { `${team.data.display_name}/${option.display_name}`}
                </React.Fragment>
            );
        }

        return option;
    };

    searchChannels = debounce((term, callback) => {
        this.props.actions.searchChannels(term).then(({data, error}) => {
            if (error) {
                // eslint-disable-next-line no-console
                console.error('Error searching channel in custom attribute settings dropdown. ' + error.message);
                callback([]);
                return;
            }

            callback(data);
        });
    }, 150);

    render() {
        return (
            <AsyncSelect
                isMulti={true}
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
