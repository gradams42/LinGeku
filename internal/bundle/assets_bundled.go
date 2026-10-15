//go:build bundled

package bundle

import (
	"embed"
	"io/fs"
)

// The assets directory is filled by `make` (see the Makefile's bundle target).
//
//go:embed assets/minipro.gz assets/infoic.xml.gz assets/logicic.xml.gz assets/60-minipro.rules assets/UPSTREAM
var embedded embed.FS

var assets fs.FS = embedded
