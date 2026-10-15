package bundle

import (
	"compress/gzip"
	"os"
	"path/filepath"
	"testing"
	"testing/fstest"
)

func gz(t *testing.T, s string) []byte {
	t.Helper()
	p := filepath.Join(t.TempDir(), "x.gz")
	f, _ := os.Create(p)
	w := gzip.NewWriter(f)
	w.Write([]byte(s))
	w.Close()
	f.Close()
	b, _ := os.ReadFile(p)
	return b
}

func TestExtract(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	old := assets
	defer func() { assets = old }()
	assets = fstest.MapFS{
		"assets/minipro.gz":       {Data: gz(t, "#!/bin/sh\necho hi\n")},
		"assets/infoic.xml.gz":    {Data: gz(t, "<infoic/>")},
		"assets/logicic.xml.gz":   {Data: gz(t, "<logicic/>")},
		"assets/60-minipro.rules": {Data: []byte("# rules\n")},
		"assets/UPSTREAM":         {Data: []byte("commit: abc\n")},
	}
	f, err := Extract()
	if err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(f.Minipro)
	if err != nil || st.Mode()&0o111 == 0 {
		t.Fatalf("minipro not executable: %v %v", st, err)
	}
	if b, _ := os.ReadFile(f.Infoic); string(b) != "<infoic/>" {
		t.Fatalf("infoic %q", b)
	}
	if f.Upstream != "commit: abc" {
		t.Fatalf("upstream %q", f.Upstream)
	}
	if _, err := Extract(); err != nil { // second run reuses the files
		t.Fatal(err)
	}
}
