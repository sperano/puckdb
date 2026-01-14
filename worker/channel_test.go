package worker

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestFanIn_SingleChannel(t *testing.T) {
	ctx := context.Background()

	// Create a single channel with some values
	ch := make(chan int, 3)
	ch <- 1
	ch <- 2
	ch <- 3
	close(ch)

	out := FanIn(ctx, ch)

	var results []int
	for v := range out {
		results = append(results, v)
	}

	assert.Len(t, results, 3)
	assert.ElementsMatch(t, []int{1, 2, 3}, results)
}

func TestFanIn_MultipleChannels(t *testing.T) {
	ctx := context.Background()

	// Create multiple channels
	ch1 := make(chan int, 2)
	ch1 <- 1
	ch1 <- 2
	close(ch1)

	ch2 := make(chan int, 2)
	ch2 <- 3
	ch2 <- 4
	close(ch2)

	out := FanIn(ctx, ch1, ch2)

	var results []int
	for v := range out {
		results = append(results, v)
	}

	assert.Len(t, results, 4)
	assert.ElementsMatch(t, []int{1, 2, 3, 4}, results)
}

func TestFanIn_EmptyChannels(t *testing.T) {
	ctx := context.Background()

	ch := make(chan int)
	close(ch)

	out := FanIn(ctx, ch)

	var results []int
	for v := range out {
		results = append(results, v)
	}

	assert.Len(t, results, 0)
}

func TestFanIn_CancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())

	// Create a channel that won't be drained
	ch := make(chan int)
	go func() {
		defer close(ch)
		for i := 0; i < 100; i++ {
			select {
			case ch <- i:
			case <-ctx.Done():
				return
			}
		}
	}()

	out := FanIn(ctx, ch)

	// Read a few values then cancel
	<-out
	cancel()

	// Channel should eventually close
	timeout := time.After(100 * time.Millisecond)
	for {
		select {
		case _, ok := <-out:
			if !ok {
				return // Success: channel closed
			}
		case <-timeout:
			t.Fatal("timeout waiting for FanIn to close after cancel")
		}
	}
}

func TestMergeErrors_SingleError(t *testing.T) {
	errCh := make(chan error, 1)
	errCh <- errors.New("test error")
	close(errCh)

	merged := MergeErrors(errCh)

	var errs []error
	for err := range merged {
		errs = append(errs, err)
	}

	assert.Len(t, errs, 1)
	assert.EqualError(t, errs[0], "test error")
}

func TestMergeErrors_MultipleChannels(t *testing.T) {
	errCh1 := make(chan error, 1)
	errCh1 <- errors.New("error 1")
	close(errCh1)

	errCh2 := make(chan error, 1)
	errCh2 <- errors.New("error 2")
	close(errCh2)

	merged := MergeErrors(errCh1, errCh2)

	var errs []error
	for err := range merged {
		errs = append(errs, err)
	}

	assert.Len(t, errs, 2)
}

func TestMergeErrors_NoErrors(t *testing.T) {
	errCh := make(chan error)
	close(errCh)

	merged := MergeErrors(errCh)

	var errs []error
	for err := range merged {
		errs = append(errs, err)
	}

	assert.Len(t, errs, 0)
}

func TestMergeErrors_MixedNilAndErrors(t *testing.T) {
	errCh := make(chan error, 2)
	errCh <- nil // nil error
	errCh <- errors.New("real error")
	close(errCh)

	merged := MergeErrors(errCh)

	var errs []error
	for err := range merged {
		errs = append(errs, err)
	}

	assert.Len(t, errs, 2)
}

func TestWaitForPipeline_NoErrors(t *testing.T) {
	errCh := make(chan error)
	close(errCh)

	err := WaitForPipeline(errCh)
	assert.NoError(t, err)
}

func TestWaitForPipeline_FirstErrorReturned(t *testing.T) {
	errCh := make(chan error, 2)
	errCh <- errors.New("first error")
	errCh <- errors.New("second error")
	close(errCh)

	err := WaitForPipeline(errCh)
	assert.Error(t, err)
	assert.EqualError(t, err, "first error")
}

func TestWaitForPipeline_NilErrorsIgnored(t *testing.T) {
	errCh := make(chan error, 3)
	errCh <- nil
	errCh <- nil
	errCh <- nil
	close(errCh)

	err := WaitForPipeline(errCh)
	assert.NoError(t, err)
}

func TestWaitForPipeline_NilThenRealError(t *testing.T) {
	errCh := make(chan error, 2)
	errCh <- nil
	errCh <- errors.New("the error")
	close(errCh)

	err := WaitForPipeline(errCh)
	assert.Error(t, err)
	assert.EqualError(t, err, "the error")
}
