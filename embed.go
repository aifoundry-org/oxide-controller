package oxidecontroller

import (
	"embed"
	"io/fs"
)

//go:embed chart/**
var chartFiles embed.FS

func Chart() (fs.FS, error) {
	return fs.Sub(chartFiles, "chart")
}
