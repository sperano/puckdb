package core

import (
	"os"
	"runtime"

	"github.com/dustin/go-humanize/english"
	log "github.com/sirupsen/logrus"
)

func PrintEnv() {
	// TODO sort
	for _, e := range os.Environ() {
		log.Trace(e)
	}
}

// TODO publish with a tag with this version too
const Version = "0.5.14"

func LogIntro() {
	log.Infof("Yahoo Fantasy Hockey version %s", Version)
	PrintEnv()
	log.Info(english.Plural(runtime.NumCPU(), "CPU", ""))
}
