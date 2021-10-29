package fs

import (
	"errors"
	"fmt"
	"os"
	"path"
	"strconv"
	"time"

	"github.com/ericsperano/yfh/core"
)

type DownloadedTeamForDateData struct {
	Team      int
	Year      int
	Month     int
	Day       int
	Timestamp time.Time
}

func NewDownloadTeamForDateData(team int, year int, month int, day int) *DownloadedTeamForDateData {
	return &DownloadedTeamForDateData{
		Team:      team,
		Year:      year,
		Month:     month,
		Day:       day,
		Timestamp: time.Now(),
	}
}

func (d *DownloadedTeamForDateData) Dir(config *core.Config) string {
	return path.Join(config.CachePath, strconv.Itoa(d.Year), strconv.Itoa(d.Month), strconv.Itoa(d.Day))
}

func (d *DownloadedTeamForDateData) Path(config *core.Config) string {
	return path.Join(d.Dir(config), fmt.Sprintf("%d-%d.html", d.Team, time.Now().Unix()))
}

type FSManager struct {
	Config *core.Config
}

func NewFSManager(config *core.Config) (*FSManager, error) {
	if config.CachePath == "" {
		return nil, errors.New("CachePath is empty")
	}
	if _, err := os.Stat(config.CachePath); os.IsNotExist(err) {
		fmt.Printf("Creating CachePath: %s\n", config.CachePath)
		if err := os.MkdirAll(config.CachePath, 0755); err != nil {
			return nil, err
		}
	} else {
		fmt.Printf("CachePath already exists: %s\n", config.CachePath)
	}
	//d := time.Date(2021, 11, 02, 12, 41, 0, 0, time.UTC)
	//fmt.Printf("d=%+v\n", d)
	return &FSManager{
		Config: config,
	}, nil
}

func (fsm *FSManager) GetDownloads(team int, year int, month int, day int) []*DownloadedTeamForDateData {
	downloads := []*DownloadedTeamForDateData{}
	return downloads
}

func (fsm *FSManager) DownloadTeamForDate(team int, year int, month int, day int) (*DownloadedTeamForDateData, error) {
	data := NewDownloadTeamForDateData(team, year, month, day)
	dir := data.Dir(fsm.Config)
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		fmt.Printf("Creating CachePath: %s\n", dir)
		if err := os.MkdirAll(dir, 0755); err != nil {
			return nil, err
		}
	} else {
		fmt.Printf("CachePath already exists: %s\n", dir)
	}
	return data, nil
}
