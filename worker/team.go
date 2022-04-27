package worker

import (
	"context"
	"fmt"
	"path"

	"github.com/dustin/go-humanize/english"
	"github.com/ericsperano/yfh/core"
	"github.com/ericsperano/yfh/core/model"
	log "github.com/sirupsen/logrus"
	"github.com/spf13/viper"
)

func HandleImportTeams(ctx context.Context, yfh *core.YFH) error {
	for _, teamID := range core.GetTeamIds() {
		if err := HandleImportTeam(ctx, yfh, teamID); err != nil {
			return err
		}
	}
	return nil
}

func EnsureRecentTeam(ctx context.Context, yfh *core.YFH, teamID int) (*core.GithubFile, error) {
	gitfiles, err := yfh.Github.FindTeam(ctx, teamID)
	if err != nil {
		return nil, err
	}
	log.Infof("Found %s in github", english.Plural(len(gitfiles), "fantasy game file", ""))
	if len(gitfiles) == 0 {
		content, err := Download(ctx, yfh, core.YahooTeamURL(viper.GetInt(core.FlagLeagueID), teamID))
		if err != nil {
			return nil, err
		}
		if err := yfh.Github.CreateTeam(ctx, teamID, content); err != nil {
			return nil, err
		}
		gitfiles, err = yfh.Github.FindTeam(ctx, teamID)
		if err != nil {
			return nil, err
		}
		if len(gitfiles) == 0 {
			return nil, fmt.Errorf("should have found at least one file for team %d", teamID)
		}
	}
	return gitfiles[0], nil
}

func HandleImportTeam(ctx context.Context, yfh *core.YFH, teamID int) error {
	var count int64
	if err := yfh.GormDB.Find(&model.Team{}, "id=?", teamID).Count(&count).Error; err != nil {
		return err
	}
	log.Debugf("Count: %d", count)
	if count > 0 {
		log.Infof("Already has %s in the database for id %d", english.Plural(int(count), "team", ""), teamID)
		return nil
	}
	log.Infof("No team found in the database for id %d", teamID)
	// check if the game list is in the github cache first
	gitfile, err := EnsureRecentTeam(ctx, yfh, teamID)
	if err != nil {
		return err
	}
	fantasy, err := yfh.Github.ParseXML(ctx, path.Join(core.GetTeamDir(teamID), gitfile.Filename()))
	if err != nil {
		return err
	}
	obj := fantasy.Team.ToTeamModel()
	obj.GithubTimestamp = gitfile.Time
	log.Infof("Creating team in database for id %d", teamID)
	return yfh.GormDB.Create(&obj).Error
}

/*

func HandleGithubImportTeam(ctx context.Context, yfh *core.YFH, user string, teamID int) error {
	body, err := Download(ctx, yfh, user, core.YahooTeamURL(viper.GetInt(core.FlagLeagueID), teamID))
	if err != nil {
		return err
	}
	return core.GithubCreateTeam(ctx, yfh, teamID, body)
}

func HandleGithubCheckTeams(ctx context.Context, yfh *core.YFH, user string) error {
	var returnedError error
	for _, team_id := range core.GetTeamIds() {
		err := core.Publish(yfh.Queue, &core.Task{
			Type: core.TaskGithubCheckTeam,
			Data: map[string]string{
				"user":    apiserver.DefaultUser,
				"team_id": strconv.Itoa(team_id),
			},
		})
		if err != nil {
			if returnedError == nil {
				returnedError = err
			}
			log.Error(err)
		}
	}
	return returnedError
}

func HandleDatabaseCheckTeams(ctx context.Context, yfh *core.YFH, user string) error {
	var returnedError error
	for _, team_id := range core.GetTeamIds() {
		err := core.Publish(yfh.Queue, &core.Task{
			Type: core.TaskDatabaseCheckTeam,
			Data: map[string]string{
				"user":    apiserver.DefaultUser,
				"team_id": strconv.Itoa(team_id),
			},
		})
		if err != nil {
			if returnedError == nil {
				returnedError = err
			}
			log.Error(err)
		}
	}
	return returnedError
}

func HandleDatabaseCheckTeam(ctx context.Context, yfh *core.YFH, user string, team_id int) error {
	var count int64
	yfh.GormDB.Find(&model.Team{}, "id=?", team_id).Count(&count)
	log.Debugf("Count: %d", count)
	if count == 0 {
		log.Infof("No team #%d found in the database", team_id)
		cfs, err := core.GithubFindTeam(ctx, yfh, team_id)
		if err != nil {
			return err
		}
		if len(cfs) > 0 {
			return core.Publish(yfh.Queue, &core.Task{
				Type: core.TaskDatabaseImportTeam,
				Data: map[string]string{
					"user":    user,
					"team_id": strconv.Itoa(team_id),
					"path":    path.Join(core.GetTeamDir(team_id), cfs[0].Filename()),
				},
			})
		} else {
			return fmt.Errorf("No file in cache")
		}
	} else {
		log.Infof("Already has %s (ID: %d) in the database", english.Plural(int(count), "team", ""), team_id)
	}
	return nil
}
*/
