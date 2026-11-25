import {ChannelTypes} from 'mattermost-redux/action_types';
import {batchActions} from 'mattermost-redux/types/actions';

// Server-initiated team leaves bypass the webapp's per-channel leave cleanup.
export function registerTeamLeaveCleanup(registry, store) {
    registry.registerWebSocketEventHandler('leave_team', (event) => {
        const {user_id: userID, team_id: teamID} = event.data;
        const state = store.getState();
        if (!userID || userID !== state.entities.users.currentUserId || !teamID) {
            return;
        }

        const {channels, channelsInTeam, myMembers} = state.entities.channels;
        const actions = [];
        const leftChannels = new Set();
        for (const member of Object.values(myMembers)) {
            const channel = channels[member.channel_id];
            if (channel && channel.team_id === teamID) {
                actions.push({type: ChannelTypes.LEAVE_CHANNEL, data: channel});
                leftChannels.add(channel.id);
            }
        }

        // The native team-leave handler clears posts for all known channels,
        // including channels that are no longer in myMembers.
        for (const channelId of channelsInTeam[teamID] || []) {
            if (!leftChannels.has(channelId)) {
                actions.push({type: 'RESET_POSTS_IN_CHANNEL', channelId});
            }
        }
        if (state.entities.teams.currentTeamId === teamID) {
            actions.push({type: ChannelTypes.SELECT_CHANNEL, data: ''});
        }
        if (actions.length) {
            store.dispatch(batchActions(actions));
        }
    });
}
