package ui

import (
	"os"
	"path/filepath"

	"github.com/lxn/walk"
)

func loadAppIcon() *walk.Icon {
	// Embedded via rsrc -ico (application icon resource).
	if icon, err := walk.NewIconFromResourceId(2); err == nil {
		return icon
	}
	if exe, err := os.Executable(); err == nil {
		candidate := filepath.Join(filepath.Dir(exe), "app.ico")
		if icon, err := walk.NewIconFromFile(candidate); err == nil {
			return icon
		}
	}
	for _, p := range []string{
		"assets/dacast.ico",
		"cmd/watchfolder/app.ico",
		filepath.Join("..", "..", "assets", "dacast.ico"),
	} {
		if icon, err := walk.NewIconFromFile(p); err == nil {
			return icon
		}
	}
	return walk.IconApplication()
}
