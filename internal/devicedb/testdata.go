package devicedb

import (
	"os"
	"path/filepath"
)

// FindShareDir returns a directory containing minipro's infoic.xml, checking
// $MINIPRO_SHARE, the extra candidates, and the standard install locations.
// It is used by tests that need the real database; it returns "" when none
// is found.
func FindShareDir(extra ...string) string {
	cands := append([]string{os.Getenv("MINIPRO_SHARE")}, extra...)
	cands = append(cands, "/usr/local/share/minipro", "/usr/share/minipro")
	for _, d := range cands {
		if d == "" {
			continue
		}
		if _, err := os.Stat(filepath.Join(d, "infoic.xml")); err == nil {
			return d
		}
	}
	return ""
}
