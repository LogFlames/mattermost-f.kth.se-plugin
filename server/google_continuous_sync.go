package main

import (
	"context"
	"fmt"
	"math"
	"time"
)

type googleContinuousSyncState struct {
	Cursor      string `json:"cursor"`
	CompletedAt int64  `json:"completed_at"`
}

func googleContinuousSyncOrder(record googleGroupSyncRecord) string {
	if record.Job.ChannelID == "" {
		return "0" + record.Key
	}
	return "1" + record.Key
}

func googleGroupSyncCycleDescription(records []googleGroupSyncRecord, config *configuration, now time.Time) (string, int64, error) {
	if config.GoogleGroupsSyncMode != "continuous" {
		return "Sync cycle: 1 hour (hourly schedule).", 0, nil
	}
	if config.GoogleGroupsSyncDelaySeconds <= 0 {
		return "", 0, fmt.Errorf("invalid continuous Google Group sync delay")
	}
	var active int64
	for _, record := range records {
		if record.Job.DeadlineAt > now.UnixMilli() {
			active++
		}
	}
	if active == 0 {
		return "Sync cycle: no active team or channel syncs.", 0, nil
	}
	delay := int64(config.GoogleGroupsSyncDelaySeconds)
	if active > math.MaxInt64/delay {
		return "", 0, fmt.Errorf("continuous Google Group sync cycle is too long to display")
	}
	seconds := active * delay
	return fmt.Sprintf("Minimum sync cycle: %d seconds, plus operation time (based on active team and channel syncs across all teams).", seconds), seconds, nil
}

func (p *Plugin) runContinuousGoogleGroupSyncs(ctx context.Context, wake <-chan struct{}) {
	for {
		var wait time.Duration
		if p.getConfiguration().GoogleGroupsSyncMode == "continuous" {
			wait = p.runGoogleGroupSyncJobsMode(ctx, true)
		} else {
			wait = 30 * time.Second
		}
		if ctx.Err() != nil {
			return
		}
		if wait <= 0 {
			continue
		}
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-wake:
			timer.Stop()
		case <-timer.C:
		}
	}
}
