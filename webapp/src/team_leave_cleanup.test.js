import {registerTeamLeaveCleanup} from './team_leave_cleanup';

function setup(channelsInTeam) {
    const state = {
        entities: {
            users: {currentUserId: 'me'},
            teams: {currentTeamId: 'otherTeam'},
            channels: {
                channelsInTeam,
                channels: {
                    town: {id: 'town', team_id: 'team', type: 'O'},
                    off: {id: 'off', team_id: 'team', type: 'O'},
                    other: {id: 'other', team_id: 'otherTeam', type: 'O'},
                    direct: {id: 'direct', team_id: '', type: 'D'},
                },
                myMembers: {
                    town: {channel_id: 'town'},
                    other: {channel_id: 'other'},
                    direct: {channel_id: 'direct'},
                },
                currentChannelId: 'other',
            },
            posts: {postsInChannel: {
                town: [{order: ['stale']}],
                off: [{order: ['stale-off']}],
                other: [{order: ['keep']}],
            }},
        },
    };
    const registry = {registerWebSocketEventHandler: jest.fn()};
    const store = {
        getState: () => state,
        dispatch: jest.fn((batch) => {
            for (const action of batch.payload) {
                if (action.type === 'LEAVE_CHANNEL') {
                    delete state.entities.channels.myMembers[action.data.id];
                    delete state.entities.posts.postsInChannel[action.data.id];
                } else if (action.type === 'RESET_POSTS_IN_CHANNEL') {
                    delete state.entities.posts.postsInChannel[action.channelId];
                } else if (action.type === 'SELECT_CHANNEL') {
                    state.entities.channels.currentChannelId = action.data;
                }
            }
        }),
    };
    registerTeamLeaveCleanup(registry, store);
    expect(registry.registerWebSocketEventHandler).toHaveBeenCalledWith('leave_team', expect.any(Function));
    return {state, store, handle: registry.registerWebSocketEventHandler.mock.calls[0][1]};
}

test.each([
    ['set', new Set(['town', 'off'])],
    ['array', ['town', 'off']],
])('cleans up only the leaving user team channels (%s)', (_, channels) => {
    const {state, store, handle} = setup({team: channels, otherTeam: ['other']});

    handle({data: {user_id: 'me', team_id: 'team'}});

    expect(store.dispatch).toHaveBeenCalledTimes(1);
    expect(store.dispatch.mock.calls[0][0]).toEqual({
        type: 'BATCHING_REDUCER.BATCH',
        meta: {batch: true},
        payload: [
            {type: 'LEAVE_CHANNEL', data: state.entities.channels.channels.town},
            {type: 'RESET_POSTS_IN_CHANNEL', channelId: 'off'},
        ],
    });
    expect(state.entities.posts.postsInChannel).toEqual({other: [{order: ['keep']}]});
    expect(state.entities.channels.myMembers).toEqual({
        other: {channel_id: 'other'},
        direct: {channel_id: 'direct'},
    });
    expect(state.entities.channels.currentChannelId).toBe('other');
});

test('deselects the channel only if the removed team is still active', () => {
    const {state, store, handle} = setup({team: new Set(['town'])});
    state.entities.teams.currentTeamId = 'team';
    state.entities.channels.currentChannelId = 'town';
    handle({data: {user_id: 'me', team_id: 'team'}});
    expect(store.dispatch.mock.calls[0][0].payload).toContainEqual({type: 'SELECT_CHANNEL', data: ''});
    expect(state.entities.channels.currentChannelId).toBe('');
});

test('does not clear post lists when someone else leaves', () => {
    const {state, store, handle} = setup({team: new Set(['town'])});
    handle({data: {user_id: 'someone-else', team_id: 'team'}});
    expect(store.dispatch).not.toHaveBeenCalled();
    expect(state.entities.posts.postsInChannel.town).toBeDefined();
});

test('does not reset all channels for a missing or unknown team', () => {
    const {store, handle} = setup({team: new Set(['town'])});
    handle({data: {user_id: 'me'}});
    handle({data: {user_id: 'me', team_id: 'unknown'}});
    expect(store.dispatch).not.toHaveBeenCalled();
});
