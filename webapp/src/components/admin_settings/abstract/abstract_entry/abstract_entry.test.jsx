import AbstractEntry from './abstract_entry';

it('reports a team lookup failure without throwing while loading channels', async () => {
    const entry = Object.create(AbstractEntry.prototype);
    entry.props = {
        channels: ['channel'],
        actions: {
            getChannel: jest.fn().mockResolvedValue({data: {id: 'channel'}}),
            getTeams: jest.fn().mockResolvedValue({error: 'team lookup failed'}),
        },
    };
    entry.setState = jest.fn();
    const logError = jest.spyOn(console, 'error').mockReturnValue();

    try {
        await expect(entry.initChannels()).resolves.toBeUndefined();
        expect(logError).toHaveBeenCalledWith('Error getting team for channel in setting. team lookup failed');
        expect(entry.setState).not.toHaveBeenCalled();
    } finally {
        logError.mockRestore();
    }
});
