// Package project saves and restores a complete LinGecu session (device,
// options and buffer contents) like XGecu's project files.
package project

import (
	"compress/gzip"
	"encoding/json"
	"fmt"
	"os"
)

// FileVersion is the current project format version.
const FileVersion = 1

// Project is the on-disk project structure (gzip-compressed JSON).
type Project struct {
	Version    int               `json:"version"`
	Programmer string            `json:"programmer"`
	Device     string            `json:"device"`
	Options    map[string]string `json:"options"`
	Code       []byte            `json:"code,omitempty"`
	Data       []byte            `json:"data,omitempty"`
	JEDEC      string            `json:"jedec,omitempty"`
	ConfigKeys []string          `json:"config_keys,omitempty"`
	Config     map[string]string `json:"config,omitempty"`
	SourceFile string            `json:"source_file,omitempty"`
	Notes      string            `json:"notes,omitempty"`
}

// Save writes p to path.
func Save(path string, p *Project) error {
	p.Version = FileVersion
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	zw := gzip.NewWriter(f)
	enc := json.NewEncoder(zw)
	enc.SetIndent("", " ")
	err = enc.Encode(p)
	if cerr := zw.Close(); err == nil {
		err = cerr
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	return err
}

// Load reads a project file.
func Load(path string) (*Project, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	zr, err := gzip.NewReader(f)
	if err != nil {
		return nil, fmt.Errorf("not a LinGecu project: %w", err)
	}
	var p Project
	if err := json.NewDecoder(zr).Decode(&p); err != nil {
		return nil, fmt.Errorf("corrupt project: %w", err)
	}
	if p.Version > FileVersion {
		return nil, fmt.Errorf("project version %d is newer than this LinGecu supports", p.Version)
	}
	return &p, nil
}
