// Package buildinfo holds deterministic, linker-supplied release metadata.
package buildinfo

import (
	"fmt"
	"runtime"
)

var Version = "dev"
var Revision = "unknown"

type Info struct {
	Version   string `json:"version"`
	Revision  string `json:"revision"`
	GoVersion string `json:"go_version"`
	Platform  string `json:"platform"`
}

func Current() Info {
	return Info{Version, Revision, runtime.Version(), runtime.GOOS + "/" + runtime.GOARCH}
}

func String() string {
	info := Current()
	return fmt.Sprintf("%s revision=%s %s %s", info.Version, info.Revision, info.GoVersion, info.Platform)
}
