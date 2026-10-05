import React, {useEffect, useMemo, useRef, useState} from 'react';
import PropTypes from 'prop-types';

export function dailySeries(data, start, end) {
    const dates = [];
    for (
        let day = new Date(`${start}T00:00:00Z`);
        day.toISOString().slice(0, 10) <= end;
        day.setUTCDate(day.getUTCDate() + 1)
    ) {
        dates.push(day.toISOString().slice(0, 10));
    }
    const values = new Map(data.days.map((day) => [`${day.user_id}:${day.date}`, day]));
    const users = data.users.map((user, index) => {
        let messages = 0;
        let characters = 0;
        return {
            ...user,
            color: `hsl(${(215 + (index * 137.508)) % 360}, 65%, 47%)`,
            points: dates.map((date) => {
                const day = values.get(`${user.id}:${date}`) || {messages: 0, characters: 0};
                messages += day.messages;
                characters += day.characters;
                return {
                    ...day,
                    cumulativeMessages: messages,
                    cumulativeCharacters: characters,
                    average: day.messages ? day.characters / day.messages : null,
                };
            }),
        };
    });
    return {dates, users};
}

const metrics = [
    {key: 'messages', cumulativeKey: 'cumulativeMessages', title: 'Messages over time', description: 'Messages per user, per day.'},
    {
        key: 'characters',
        cumulativeKey: 'cumulativeCharacters',
        title: 'Characters over time',
        description: 'Total characters per user, per day.',
    },
    {
        key: 'average',
        title: 'Characters per message',
        description:
            'Daily characters ÷ daily messages. Gaps mean no messages, not a zero-length average.',
    },
];

const format = (value) =>
    (value === null ? 'No messages' : value.toLocaleString('en', {maximumFractionDigits: 1}));

export default function StatsCharts({data, start, end, cumulative, setCumulative}) {
    const series = useMemo(() => dailySeries(data, start, end), [data, start, end]);
    const [hover, setHover] = useState(null);
    const [hidden, setHidden] = useState([]);
    const [highlight, setHighlight] = useState(null);
    const visible = series.users.filter((user) => !hidden.includes(user.id));
    return (
        <>
            <div
                className='stats-display'
                role='group'
                aria-label='Messages and characters display'
            >
                <button
                    type='button'
                    aria-pressed={cumulative}
                    onClick={() => setCumulative(true)}
                >
                    {'Cumulative total'}
                </button>
                <button
                    type='button'
                    aria-pressed={!cumulative}
                    onClick={() => setCumulative(false)}
                >
                    {'Per day'}
                </button>
            </div>
            <div
                className='stats-legend'
                aria-label='Toggle user lines'
            >
                {series.users.map((user) => (
                    <button
                        key={user.id}
                        type='button'
                        aria-pressed={!hidden.includes(user.id)}
                        onClick={() =>
                            setHidden(
                                hidden.includes(user.id) ?
                                    hidden.filter((id) => id !== user.id) :
                                    [...hidden, user.id],
                            )
                        }
                        onMouseEnter={() => setHighlight(user.id)}
                        onMouseLeave={() => setHighlight(null)}
                        onFocus={() => setHighlight(user.id)}
                        onBlur={() => setHighlight(null)}
                    >
                        <span
                            className='stats-swatch'
                            style={{background: user.color}}
                        />
                        {`@${user.username}`}
                    </button>
                ))}
            </div>
            {metrics.map((metric) => (
                <LineChart
                    key={metric.key}
                    metric={cumulative && metric.cumulativeKey ? {
                        ...metric,
                        key: metric.cumulativeKey,
                        description: `Cumulative ${metric.key} per user from ${start}, through each day.`,
                    } : metric}
                    dates={series.dates}
                    users={visible}
                    hover={hover}
                    setHover={setHover}
                    highlight={highlight}
                />
            ))}
        </>
    );
}

StatsCharts.propTypes = {
    data: PropTypes.object.isRequired,
    start: PropTypes.string.isRequired,
    end: PropTypes.string.isRequired,
    cumulative: PropTypes.bool.isRequired,
    setCumulative: PropTypes.func.isRequired,
};

function LineChart({metric, dates, users, hover, setHover, highlight}) {
    const element = useRef(null);
    const [width, setWidth] = useState(900);
    useEffect(() => {
        const resize = () => setWidth(Math.max(240, element.current.getBoundingClientRect().width));
        resize();
        const observer = new ResizeObserver(resize);
        observer.observe(element.current);
        return () => observer.disconnect();
    }, []);
    const left = 56;
    const right = width - 20;
    const bottom = 215;
    const top = 15;
    let max = 1;
    for (const user of users) {
        for (const point of user.points) {
            max = Math.max(max, point[metric.key] || 0);
        }
    }
    const ceiling = Math.ceil(max / 4) * 4;
    const x = (index) =>
        (dates.length === 1 ?
            (left + right) / 2 :
            left + ((index / (dates.length - 1)) * (right - left)));
    const y = (value) => bottom - ((value / ceiling) * (bottom - top));
    const path = (points) => {
        let penDown = false;
        return points.
            map((point, index) => {
                const value = point[metric.key];
                if (value === null) {
                    penDown = false;
                    return '';
                }
                const segment = `${penDown ? 'L' : 'M'}${x(index)},${y(value)}`;
                penDown = true;
                return segment;
            }).
            join(' ');
    };
    const move = (event) => {
        const rect = event.currentTarget.getBoundingClientRect();
        const index = Math.round(
            ((event.clientX - rect.left - left) / (right - left)) * (dates.length - 1),
        );
        setHover(Math.max(0, Math.min(dates.length - 1, index)));
    };
    const keyboard = (event) => {
        if (event.key === 'Escape') {
            setHover(null);
        } else if (['ArrowLeft', 'ArrowRight', 'Home', 'End'].includes(event.key)) {
            event.preventDefault();
            let index = (hover === null ? 0 : hover) + (event.key === 'ArrowRight' ? 1 : -1);
            if (event.key === 'Home') {
                index = 0;
            } else if (event.key === 'End') {
                index = dates.length - 1;
            }
            setHover(Math.max(0, Math.min(dates.length - 1, index)));
        }
    };
    const tickEvery = Math.max(1, Math.ceil((dates.length - 1) / (width < 500 ? 2 : 6)));
    const hoverUsers = hover === null ? [] : [...users].sort((a, b) =>
        ((b.points[hover][metric.key] ?? -1) - (a.points[hover][metric.key] ?? -1)) ||
        a.username.localeCompare(b.username),
    );
    return (
        <article className='stats-card'>
            <h2>{metric.title}</h2>
            <p className='stats-muted stats-chart-subtitle'>{metric.description}</p>
            <div
                ref={element}
                className='stats-chart'
                onPointerLeave={() => setHover(null)}
            >
                <svg
                    viewBox={`0 0 ${width} 250`}
                    role='img'
                    aria-label={`${metric.title}. Use left and right arrows to explore daily values; Escape clears the tooltip.`}
                    tabIndex={0}
                    onPointerMove={move}
                    onKeyDown={keyboard}
                    onBlur={() => setHover(null)}
                >
                    {[0, 1, 2, 3, 4].map((tick) => (
                        <g key={tick}>
                            <line
                                x1={left}
                                x2={right}
                                y1={y((ceiling * tick) / 4)}
                                y2={y((ceiling * tick) / 4)}
                                stroke='currentColor'
                                opacity={0.1}
                            />
                            <text
                                x={left - 10}
                                y={y((ceiling * tick) / 4) + 4}
                                textAnchor='end'
                            >
                                {new Intl.NumberFormat('en', {
                                    notation: 'compact',
                                    maximumFractionDigits: 1,
                                }).format((ceiling * tick) / 4)}
                            </text>
                        </g>
                    ))}
                    {dates.map(
                        (date, index) =>
                            index % tickEvery === 0 && (
                                <text
                                    key={date}
                                    x={x(index)}
                                    y={242}
                                    textAnchor='middle'
                                >
                                    {new Date(`${date}T00:00:00Z`).toLocaleDateString('en', {
                                        month: 'short',
                                        day: 'numeric',
                                        ...(dates[0].slice(0, 4) === dates[dates.length - 1].slice(0, 4) ? {} : {year: 'numeric'}),
                                        timeZone: 'UTC',
                                    })}
                                </text>
                            ),
                    )}
                    {users.map((user) => (
                        <g
                            key={user.id}
                            opacity={highlight && highlight !== user.id ? 0.18 : 1}
                        >
                            <path
                                data-user={user.id}
                                d={path(user.points)}
                                fill='none'
                                stroke={user.color}
                                strokeWidth={highlight === user.id ? 3 : 2}
                                strokeLinejoin='round'
                            />
                            {user.points.map(
                                (point, index) =>
                                    point[metric.key] !== null &&
                                    (dates.length <= 31 || (metric.key === 'average' && !user.points[index - 1]?.messages && !user.points[index + 1]?.messages)) && (
                                        <circle
                                            key={dates[index]}
                                            cx={x(index)}
                                            cy={y(point[metric.key])}
                                            r={2.5}
                                            fill={user.color}
                                        />
                                    ),
                            )}
                        </g>
                    ))}
                    {hover !== null && (
                        <g>
                            <line
                                x1={x(hover)}
                                x2={x(hover)}
                                y1={top}
                                y2={bottom}
                                stroke='currentColor'
                                opacity={0.35}
                                strokeDasharray='4 4'
                            />
                            {users.map(
                                (user) =>
                                    user.points[hover]?.[metric.key] !== null && (
                                        <circle
                                            key={user.id}
                                            cx={x(hover)}
                                            cy={y(user.points[hover][metric.key])}
                                            r={4}
                                            fill={user.color}
                                            stroke='var(--center-channel-bg, white)'
                                            strokeWidth={2}
                                        />
                                    ),
                            )}
                        </g>
                    )}
                </svg>
                {hover !== null && (
                    <div
                        className='stats-tooltip'
                        role='status'
                        style={{
                            left: Math.max(
                                0,
                                Math.min(
                                    width - 215,
                                    x(hover) + (x(hover) > width / 2 ? -230 : 15),
                                ),
                            ),
                        }}
                    >
                        <strong>{`${dates[hover]} · UTC`}</strong>
                        {hoverUsers.map((user) => (
                            <div
                                className='stats-tooltip-row'
                                key={user.id}
                            >
                                <span
                                    className='stats-swatch'
                                    style={{background: user.color}}
                                />
                                <span>{`@${user.username}`}</span>
                                <strong>{format(user.points[hover][metric.key])}</strong>
                            </div>
                        ))}
                    </div>
                )}
            </div>
        </article>
    );
}

LineChart.propTypes = {
    metric: PropTypes.object.isRequired,
    dates: PropTypes.array.isRequired,
    users: PropTypes.array.isRequired,
    hover: PropTypes.number,
    setHover: PropTypes.func.isRequired,
    highlight: PropTypes.string,
};
