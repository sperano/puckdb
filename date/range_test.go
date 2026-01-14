package date

import (
	"errors"
	"github.com/golang-module/carbon/v2"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func assertDate(t *testing.T, date time.Time, year int, month time.Month, day int) {
	assert.Equal(t, year, date.Year())
	assert.Equal(t, month, date.Month())
	assert.Equal(t, day, date.Day())
}

func TestGetDateRange(t *testing.T) {
	t.Parallel()
	rts1 := carbon.SetTimezone(time.UTC.String()).Parse("2022-11-01")
	rts2 := carbon.SetTimezone(time.UTC.String()).Parse("2022-11-05")
	dates := NewDateRange(rts1.ToStdTime(), rts2.ToStdTime())
	assert.Equal(t, 5, len(dates))
	assertDate(t, dates[0], 2022, time.November, 01)
	assertDate(t, dates[1], 2022, time.November, 02)
	assertDate(t, dates[2], 2022, time.November, 03)
	assertDate(t, dates[3], 2022, time.November, 04)
	assertDate(t, dates[4], 2022, time.November, 05)
}

func TestGetDateRangeToTodayBeforeStart(t *testing.T) {
	t.Parallel()
	start := carbon.SetTimezone(time.UTC.String()).Parse("2022-11-01")
	end := carbon.SetTimezone(time.UTC.String()).Parse("2022-11-05")
	today := carbon.SetTimezone(time.UTC.String()).Parse("2022-10-25")
	dates, err := DateRangeToToday(start.ToStdTime(), end.ToStdTime(), today.ToStdTime())
	assert.Nil(t, dates)
	assert.True(t, errors.Is(err, ErrBeforeSeasonStart))
}

func TestGetDateRangeToTodayOnStart(t *testing.T) {
	t.Parallel()
	start := carbon.SetTimezone(time.UTC.String()).Parse("2022-11-01")
	end := carbon.SetTimezone(time.UTC.String()).Parse("2022-11-05")
	today := carbon.SetTimezone(time.UTC.String()).Parse("2022-11-01")
	dates, err := DateRangeToToday(start.ToStdTime(), end.ToStdTime(), today.ToStdTime())
	if err != nil {
		t.Fatal(err)
	}
	assert.Equal(t, 0, len(dates))
}

func TestGetDateRangeToTodayOneDayAfterStart(t *testing.T) {
	t.Parallel()
	start := carbon.SetTimezone(time.UTC.String()).Parse("2022-11-01")
	end := carbon.SetTimezone(time.UTC.String()).Parse("2022-11-05")
	today := carbon.SetTimezone(time.UTC.String()).Parse("2022-11-02")
	dates, err := DateRangeToToday(start.ToStdTime(), end.ToStdTime(), today.ToStdTime())
	if err != nil {
		t.Fatal(err)
	}
	assert.Equal(t, 1, len(dates))
	assertDate(t, dates[0], 2022, time.November, 01)
}

func TestGetDateRangeToTodayBeforeEnd(t *testing.T) {
	t.Parallel()
	start := carbon.SetTimezone(time.UTC.String()).Parse("2022-11-01")
	end := carbon.SetTimezone(time.UTC.String()).Parse("2022-11-05")
	today := carbon.SetTimezone(time.UTC.String()).Parse("2022-11-03")
	dates, err := DateRangeToToday(start.ToStdTime(), end.ToStdTime(), today.ToStdTime())
	if err != nil {
		t.Fatal(err)
	}
	assert.Equal(t, 2, len(dates))
	assertDate(t, dates[0], 2022, time.November, 01)
	assertDate(t, dates[1], 2022, time.November, 02)
}

func TestGetDateRangeToTodayAfterEnd(t *testing.T) {
	t.Parallel()
	start := carbon.SetTimezone(time.UTC.String()).Parse("2022-11-01")
	end := carbon.SetTimezone(time.UTC.String()).Parse("2022-11-05")
	today := carbon.SetTimezone(time.UTC.String()).Parse("2022-11-20")
	dates, err := DateRangeToToday(start.ToStdTime(), end.ToStdTime(), today.ToStdTime())
	if err != nil {
		t.Fatal(err)
	}
	assert.Equal(t, 5, len(dates))
	assertDate(t, dates[0], 2022, time.November, 01)
	assertDate(t, dates[1], 2022, time.November, 02)
	assertDate(t, dates[2], 2022, time.November, 03)
	assertDate(t, dates[3], 2022, time.November, 04)
	assertDate(t, dates[4], 2022, time.November, 05)
}

func TestGetDateRangeToTodayOnEnd(t *testing.T) {
	t.Parallel()
	start := carbon.SetTimezone(time.UTC.String()).Parse("2022-11-01")
	end := carbon.SetTimezone(time.UTC.String()).Parse("2022-11-05")
	today := carbon.SetTimezone(time.UTC.String()).Parse("2022-11-03")
	dates, err := DateRangeToToday(start.ToStdTime(), end.ToStdTime(), today.ToStdTime())
	if err != nil {
		t.Fatal(err)
	}
	assert.Equal(t, 2, len(dates))
	assertDate(t, dates[0], 2022, time.November, 01)
	assertDate(t, dates[1], 2022, time.November, 02)
}
