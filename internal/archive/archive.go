package archive

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path"
	"sort"
	"strconv"
	"strings"
)

const (
	FormatVersion       = "1.0"
	ManifestName        = "manifest.json"
	ReportName          = "report.json"
	ShardPrefix         = "shard-"
	ShardSuffix         = ".dat"
	MaxTotalSize  int64 = 4 << 20
	MaxEntryCount       = 13
)

type ShardHash struct {
	Number int    `json:"number"`
	SHA256 string `json:"sha256"`
}

type Manifest struct {
	FormatVersion  string      `json:"format_version"`
	K              int         `json:"k"`
	M              int         `json:"m"`
	OriginalLength int         `json:"original_length"`
	ShardLength    int         `json:"shard_length"`
	OriginalName   string      `json:"original_name"`
	OriginalSHA256 string      `json:"original_sha256"`
	ShardHashes    []ShardHash `json:"shard_hashes"`
}

type RecoveryReport struct {
	Status              string `json:"status"`
	K                   int    `json:"k"`
	M                   int    `json:"m"`
	OriginalName        string `json:"original_name"`
	OriginalSHA256      string `json:"original_sha256"`
	RecoveredSHA256     string `json:"recovered_sha256,omitempty"`
	MissingShardNumbers []int  `json:"missing_shard_numbers"`
	BadShardNumbers     []int  `json:"bad_shard_numbers"`
	UsedShardNumbers    []int  `json:"used_shard_numbers,omitempty"`
	Error               string `json:"error,omitempty"`
	TrustNotice         string `json:"trust_notice"`
}

type Bundle struct {
	Manifest Manifest
	Shards   map[int][]byte
}

func ShardName(number int) string {
	return fmt.Sprintf("%s%d%s", ShardPrefix, number, ShardSuffix)
}

func ValidateParameters(k, m int, originalLength int) error {
	if k < 2 || k > 8 {
		return fmt.Errorf("k must be between 2 and 8, got %d", k)
	}
	if m < 1 || m > 4 {
		return fmt.Errorf("m must be between 1 and 4, got %d", m)
	}
	if originalLength < 1 || originalLength > 1<<20 {
		return fmt.Errorf("file size must be between 1 byte and 1 MiB, got %d", originalLength)
	}
	return nil
}

func NewManifest(k, m, originalLength, shardLength int, name string, data []byte, shards [][]byte) Manifest {
	manifest := Manifest{
		FormatVersion:  FormatVersion,
		K:              k,
		M:              m,
		OriginalLength: originalLength,
		ShardLength:    shardLength,
		OriginalName:   name,
		OriginalSHA256: hex.EncodeToString(sha256Bytes(data)),
		ShardHashes:    make([]ShardHash, 0, len(shards)),
	}
	for number, shard := range shards {
		manifest.ShardHashes = append(manifest.ShardHashes, ShardHash{
			Number: number,
			SHA256: hex.EncodeToString(sha256Bytes(shard)),
		})
	}
	return manifest
}

func ValidateManifest(manifest Manifest) error {
	if manifest.FormatVersion != FormatVersion {
		return fmt.Errorf("unsupported format version %q", manifest.FormatVersion)
	}
	if err := ValidateParameters(manifest.K, manifest.M, manifest.OriginalLength); err != nil {
		return err
	}
	if manifest.ShardLength <= 0 {
		return errors.New("shard length must be positive")
	}
	expectedLength := (manifest.OriginalLength + manifest.K - 1) / manifest.K
	if manifest.ShardLength != expectedLength {
		return fmt.Errorf("shard length %d does not match expected %d", manifest.ShardLength, expectedLength)
	}
	if err := ValidateSafeName(manifest.OriginalName); err != nil {
		return fmt.Errorf("invalid original name: %w", err)
	}
	if manifest.OriginalName == ManifestName || manifest.OriginalName == ReportName {
		return errors.New("reserved original file name")
	}
	if !isValidSHA256(manifest.OriginalSHA256) {
		return errors.New("invalid original sha256")
	}
	if len(manifest.ShardHashes) != manifest.K+manifest.M {
		return fmt.Errorf("expected %d shard hashes, got %d", manifest.K+manifest.M, len(manifest.ShardHashes))
	}
	seen := make(map[int]bool, manifest.K+manifest.M)
	for _, entry := range manifest.ShardHashes {
		if entry.Number < 0 || entry.Number >= manifest.K+manifest.M {
			return fmt.Errorf("shard number %d out of range", entry.Number)
		}
		if seen[entry.Number] {
			return fmt.Errorf("duplicate shard hash %d", entry.Number)
		}
		seen[entry.Number] = true
		if !isValidSHA256(entry.SHA256) {
			return fmt.Errorf("invalid sha256 for shard %d", entry.Number)
		}
	}
	return nil
}

func ValidateSafeName(name string) error {
	if name == "" || name == "." || name == ".." {
		return errors.New("empty name")
	}
	if len(name) > 255 || strings.ContainsAny(name, `/\`) {
		return errors.New("name must be a single path component")
	}
	if path.IsAbs(name) || strings.Contains(name, "\x00") {
		return errors.New("absolute or NUL-containing name")
	}
	clean := path.Clean(name)
	if clean != name || clean == ".." || strings.HasPrefix(clean, "../") {
		return errors.New("path traversal")
	}
	return nil
}

func WriteEncodeZip(manifest Manifest, shards [][]byte) ([]byte, error) {
	if len(shards) != len(manifest.ShardHashes) {
		return nil, errors.New("shard count does not match manifest")
	}
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	if err := writeJSON(writer, ManifestName, manifest); err != nil {
		return nil, err
	}
	for number, shard := range shards {
		writerFile, err := writer.Create(ShardName(number))
		if err != nil {
			return nil, err
		}
		if _, err := writerFile.Write(shard); err != nil {
			return nil, err
		}
	}
	if err := writer.Close(); err != nil {
		return nil, err
	}
	return buffer.Bytes(), nil
}

func WriteRecoveryZip(name string, content []byte, report RecoveryReport) ([]byte, error) {
	if err := ValidateSafeName(name); err != nil {
		return nil, err
	}
	if name == ManifestName || name == ReportName {
		return nil, errors.New("reserved recovery file name")
	}
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	if err := writeBytes(writer, name, content); err != nil {
		return nil, err
	}
	if err := writeJSON(writer, ReportName, report); err != nil {
		return nil, err
	}
	if err := writer.Close(); err != nil {
		return nil, err
	}
	return buffer.Bytes(), nil
}

func ReadBundle(archive []byte) (*Bundle, error) {
	reader, err := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
	if err != nil {
		return nil, fmt.Errorf("invalid zip: %w", err)
	}
	if len(reader.File) == 0 || len(reader.File) > MaxEntryCount {
		return nil, fmt.Errorf("entry count must be between 1 and %d", MaxEntryCount)
	}

	var totalUncompressed int64
	seenNames := make(map[string]bool, len(reader.File))
	for _, file := range reader.File {
		if seenNames[file.Name] {
			return nil, fmt.Errorf("duplicate zip entry %q", file.Name)
		}
		seenNames[file.Name] = true
		if err := ValidateSafeName(file.Name); err != nil {
			return nil, fmt.Errorf("invalid zip entry %q: %w", file.Name, err)
		}
		if !file.Mode().IsRegular() {
			return nil, fmt.Errorf("zip entry %q is not a regular file", file.Name)
		}
		totalUncompressed += int64(file.UncompressedSize64)
		if totalUncompressed > MaxTotalSize {
			return nil, errors.New("uncompressed archive exceeds 4 MiB")
		}
	}

	var manifest Manifest
	remaining := totalUncompressed
	shardData := make(map[int][]byte)
	for _, file := range reader.File {
		content, consumed, err := readZipFile(file, remaining)
		if err != nil {
			return nil, fmt.Errorf("reading %q: %w", file.Name, err)
		}
		remaining -= consumed
		switch {
		case file.Name == ManifestName:
			decoder := json.NewDecoder(bytes.NewReader(content))
			decoder.DisallowUnknownFields()
			if err := decoder.Decode(&manifest); err != nil {
				return nil, fmt.Errorf("invalid manifest: %w", err)
			}
			if decoder.Decode(&struct{}{}) != io.EOF {
				return nil, errors.New("invalid manifest: unexpected trailing data")
			}
		case strings.HasPrefix(file.Name, ShardPrefix) && strings.HasSuffix(file.Name, ShardSuffix):
			numberText := strings.TrimSuffix(strings.TrimPrefix(file.Name, ShardPrefix), ShardSuffix)
			number, err := strconv.Atoi(numberText)
			if err != nil || numberText != strconv.Itoa(number) {
				return nil, fmt.Errorf("invalid shard file name %q", file.Name)
			}
			if _, exists := shardData[number]; exists {
				return nil, fmt.Errorf("duplicate shard number %d", number)
			}
			shardData[number] = content
		default:
			return nil, fmt.Errorf("unexpected zip entry %q", file.Name)
		}
	}
	if err := ValidateManifest(manifest); err != nil {
		return nil, err
	}
	for number := range shardData {
		if number < 0 || number >= manifest.K+manifest.M {
			return nil, fmt.Errorf("shard number %d out of range", number)
		}
	}
	return &Bundle{Manifest: manifest, Shards: shardData}, nil
}

func ClassifyShards(bundle *Bundle) (map[int][]byte, []int, []int) {
	hashes := make(map[int]string, len(bundle.Manifest.ShardHashes))
	for _, entry := range bundle.Manifest.ShardHashes {
		hashes[entry.Number] = entry.SHA256
	}
	valid := make(map[int][]byte)
	var bad []int
	for number, shard := range bundle.Shards {
		if len(shard) != bundle.Manifest.ShardLength || hex.EncodeToString(sha256Bytes(shard)) != hashes[number] {
			bad = append(bad, number)
			continue
		}
		valid[number] = shard
	}
	var missing []int
	for number := 0; number < bundle.Manifest.K+bundle.Manifest.M; number++ {
		if _, ok := bundle.Shards[number]; !ok {
			missing = append(missing, number)
		}
	}
	sort.Ints(bad)
	sort.Ints(missing)
	return valid, missing, bad
}

func sha256Bytes(data []byte) []byte {
	hash := sha256.Sum256(data)
	return hash[:]
}

func isValidSHA256(value string) bool {
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == sha256.Size
}

func readZipFile(file *zip.File, budget int64) ([]byte, int64, error) {
	readCloser, err := file.Open()
	if err != nil {
		return nil, 0, err
	}
	defer readCloser.Close()
	var buffer bytes.Buffer
	written, err := io.Copy(&buffer, io.LimitReader(readCloser, budget+1))
	if err != nil {
		return nil, 0, err
	}
	if written > budget {
		return nil, written, errors.New("uncompressed archive exceeds 4 MiB")
	}
	return buffer.Bytes(), written, nil
}

func writeBytes(writer *zip.Writer, name string, content []byte) error {
	file, err := writer.Create(name)
	if err != nil {
		return err
	}
	_, err = file.Write(content)
	return err
}

func writeJSON(writer *zip.Writer, name string, value any) error {
	content, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	return writeBytes(writer, name, content)
}
