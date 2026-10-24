package shared

import (
	"fmt"
	"slices"

	"go.temporal.io/sdk/workflow"
)

// PoolProgress is how a RunWorkerPool call reports its items on the tracker;
// a nil PoolProgress reports nothing. The modes are the values SingleBar,
// BarPerItem and BarsPerItem; RunWorkerPool rejects pointers to them with an
// error. Every mode advances an item whether or not it failed, unless
// PoolOptions.OnlyOnSuccess is set.
type PoolProgress interface {
	// hooks checks the mode against the tracker's report for total items
	// and returns the scheduler hooks that apply it. saveEach makes every
	// mutation save immediately; otherwise the pool batches the saves.
	// Unexported: the set of modes is closed.
	hooks(t *ReportTracker, total int, saveEach bool) (workerPoolHooks, error)
}

// SingleBar advances one bar per completed item: by Increment(i), or by 1
// when Increment is nil. The bar advances whether or not the item failed.
type SingleBar struct {
	Bar       BarRef
	Increment IncrementFunc
}

func (p SingleBar) hooks(t *ReportTracker, _ int, saveEach bool) (workerPoolHooks, error) {
	bars, ok := t.poolGroupBars(p.Bar.Group)
	if !ok || p.Bar.Bar < 0 || p.Bar.Bar >= bars {
		return workerPoolHooks{}, fmt.Errorf("worker pool: bar %s outside report (%s)", p.Bar, t.poolReportShape(p.Bar.Group))
	}
	ref, increment := p.Bar, p.Increment
	if increment == nil {
		increment = func(int) int { return 1 }
	}
	if saveEach {
		return workerPoolHooks{onComplete: func(ctx workflow.Context, index int) {
			t.IncrementBarBy(ctx, ref, increment(index))
		}}, nil
	}
	return workerPoolHooks{onComplete: func(_ workflow.Context, index int) {
		t.incrementBarByNoSave(ref, increment(index))
	}}, nil
}

// BarPerItem gives item i the bar First+i of Group: StartBar when the item is
// dispatched, CompleteBar when it completes.
type BarPerItem struct {
	Group int
	First int
}

func (p BarPerItem) hooks(t *ReportTracker, total int, saveEach bool) (workerPoolHooks, error) {
	bars, ok := t.poolGroupBars(p.Group)
	if !ok || p.First < 0 || total > bars-p.First {
		return workerPoolHooks{}, fmt.Errorf("worker pool: bars [%d, %d) of group %d outside report (%s)",
			p.First, p.First+total, p.Group, t.poolReportShape(p.Group))
	}
	group, first := p.Group, p.First
	if saveEach {
		return workerPoolHooks{
			onStart: func(ctx workflow.Context, index int) {
				t.StartBar(ctx, BarRef{Group: group, Bar: first + index})
			},
			onComplete: func(ctx workflow.Context, index int) {
				t.CompleteBar(ctx, BarRef{Group: group, Bar: first + index})
			},
		}, nil
	}
	return workerPoolHooks{
		onStart: func(_ workflow.Context, index int) {
			t.startBarNoSave(BarRef{Group: group, Bar: first + index})
		},
		onComplete: func(_ workflow.Context, index int) {
			t.completeBarNoSave(BarRef{Group: group, Bar: first + index})
		},
	}, nil
}

// BarsPerItem gives item i the bars Ranges[i] of Group: StartBars on
// dispatch, CompleteBars on completion. Total must equal len(Ranges).
type BarsPerItem struct {
	Group  int
	Ranges []BarRange
}

func (p BarsPerItem) hooks(t *ReportTracker, total int, saveEach bool) (workerPoolHooks, error) {
	if len(p.Ranges) != total {
		return workerPoolHooks{}, fmt.Errorf("worker pool: %d bar ranges for %d items", len(p.Ranges), total)
	}
	bars, ok := t.poolGroupBars(p.Group)
	if !ok {
		return workerPoolHooks{}, fmt.Errorf("worker pool: group %d outside report (%s)", p.Group, t.poolReportShape(p.Group))
	}
	for i, r := range p.Ranges {
		if r.Start < 0 || r.Count < 0 || r.Count > bars-r.Start {
			return workerPoolHooks{}, fmt.Errorf("worker pool: item %d bars [%d, %d) of group %d outside report (%s)",
				i, r.Start, r.Start+r.Count, p.Group, t.poolReportShape(p.Group))
		}
	}
	// A copy: the caller may reuse its slice once validation has passed.
	group, ranges := p.Group, slices.Clone(p.Ranges)
	if saveEach {
		return workerPoolHooks{
			onStart: func(ctx workflow.Context, index int) {
				t.StartBars(ctx, group, ranges[index])
			},
			onComplete: func(ctx workflow.Context, index int) {
				t.CompleteBars(ctx, group, ranges[index])
			},
		}, nil
	}
	return workerPoolHooks{
		onStart: func(_ workflow.Context, index int) {
			t.startBarsNoSave(group, ranges[index])
		},
		onComplete: func(_ workflow.Context, index int) {
			t.completeBarsNoSave(group, ranges[index])
		},
	}, nil
}

// poolGroupBars returns how many bars group has; ok is false when the report
// has no such group. Plain bounds checks: pool validation returns an error
// rather than panicking.
func (t *ReportTracker) poolGroupBars(group int) (bars int, ok bool) {
	if t.report == nil || group < 0 || group >= len(t.report.Groups) {
		return 0, false
	}
	return len(t.report.Groups[group].Bars), true
}

// poolReportShape describes the report's size around group for a
// validation error: its group count, and group's bar count when it exists.
func (t *ReportTracker) poolReportShape(group int) string {
	groups := 0
	if t.report != nil {
		groups = len(t.report.Groups)
	}
	bars, ok := t.poolGroupBars(group)
	if !ok {
		return fmt.Sprintf("%d groups", groups)
	}
	return fmt.Sprintf("%d groups; group %d has %d bars", groups, group, bars)
}
