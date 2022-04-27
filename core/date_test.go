package core

import (
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestGetMonthDayPairSuccess(t *testing.T) {
	t.Parallel()
	du := DateUtils{30, time.October, 2021}
	month, day, err := du.GetMonthDayPair("09-30")
	assert.Nil(t, err)
	assert.Equal(t, time.September, month)
	assert.Equal(t, 30, day)
}

func TestGetMonthDayPairErrUnexpectedMonthDayPair(t *testing.T) {
	t.Parallel()
	du := DateUtils{30, time.October, 2021}
	month, day, err := du.GetMonthDayPair("0930")
	assert.Equal(t, ErrUnexpectedMonthDayPair, err)
	assert.Equal(t, time.Month(0), month)
	assert.Equal(t, 0, day)
	month, day, err = du.GetMonthDayPair("09-3-0")
	assert.Equal(t, ErrUnexpectedMonthDayPair, err)
	assert.Equal(t, time.Month(0), month)
	assert.Equal(t, 0, day)
}

func TestGetMonthDayPairMonthIsNotAnInt(t *testing.T) {
	t.Parallel()
	du := DateUtils{30, time.October, 2021}
	month, day, err := du.GetMonthDayPair("aa-30")
	assert.Equal(t, time.Month(0), month)
	assert.Equal(t, 0, day)
	_, ok := err.(*strconv.NumError)
	assert.True(t, ok)
}

func TestGetMonthDayPairErrInvalidMonth(t *testing.T) {
	t.Parallel()
	du := DateUtils{30, time.October, 2021}
	month, day, err := du.GetMonthDayPair("15-30")
	assert.Equal(t, time.Month(0), month)
	assert.Equal(t, 0, day)
	assert.Equal(t, ErrInvalidMonth, err)
}

func TestGetMonthDayPairDayIsNotAnInt(t *testing.T) {
	t.Parallel()
	du := DateUtils{30, time.October, 2021}
	month, day, err := du.GetMonthDayPair("10-aa")
	assert.Equal(t, time.October, month)
	assert.Equal(t, 0, day)
	_, ok := err.(*strconv.NumError)
	assert.True(t, ok)
}

func TestGetDateSuccessFirstHalf(t *testing.T) {
	t.Parallel()
	/*
		config := core.Config{
			SeasonStartYear:  2021,
			SeasonStartMonth: 10,
		}
	*/
	du := DateUtils{30, time.October, 2021}
	tm, err := du.GetDate("11-30")
	assert.Nil(t, err)
	assert.Equal(t, time.November, tm.Month())
	assert.Equal(t, 30, tm.Day())
	assert.Equal(t, 2021, tm.Year())
}

func TestGetDateSuccessSecondHalf(t *testing.T) {
	t.Parallel()
	du := DateUtils{30, time.October, 2021}
	tm, err := du.GetDate("02-20")
	assert.Nil(t, err)
	assert.Equal(t, time.February, tm.Month())
	assert.Equal(t, 20, tm.Day())
	assert.Equal(t, 2022, tm.Year())
}

func assertDate(t *testing.T, date time.Time, year int, month time.Month, day int) {
	assert.Equal(t, year, date.Year())
	assert.Equal(t, month, date.Month())
	assert.Equal(t, day, date.Day())
}

func TestGetDateRangeWithFromAndTo(t *testing.T) {
	du := DateUtils{30, time.October, 2021}
	dates, err := du.GetDateRange("11-01", "11-05")
	assert.Nil(t, err)
	assert.Equal(t, 4, len(dates))
	assertDate(t, dates[0], 2021, time.November, 01)
	assertDate(t, dates[1], 2021, time.November, 02)
	assertDate(t, dates[2], 2021, time.November, 03)
	assertDate(t, dates[3], 2021, time.November, 04)
}

func TestGetDateRangeWithTo(t *testing.T) {
	du := DateUtils{30, time.October, 2021}
	dates, err := du.GetDateRange("", "11-05")
	assert.Nil(t, err)
	assert.Equal(t, 6, len(dates))
	assertDate(t, dates[0], 2021, time.October, 30)
	assertDate(t, dates[1], 2021, time.October, 31)
	assertDate(t, dates[2], 2021, time.November, 01)
	assertDate(t, dates[3], 2021, time.November, 02)
	assertDate(t, dates[4], 2021, time.November, 03)
	assertDate(t, dates[5], 2021, time.November, 04)
}

func TestGetDateRangeWithFrom(t *testing.T) {
	du := DateUtils{30, time.October, 2021}
	now := time.Now().AddDate(0, 0, -1)
	dates, err := du.GetDateRange("", "")
	assert.Nil(t, err)
	assertDate(t, dates[0], 2021, time.October, 30)
	assertDate(t, dates[len(dates)-1], now.Year(), now.Month(), now.Day())
}
