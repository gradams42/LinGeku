// Package bundle unpacks the minipro executable and databases that are
// embedded into release builds of LinGecu (built with -tags bundled).
package bundle

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// ErrNotBundled is returned when the binary was built without embedded minipro.
var ErrNotBundled = errors.New("this build does not embed minipro")

// Files are the paths of the unpacked bundle.
type Files struct {
	Dir       string
	Minipro   string
	Infoic    string
	Logicic   string
	UdevRules string
	Upstream  string // contents of third_party/minipro/UPSTREAM
}

// Available reports whether minipro is embedded in this binary.
func Available() bool { return assets != nil }

// Extract unpacks the bundle into the user cache directory (once per bundle
// version) and returns the resulting paths.
func Extract() (*Files, error) {
	if assets == nil {
		return nil, ErrNotBundled
	}
	bin, err := fs.ReadFile(assets, "assets/minipro.gz")
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(bin)
	cache, err := os.UserCacheDir()
	if err != nil {
		cache = os.TempDir()
	}
	dir := filepath.Join(cache, "lingecu", "minipro-"+hex.EncodeToString(sum[:6]))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	f := &Files{
		Dir:       dir,
		Minipro:   filepath.Join(dir, "minipro"),
		Infoic:    filepath.Join(dir, "infoic.xml"),
		Logicic:   filepath.Join(dir, "logicic.xml"),
		UdevRules: filepath.Join(dir, "60-minipro.rules"),
	}
	items := []struct {
		asset, dest string
		mode        os.FileMode
	}{
		{"assets/minipro.gz", f.Minipro, 0o755},
		{"assets/infoic.xml.gz", f.Infoic, 0o644},
		{"assets/logicic.xml.gz", f.Logicic, 0o644},
		{"assets/60-minipro.rules", f.UdevRules, 0o644},
	}
	for _, it := range items {
		if err := extractFile(it.asset, it.dest, it.mode); err != nil {
			return nil, err
		}
	}
	if up, err := fs.ReadFile(assets, "assets/UPSTREAM"); err == nil {
		f.Upstream = strings.TrimSpace(string(up))
	}
	return f, nil
}

// extractFile writes an embedded asset (gunzipping *.gz) unless the
// destination already exists; writes are atomic via rename.
func extractFile(asset, dest string, mode os.FileMode) error {
	if _, err := os.Stat(dest); err == nil {
		return nil
	}
	data, err := fs.ReadFile(assets, asset)
	if err != nil {
		return err
	}
	var r io.Reader = bytes.NewReader(data)
	if strings.HasSuffix(asset, ".gz") {
		zr, err := gzip.NewReader(r)
		if err != nil {
			return err
		}
		defer zr.Close()
		r = zr
	}
	tmp, err := os.CreateTemp(filepath.Dir(dest), ".extract-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := io.Copy(tmp, r); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), dest)
}
