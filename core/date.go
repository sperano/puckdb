package core

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/viper"
)

// TODO use class with attributes
var ErrUnexpectedMonthDayPair = errors.New("expected M-D")
var ErrInvalidMonth = errors.New("invalid month")

type DateUtils struct {
	StartDay   int
	StartMonth time.Month
	StartYear  int
}

func (du *DateUtils) GetStart() time.Time {
	return time.Date(du.StartYear, du.StartMonth, du.StartDay, 0, 0, 0, 0, time.UTC)
}

func (du *DateUtils) GetMonthDayPair(arg string) (time.Month, int, error) {
	tokens := strings.Split(arg, "-")
	if len(tokens) != 2 {
		return 0, 0, ErrUnexpectedMonthDayPair
	}
	monthNo, err := strconv.Atoi(tokens[0])
	if err != nil {
		return 0, 0, err
	}
	if monthNo < 1 || monthNo > 12 {
		return 0, 0, ErrInvalidMonth
	}
	month := time.Month(monthNo)
	day, err := strconv.Atoi(tokens[1])
	if err != nil {
		return month, 0, err
	}
	return month, day, nil
}

func (du *DateUtils) GetDate(arg string) (time.Time, error) {
	month, day, err := du.GetMonthDayPair(arg)
	if err != nil {
		return time.Time{}, err
	}
	year := du.StartYear
	if month < du.StartMonth {
		year++
	}
	return time.Date(year, month, day, 0, 0, 0, 0, time.UTC), nil
}

func (du *DateUtils) GetDateRange(from string, to string) ([]time.Time, error) {
	dates := []time.Time{}
	fromDate := du.GetStart()
	toDate := time.Now()
	var err error
	if len(from) > 0 {
		if fromDate, err = du.GetDate(from); err != nil {
			return nil, err
		}
	}
	if len(to) > 0 {
		if toDate, err = du.GetDate(to); err != nil {
			return nil, err
		}
	}
	d := fromDate
	for {
		dates = append(dates, d)
		d = d.AddDate(0, 0, 1)
		if d.Day() == toDate.Day() && d.Month() == toDate.Month() && d.Year() == toDate.Year() {
			break
		}
	}
	return dates, nil
}

func (du *DateUtils) GetDatesFromCommandLine(from string, to string, args []string) ([]time.Time, error) {
	if len(args) > 0 {
		dates := []time.Time{}
		if len(from) > 0 {
			return nil, fmt.Errorf("can't use --from when passing arguments")
		}
		if len(to) > 0 {
			return nil, fmt.Errorf("can't use --to when passing arguments")
		}
		for _, arg := range args {
			tm, err := du.GetDate(arg)
			if err != nil {
				return nil, err
			}
			dates = append(dates, tm)
		}
		return dates, nil
	}
	dates, err := du.GetDateRange(from, to)
	if err != nil {
		return nil, err
	}
	return dates, nil
}

func GetDateRange(from string, to string) ([]time.Time, error) {
	du := DateUtils{
		StartDay:   viper.GetInt(FlagSeasonStartDay),
		StartMonth: time.Month(viper.GetInt(FlagSeasonStartMonth)),
		StartYear:  viper.GetInt(FlagSeasonStartYear),
	}
	return du.GetDateRange(from, to)
}
