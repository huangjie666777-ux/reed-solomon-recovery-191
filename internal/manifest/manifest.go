// Package manifest defines the archive manifest format and its validation.
package manifest

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"

	"reed-solomon-recovery/internal/reedsolomon"
)

// FormatVersion is the current manifest format version.
const FormatVersion = 1

// Manifest describes one encoded archive.
type Manifest struct {
	FormatVersion  int          `json:"format_version"`
	K              int          `json:"k"`
	M              int          `json:"m"`
	OriginalLength int          `json:"original_length"`
	ShardLength    int          `json:"shard_length"`
	OriginalName   string       `json:"original_name"`
	OriginalSHA256 string       `json:"original_sha256"`
	Shards         []ShardEntry `json:"shards"`
}

// ShardEntry describes one numbered shard.
type ShardEntry struct {
	Number int    `json:"number"`
	Name   string `json:"name"`
	SHA256 string `json:"sha256"`
}

// ShardName returns the canonical shard file name, e.g. shard-03.
func ShardName(number int) string {
	return fmt.Sprintf("shard-%02d", number)
}

// ParseShardName parses a canonical shard name into its number.
func ParseShardName(name string) (int, bool) {
	var n int
	if _, err := fmt.Sscanf(name, "shard-%02d", &n); err != nil {
		return 0, false
	}
	if ShardName(n) != name {
		return 0, false
	}
	return n, true
}

// HexSHA256 returns the lowercase hex SHA-256 of data.
func HexSHA256(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// Validate checks internal consistency of the manifest without external shard
// bytes. It rejects illegal parameters, duplicate shard numbers/names, bad
// hashes and names that do not match their number.
func (m *Manifest) Validate() error {
	if m.FormatVersion != FormatVersion {
		return fmt.Errorf("unsupported format_version %d (want %d)", m.FormatVersion, FormatVersion)
	}
	if m.K < reedsolomon.MinK || m.K > reedsolomon.MaxK {
		return fmt.Errorf("k %d out of range [%d,%d]", m.K, reedsolomon.MinK, reedsolomon.MaxK)
	}
	if m.M < reedsolomon.MinM || m.M > reedsolomon.MaxM {
		return fmt.Errorf("m %d out of range [%d,%d]", m.M, reedsolomon.MinM, reedsolomon.MaxM)
	}
	if m.OriginalLength < 0 {
		return fmt.Errorf("original_length negative")
	}
	wantShardLen := reedsolomon.ShardSize(m.OriginalLength, m.K)
	if m.ShardLength != wantShardLen {
		return fmt.Errorf("shard_length %d inconsistent with original_length %d and k %d (want %d)", m.ShardLength, m.OriginalLength, m.K, wantShardLen)
	}
	if !isHexSHA(m.OriginalSHA256) {
		return fmt.Errorf("invalid original_sha256")
	}
	if !safeName(m.OriginalName) {
		return fmt.Errorf("invalid original_name %q", m.OriginalName)
	}
	if len(m.Shards) != m.K+m.M {
		return fmt.Errorf("manifest lists %d shards, want %d", len(m.Shards), m.K+m.M)
	}
	seenNum := map[int]bool{}
	seenName := map[string]bool{}
	for _, s := range m.Shards {
		if s.Number < 0 || s.Number >= m.K+m.M {
			return fmt.Errorf("shard number %d out of range [0,%d]", s.Number, m.K+m.M-1)
		}
		if seenNum[s.Number] {
			return fmt.Errorf("duplicate shard number %d", s.Number)
		}
		seenNum[s.Number] = true
		if s.Name != ShardName(s.Number) {
			return fmt.Errorf("shard %d has mismatching name %q", s.Number, s.Name)
		}
		if seenName[s.Name] {
			return fmt.Errorf("duplicate shard name %q", s.Name)
		}
		seenName[s.Name] = true
		if !isHexSHA(s.SHA256) {
			return fmt.Errorf("invalid sha256 for shard %d", s.Number)
		}
	}
	return nil
}

func isHexSHA(s string) bool {
	if len(s) != 64 {
		return false
	}
	_, err := hex.DecodeString(s)
	return err == nil
}

// safeName rejects absolute paths, drive letters, traversal and separators;
// shard/original names must be plain base names inside the archive root.
func safeName(name string) bool {
	if name == "" || name == "." || name == ".." {
		return false
	}
	if len(name) > 255 {
		return false
	}
	for _, c := range name {
		switch c {
		case '/', '\\':
			return false
		}
	}
	return name != "manifest.json" && name != "report.json"
}

// SafeOriginalName reports whether name is a safe original file name.
func SafeOriginalName(name string) bool { return safeName(name) }
