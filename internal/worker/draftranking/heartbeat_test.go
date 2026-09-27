package draftranking

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	// testPulseInterval is short so ticks arrive quickly; testPulseWait
	// bounds how long a test waits for one.
	testPulseInterval = 5 * time.Millisecond
	testPulseWait     = 2 * time.Second
	// testRepeatedTicks is how many periodic heartbeats a test waits for.
	testRepeatedTicks = 3
	testStep          = "projections"
)

// recorder collects heartbeat details on a buffered channel.
func recorder() (chan string, func(string)) {
	records := make(chan string, 64)
	return records, func(step string) { records <- step }
}

func nextRecord(t *testing.T, records <-chan string) string {
	t.Helper()
	select {
	case step := <-records:
		return step
	case <-time.After(testPulseWait):
		require.FailNow(t, "no heartbeat recorded")
		return ""
	}
}

func TestStartPulse_RecordsInitialStepPeriodically(t *testing.T) {
	t.Parallel()
	records, record := recorder()
	_, stop := startPulse(context.Background(), testPulseInterval, record)
	defer stop()

	assert.Equal(t, initialStep, nextRecord(t, records))
}

func TestStartPulse_StepRecordsAtOnceThenRepeats(t *testing.T) {
	t.Parallel()
	records, record := recorder()
	// An interval longer than the test means the first record can only be
	// the one Step sends itself.
	step, stop := startPulse(context.Background(), time.Hour, record)
	step(testStep)
	assert.Equal(t, testStep, nextRecord(t, records))
	stop()

	records, record = recorder()
	step, stop = startPulse(context.Background(), testPulseInterval, record)
	defer stop()
	step(testStep)
	seen := 0
	for seen < testRepeatedTicks {
		if nextRecord(t, records) == testStep {
			seen++
		}
	}
}

func TestStartPulse_StopEndsHeartbeats(t *testing.T) {
	t.Parallel()
	records, record := recorder()
	_, stop := startPulse(context.Background(), testPulseInterval, record)
	nextRecord(t, records)
	stop()

	drained := len(records)
	time.Sleep(testRepeatedTicks * testPulseInterval)
	assert.Equal(t, drained, len(records), "no heartbeat after stop returns")
}

func TestStartPulse_ContextCancelEndsHeartbeats(t *testing.T) {
	t.Parallel()
	records, record := recorder()
	ctx, cancel := context.WithCancel(context.Background())
	_, stop := startPulse(ctx, testPulseInterval, record)
	nextRecord(t, records)
	cancel()
	stop() // returns only once the pulse has ended

	drained := len(records)
	time.Sleep(testRepeatedTicks * testPulseInterval)
	assert.Equal(t, drained, len(records), "no heartbeat after the context is done")
}
