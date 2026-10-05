// Package archive builds and reads ZIP archives under strict size and entry
// limits, with protection against path traversal and entry-name confusion.
package archive

import (
	"archive/zip"
	"bytes"
	"fmt"
	"io"
	"strings"
)

const (
	// MaxEntries is the maximum number of entries accepted in any archive.
	MaxEntries = 13
	// MaxTotalBytes bounds the sum of uncompressed entry bytes.
	MaxTotalBytes = 4 << 20
	// MaxSingleBytes bounds one entry's uncompressed size.
	MaxSingleBytes = MaxTotalBytes
)

// File is one archive entry.
type File struct {
	Name string
	Data []byte
}

// Build returns a ZIP containing the given files (entry order preserved).
func Build(files []File) ([]byte, error) {
	if len(files) > MaxEntries {
		return nil, fmt.Errorf("too many entries: %d > %d", len(files), MaxEntries)
	}
	var total int64
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	seen := map[string]bool{}
	for _, f := range files {
		if !SafeEntryName(f.Name) {
			return nil, fmt.Errorf("unsafe entry name %q", f.Name)
		}
		if seen[f.Name] {
			return nil, fmt.Errorf("duplicate entry %q", f.Name)
		}
		seen[f.Name] = true
		size := int64(len(f.Data))
		if size > MaxSingleBytes || total+size > MaxTotalBytes {
			return nil, fmt.Errorf("archive size limit %d bytes exceeded", MaxTotalBytes)
		}
		total += size
		ew, err := w.Create(f.Name)
		if err != nil {
			return nil, err
		}
		if _, err := ew.Write(f.Data); err != nil {
			return nil, err
		}
	}
	if err := w.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// Read parses an uploaded ZIP, enforcing entry count, per-entry and total
// uncompressed size limits and rejecting unsafe or duplicate names.
func Read(zipBytes []byte) ([]File, error) {
	zr, err := zip.NewReader(bytes.NewReader(zipBytes), int64(len(zipBytes)))
	if err != nil {
		return nil, fmt.Errorf("invalid zip: %w", err)
	}
	if len(zr.File) > MaxEntries {
		return nil, fmt.Errorf("zip has %d entries, limit is %d", len(zr.File), MaxEntries)
	}
	seen := map[string]bool{}
	out := make([]File, 0, len(zr.File))
	var total int64
	for _, zf := range zr.File {
		name := zf.Name
		if strings.HasSuffix(name, "/") {
			continue // directory entries carry no data
		}
		if !SafeEntryName(name) {
			return nil, fmt.Errorf("unsafe entry name %q", name)
		}
		if seen[name] {
			return nil, fmt.Errorf("duplicate entry %q", name)
		}
		seen[name] = true
		if zf.Mode()&0o170000 != 0 {
			return nil, fmt.Errorf("entry %q is not a regular file", name)
		}
		if zf.UncompressedSize64 > MaxSingleBytes {
			return nil, fmt.Errorf("entry %q exceeds single-entry limit", name)
		}
		rc, err := zf.Open()
		if err != nil {
			return nil, fmt.Errorf("opening %q: %w", name, err)
		}
		lr := io.LimitReader(rc, MaxSingleBytes+1)
		data, err := io.ReadAll(lr)
		closeErr := rc.Close()
		if err != nil {
			return nil, fmt.Errorf("reading %q: %w", name, err)
		}
		if closeErr != nil {
			return nil, fmt.Errorf("reading %q: %w", name, closeErr)
		}
		if int64(len(data)) > MaxSingleBytes {
			return nil, fmt.Errorf("entry %q exceeds single-entry limit", name)
		}
		total += int64(len(data))
		if total > MaxTotalBytes {
			return nil, fmt.Errorf("total uncompressed size exceeds %d bytes", MaxTotalBytes)
		}
		out = append(out, File{Name: name, Data: data})
	}
	return out, nil
}

// SafeEntryName rejects absolute paths, drive letters, traversal segments,
// separators, control characters and reserved device-like prefixes. Names are
// always treated as flat base names within the archive root.
func SafeEntryName(name string) bool {
	if name == "" || len(name) > 255 {
		return false
	}
	if strings.ContainsAny(name, "/\\") || strings.Contains(name, "\x00") || name == "." || name == ".." || isReserved(name) {
		return false
	}
	for _, c := range name {
		if c < 0x20 {
			return false
		}
	}
	return true
}

func isReserved(name string) bool {
	upper := strings.ToUpper(name)
	for _, p := range []string{"CON", "PRN", "AUX", "NUL"} {
		if upper == p || strings.HasPrefix(upper, p+".") {
			return true
		}
	}
	for _, p := range []string{"COM", "LPT"} {
		for i := 1; i <= 9; i++ {
			dev := fmt.Sprintf("%s%d", p, i)
			if upper == dev || strings.HasPrefix(upper, dev+".") {
				return true
			}
		}
	}
	return false
}
