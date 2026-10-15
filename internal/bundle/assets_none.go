//go:build !bundled

package bundle

import "io/fs"

// Without the bundled tag nothing is embedded; minipro is found on the system.
var assets fs.FS
