export function dateRange(days, now = new Date()) {
    const end = now.toISOString().slice(0, 10);
    const start = new Date(`${end}T00:00:00Z`);
    start.setUTCDate(start.getUTCDate() - (days - 1));
    return {start: start.toISOString().slice(0, 10), end};
}

export function readStatsFilters(search) {
    const params = new URLSearchParams(search);
    const scope = params.get('scope') || 'all';
    const usersMode = params.get('users_mode') || 'choose';
    const preset = params.get('range') || 'all';
    return {
        scope: ['all', 'team', 'channel'].includes(scope) ? scope : 'all',
        team: params.get('team_id') || '',
        channel: params.get('channel_id') || '',
        includeDMs: params.get('include_dms') === 'true',
        usersMode: ['choose', 'channel', 'thread'].includes(usersMode) ? usersMode : 'choose',
        userIDs: params.has('user_ids') ? [...new Set(params.get('user_ids').split(',').filter(Boolean))] : null,
        memberChannel: params.get('member_channel') || '',
        thread: params.get('thread') || '',
        preset: ['all', '7', '30', '90', '365', 'custom'].includes(preset) ? preset : 'all',
        range: params.has('start') && params.has('end') ? {
            start: params.get('start'),
            end: params.get('end'),
        } : dateRange(['7', '30', '90', '365'].includes(preset) ? Number(preset) : 30),
        cumulative: params.get('display') !== 'daily',
    };
}

export function statsFilterQuery(request, preset, cumulative) {
    const params = new URLSearchParams();
    for (const [key, value] of Object.entries(request)) {
        if (Array.isArray(value)) {
            params.set(key, value.join(','));
        } else if (value !== '' && value !== false) {
            params.set(key, String(value));
        }
    }
    params.set('range', preset);
    params.set('display', cumulative ? 'cumulative' : 'daily');
    return params.toString();
}
