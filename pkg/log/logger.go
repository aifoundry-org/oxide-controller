package log

import (
	log "github.com/sirupsen/logrus"
)

func GetLevel(verbose int) log.Level {
	var level log.Level
	switch verbose {
	case 0:
		level = log.InfoLevel
	case 1:
		level = log.DebugLevel
	case 2:
		level = log.TraceLevel
	default:
		level = log.InfoLevel

	}
	return level
}

func GetFlag(level log.Level) int {
	var verbose int
	switch level {
	case log.DebugLevel:
		verbose = 1
	case log.TraceLevel:
		verbose = 2
	default:
		verbose = 0
	}
	return verbose
}
