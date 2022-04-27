package core

import (
	"fmt"
	"strings"
	"time"

	log "github.com/sirupsen/logrus"
)

type GithubFile struct {
	Name      string
	Extension string
	Time      time.Time
}

func GetGithubFilename(name string, extension string, time time.Time) string {
	return fmt.Sprintf("%s_%s.%s", name, GetTimestamp(time), extension)
}

func (gf *GithubFile) Filename() string {
	return GetGithubFilename(gf.Name, gf.Extension, gf.Time)
}

func ParseGithubFilename(name string) *GithubFile {
	if !strings.Contains(name, "_") {
		log.Tracef("no _ in filename, ignoring: %s", name)
		return nil
	}
	tokens := strings.Split(name, "_")
	if len(tokens) != 2 {
		log.Tracef("more then two tokens in filename, ignoring: %s", name)
		return nil
	}
	name = tokens[0]
	tokens = strings.Split(tokens[1], ".")
	if len(tokens) != 2 {
		log.Tracef("more then two tokens in second _ token, ignoring: %s", name)
		return nil
	}
	time, err := ParseTimestamp(tokens[0])
	if err != nil {
		log.Tracef("not a valid timestamp: %s -> %s", tokens[0], err)
		return nil
	}
	return &GithubFile{
		Name:      name,
		Extension: tokens[1],
		Time:      time,
	}
}
