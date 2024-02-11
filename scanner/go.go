package scanner

import (
	"fmt"
	"github.com/superfly/flyctl/terminal"
	"golang.org/x/mod/modfile"
	"os"
)

func configureGo(sourceDir string, config *ScannerConfig) (*SourceInfo, error) {
	if !checksPass(sourceDir, fileExists("go.mod")) {
		return nil, nil
	}

	gomod, parseErr := parseModfile()

	version := "1"
	if parseErr != nil {
		terminal.Warnf("go.mod appears to be invalid, the next deployment may fail: %v", parseErr)
	} else if gomod.Go.Version != "" {
		version = gomod.Go.Version
	}

	files := templates("templates/go")

	s := &SourceInfo{
		Files:  files,
		Family: "Go",
		Port:   8080,
		Env: map[string]string{
			"PORT": "8080",
		},
		BuildArgs: map[string]string{
			"GO_VERSION": version,
		},
	}

	return s, nil
}

func parseModfile() (*modfile.File, error) {
	dat, err := os.ReadFile("go.mod")
	if err != nil {
		return nil, fmt.Errorf("could not open go.mod: %w", err)
	}

	f, modErr := modfile.Parse("go.mod", dat, nil)

	if modErr != nil {
		return nil, fmt.Errorf("could not parse go.mod: %w", modErr)
	}

	return f, nil
}
