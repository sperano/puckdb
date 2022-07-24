package core

import (
	"fmt"
	"strings"
	"time"

	"github.com/rs/zerolog/log"
)

type LocalFile struct {
	Name      string
	Extension string
	Time      time.Time
}

func GetLocalFilename(name string, extension string, time time.Time) string {
	return fmt.Sprintf("%s_%s.%s", name, GetTimestamp(time), extension)
}

func (gf *LocalFile) Filename() string {
	return GetLocalFilename(gf.Name, gf.Extension, gf.Time)
}

func ParseLocalFilename(name string) *LocalFile {
	if !strings.Contains(name, "_") {
		log.Debug().Str("name", name).Msg("no _ in filename, ignoring")
		return nil
	}
	tokens := strings.Split(name, "_")
	if len(tokens) != 2 {
		log.Debug().Str("name", name).Msg("more then two tokens in filename, ignoring")
		return nil
	}
	name = tokens[0]
	tokens = strings.Split(tokens[1], ".")
	if len(tokens) != 2 {
		log.Debug().Str("name", name).Msg("more then two tokens in second _ token, ignoring")
		return nil
	}
	time, err := ParseTimestamp(tokens[0])
	if err != nil {
		log.Debug().Str("token", tokens[0]).Err(err).Msg("not a valid timestamp")
		return nil
	}
	return &LocalFile{
		Name:      name,
		Extension: tokens[1],
		Time:      time,
	}
}
