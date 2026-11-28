package main

import (
	"context"
	"encoding/json"
	"math"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mattermost/mattermost/server/public/model"
)

func TestGoogleGroupSyncCycleDescription(t *testing.T) {
	now := time.Now()
	records := []googleGroupSyncRecord{
		{Job: googleGroupSync{DeadlineAt: now.Add(time.Hour).UnixMilli()}},
		{Job: googleGroupSync{DeadlineAt: now.Add(time.Hour).UnixMilli(), ChannelID: "channel"}},
		{Job: googleGroupSync{DeadlineAt: now.UnixMilli()}},
	}
	config := &configuration{GoogleGroupsSyncMode: "continuous", GoogleGroupsSyncDelaySeconds: 10}
	cycle, seconds, err := googleGroupSyncCycleDescription(records, config, now)
	if err != nil || seconds != 20 || !strings.Contains(cycle, "20 seconds") || !strings.Contains(cycle, "plus operation time") {
		t.Fatalf("continuous cycle: %q, %v", cycle, err)
	}
	cycle, _, err = googleGroupSyncCycleDescription(records[2:], config, now)
	if err != nil || !strings.Contains(cycle, "no active") {
		t.Fatalf("empty cycle: %q, %v", cycle, err)
	}
	config.GoogleGroupsSyncMode = "hourly"
	cycle, _, err = googleGroupSyncCycleDescription(records, config, now)
	if err != nil || !strings.Contains(cycle, "1 hour") {
		t.Fatalf("hourly cycle: %q, %v", cycle, err)
	}
	config.GoogleGroupsSyncMode = "continuous"
	config.GoogleGroupsSyncDelaySeconds = 0
	if _, _, err := googleGroupSyncCycleDescription(records, config, now); err == nil {
		t.Fatal("accepted invalid delay")
	}
	maxInt := int(^uint(0) >> 1)
	if int64(maxInt) > math.MaxInt64/2 {
		config.GoogleGroupsSyncDelaySeconds = maxInt
		if _, _, err := googleGroupSyncCycleDescription(records, config, now); err == nil {
			t.Fatal("accepted overflowing cycle duration")
		}
	}
}

type continuousSyncTestAPI struct {
	*googleGroupSyncTestAPI
	attempts     chan string
	attemptTimes chan time.Time
}

type lockedContinuousSyncTestAPI struct {
	*continuousSyncTestAPI
	mu     sync.Mutex
	listed chan struct{}
}

func (a *lockedContinuousSyncTestAPI) KVGet(key string) ([]byte, *model.AppError) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.categoryTestAPI.KVGet(key)
}

func (a *lockedContinuousSyncTestAPI) KVSet(key string, value []byte) *model.AppError {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.categoryTestAPI.KVSet(key, value)
}

func (a *lockedContinuousSyncTestAPI) KVSetWithOptions(key string, value []byte, options model.PluginKVSetOptions) (bool, *model.AppError) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.categoryTestAPI.KVSetWithOptions(key, value, options)
}

func (a *lockedContinuousSyncTestAPI) KVDelete(key string) *model.AppError {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.categoryTestAPI.KVDelete(key)
}

func (a *lockedContinuousSyncTestAPI) KVList(page, perPage int) ([]string, *model.AppError) {
	a.mu.Lock()
	defer a.mu.Unlock()
	keys, err := a.categoryTestAPI.KVList(page, perPage)
	select {
	case a.listed <- struct{}{}:
	default:
	}
	return keys, err
}

func (a *continuousSyncTestAPI) LogError(message string, fields ...any) {
	if message == "Scheduled Google Group sync creator is no longer an admin" {
		if a.attemptTimes != nil {
			a.attemptTimes <- time.Now()
		}
		a.attempts <- fields[1].(string)
	}
}

func TestContinuousGoogleSyncRotationAndSpacing(t *testing.T) {
	base := &categoryTestAPI{kv: map[string][]byte{}}
	api := &continuousSyncTestAPI{
		googleGroupSyncTestAPI: &googleGroupSyncTestAPI{categoryTestAPI: base},
		attempts:               make(chan string, 10),
	}
	p := &Plugin{configuration: &configuration{GoogleGroupsSyncMode: "continuous", GoogleGroupsSyncDelaySeconds: 10}}
	p.SetAPI(api)
	for _, job := range []googleGroupSync{
		{GroupEmail: "channel", TeamID: "team", ChannelID: "channel", DeadlineAt: time.Now().Add(time.Hour).UnixMilli(), LastSyncedAt: time.Now().UnixMilli()},
		{GroupEmail: "team", TeamID: "team", DeadlineAt: time.Now().Add(time.Hour).UnixMilli(), LastSyncedAt: time.Now().UnixMilli()},
	} {
		data, err := json.Marshal(job)
		if err != nil {
			t.Fatal(err)
		}
		base.kv[googleGroupSyncKey(job.TeamID, job.ChannelID, job.GroupEmail)] = data
	}

	if wait := p.runGoogleGroupSyncJobsMode(context.Background(), true); wait != 0 {
		t.Fatalf("first run wait: %v", wait)
	}
	if got := <-api.attempts; got != "team" {
		t.Fatalf("first attempt = %q, want team", got)
	}
	other := &Plugin{configuration: &configuration{GoogleGroupsSyncMode: "continuous", GoogleGroupsSyncDelaySeconds: 10}}
	other.SetAPI(api)
	wait := other.runGoogleGroupSyncJobsMode(context.Background(), true)
	if wait < 9*time.Second || wait > 10*time.Second {
		t.Fatalf("delay after failed operation = %v, want about 10s", wait)
	}
	select {
	case got := <-api.attempts:
		t.Fatalf("attempt %q ran before delay elapsed", got)
	default:
	}

	var state googleContinuousSyncState
	if err := json.Unmarshal(base.kv[googleGroupContinuousStateKey], &state); err != nil {
		t.Fatal(err)
	}
	state.CompletedAt = time.Now().Add(-3 * time.Second).UnixNano()
	data, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	base.kv[googleGroupContinuousStateKey] = data
	p.setConfiguration(&configuration{GoogleGroupsSyncMode: "continuous", GoogleGroupsSyncDelaySeconds: 2})
	p.runGoogleGroupSyncJobsMode(context.Background(), true)
	if got := <-api.attempts; got != "channel" {
		t.Fatalf("second attempt = %q, want channel", got)
	}
	p.runGoogleGroupSyncJobs(context.Background())
	select {
	case got := <-api.attempts:
		t.Fatalf("hourly worker ran team/channel sync %q in continuous mode", got)
	default:
	}
	if err := json.Unmarshal(base.kv[googleGroupContinuousStateKey], &state); err != nil {
		t.Fatal(err)
	}
	state.CompletedAt = time.Now().Add(-3 * time.Second).UnixNano()
	data, err = json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	base.kv[googleGroupContinuousStateKey] = data
	p.runGoogleGroupSyncJobsMode(context.Background(), true)
	if got := <-api.attempts; got != "team" {
		t.Fatalf("rotation did not wrap: got %q", got)
	}
	p.setConfiguration(&configuration{GoogleGroupsSyncMode: "hourly", GoogleGroupsSyncDelaySeconds: 2})
	p.runGoogleGroupSyncJobsMode(context.Background(), true)
	select {
	case got := <-api.attempts:
		t.Fatalf("continuous attempt %q ran after switching to hourly", got)
	default:
	}
}

func TestContinuousGoogleSyncWorkerWakesOnConfigurationChange(t *testing.T) {
	base := &categoryTestAPI{kv: map[string][]byte{}}
	api := &continuousSyncTestAPI{
		googleGroupSyncTestAPI: &googleGroupSyncTestAPI{categoryTestAPI: base},
		attempts:               make(chan string, 1),
	}
	job := googleGroupSync{GroupEmail: "team", TeamID: "team", DeadlineAt: time.Now().Add(time.Hour).UnixMilli()}
	data, err := json.Marshal(job)
	if err != nil {
		t.Fatal(err)
	}
	base.kv[googleGroupSyncKey(job.TeamID, "", job.GroupEmail)] = data
	p := &Plugin{configuration: &configuration{GoogleGroupsSyncMode: "hourly"}, googleSyncWake: make(chan struct{}, 1)}
	p.SetAPI(api)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		p.runContinuousGoogleGroupSyncs(ctx, p.googleSyncWake)
	}()
	defer func() {
		cancel()
		<-done
	}()
	p.setConfiguration(&configuration{GoogleGroupsSyncMode: "continuous", GoogleGroupsSyncDelaySeconds: 10})
	select {
	case got := <-api.attempts:
		if got != "team" {
			t.Fatalf("attempt = %q", got)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("worker did not wake on configuration change")
	}
}

func TestContinuousGoogleSyncWorkerWakesWhenAddedAndSpacesRetries(t *testing.T) {
	api := &lockedContinuousSyncTestAPI{
		continuousSyncTestAPI: &continuousSyncTestAPI{
			googleGroupSyncTestAPI: &googleGroupSyncTestAPI{categoryTestAPI: &categoryTestAPI{kv: map[string][]byte{}}},
			attempts:               make(chan string, 3),
			attemptTimes:           make(chan time.Time, 3),
		},
		listed: make(chan struct{}, 1),
	}
	p := &Plugin{
		configuration:  &configuration{GoogleGroupsSyncMode: "continuous", GoogleGroupsSyncDelaySeconds: 1},
		googleSyncWake: make(chan struct{}, 1),
	}
	p.SetAPI(api)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		p.runContinuousGoogleGroupSyncs(ctx, p.googleSyncWake)
	}()
	defer func() {
		cancel()
		<-done
	}()
	select {
	case <-api.listed:
	case <-time.After(3 * time.Second):
		t.Fatal("worker did not check for syncs")
	}
	job := googleGroupSync{GroupEmail: "team", TeamID: "team", DeadlineAt: time.Now().Add(time.Hour).UnixMilli()}
	data, err := json.Marshal(job)
	if err != nil {
		t.Fatal(err)
	}
	if appErr := api.KVSet(googleGroupSyncKey(job.TeamID, "", job.GroupEmail), data); appErr != nil {
		t.Fatal(appErr)
	}
	p.wakeGoogleGroupSync()
	select {
	case <-api.attempts:
	case <-time.After(3 * time.Second):
		t.Fatal("worker did not wake for the new sync")
	}
	first := <-api.attemptTimes
	select {
	case <-api.attempts:
		if elapsed := (<-api.attemptTimes).Sub(first); elapsed < time.Second {
			t.Fatalf("retry started too early: %v", elapsed)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("worker did not retry the sync")
	}
}

func TestGoogleSyncSchedulingConfiguration(t *testing.T) {
	p, api := newDefaultChannelTestPlugin(t)
	cfg := p.getConfiguration()
	if cfg.GoogleGroupsSyncMode != "hourly" || cfg.GoogleGroupsSyncDelaySeconds != 10 {
		t.Fatalf("unexpected defaults: mode=%q delay=%d", cfg.GoogleGroupsSyncMode, cfg.GoogleGroupsSyncDelaySeconds)
	}
	api.settings["GoogleGroupsSyncMode"] = "continuous"
	api.settings["GoogleGroupsSyncDelaySeconds"] = 2
	if err := p.OnConfigurationChange(); err != nil {
		t.Fatal(err)
	}
	for _, invalid := range []struct {
		mode  string
		delay int
	}{{"unknown", 2}, {"continuous", 0}, {"continuous", -1}} {
		api.settings["GoogleGroupsSyncMode"] = invalid.mode
		api.settings["GoogleGroupsSyncDelaySeconds"] = invalid.delay
		if err := p.OnConfigurationChange(); err == nil {
			t.Fatalf("accepted invalid settings: %+v", invalid)
		}
		if p.getConfiguration().GoogleGroupsSyncMode != "continuous" || p.getConfiguration().GoogleGroupsSyncDelaySeconds != 2 {
			t.Fatal("invalid configuration replaced active settings")
		}
	}
}
