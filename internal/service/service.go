// Package service implements offline Reed-Solomon encode and recover flows.
package service

import (
	"encoding/json"
	"fmt"
	"sort"

	"reed-solomon-recovery/internal/archive"
	"reed-solomon-recovery/internal/manifest"
	"reed-solomon-recovery/internal/reedsolomon"
)

const (
	// MinFileSize / MaxFileSize bound uploads for encoding.
	MinFileSize  = 1
	MaxFileSize  = 1 << 20
	ManifestName = "manifest.json"
	ReportName   = "report.json"
)

// TrustNote is embedded in every recovery report: matching hashes only prove
// byte-for-byte agreement with the manifest, not a trusted origin.
const TrustNote = "SHA-256 摘要仅能证明分片及重建文件与清单记录一致，不证明清单或文件来源可信。"

// Report describes a recovery attempt.
type Report struct {
	Status          string `json:"status"`
	K               int    `json:"k"`
	M               int    `json:"m"`
	OriginalName    string `json:"original_name"`
	OriginalSHA256  string `json:"original_sha256,omitempty"`
	MissingShards   []int  `json:"missing_shards"`
	CorruptShards   []int  `json:"corrupt_shards"`
	UsedShards      []int  `json:"used_shards,omitempty"`
	RecoveredSHA256 string `json:"recovered_sha256,omitempty"`
	Error           string `json:"error,omitempty"`
	TrustNote       string `json:"trust_note"`
}

// EncodeResult carries the produced archive bytes.
type EncodeResult struct {
	ZIP []byte
}

// Encode splits data into k zero-padded data shards, derives m parity shards
// and returns a ZIP holding manifest.json plus all numbered shards.
func Encode(originalName string, data []byte, k, m int) (*EncodeResult, error) {
	if len(data) < MinFileSize || len(data) > MaxFileSize {
		return nil, fmt.Errorf("file size must be between %d and %d bytes, got %d", MinFileSize, MaxFileSize, len(data))
	}
	if !manifest.SafeOriginalName(originalName) {
		return nil, fmt.Errorf("invalid original file name %q", originalName)
	}
	codec, err := reedsolomon.New(k, m)
	if err != nil {
		return nil, err
	}
	dataShards, shardLen := reedsolomon.Split(data, k)
	shards := codec.Encode(dataShards, shardLen)

	mf := &manifest.Manifest{
		FormatVersion:  manifest.FormatVersion,
		K:              k,
		M:              m,
		OriginalLength: len(data),
		ShardLength:    shardLen,
		OriginalName:   originalName,
		OriginalSHA256: manifest.HexSHA256(data),
		Shards:         make([]manifest.ShardEntry, k+m),
	}
	files := make([]archive.File, 0, k+m+1)
	for i := 0; i < k+m; i++ {
		name := manifest.ShardName(i)
		mf.Shards[i] = manifest.ShardEntry{
			Number: i,
			Name:   name,
			SHA256: manifest.HexSHA256(shards[i]),
		}
		files = append(files, archive.File{Name: name, Data: shards[i]})
	}
	mfData, err := json.MarshalIndent(mf, "", "  ")
	if err != nil {
		return nil, err
	}
	files = append(files, archive.File{Name: ManifestName, Data: mfData})
	zipData, err := archive.Build(files)
	if err != nil {
		return nil, err
	}
	return &EncodeResult{ZIP: zipData}, nil
}

// Recover reads a damaged archive (full manifest plus some shards), validates
// each present shard against the manifest by length and SHA-256, rebuilds the
// data from exactly k distinct valid shards, trims padding and verifies the
// original hash. On success it returns the recovered file, the report and the
// result ZIP. A non-nil error indicates an invalid request; an unrecoverable
// (but well-formed) archive yields ok=false with a populated report.
func Recover(zipBytes []byte) (originalName string, recovered []byte, report *Report, resultZIP []byte, err error) {
	entries, err := archive.Read(zipBytes)
	if err != nil {
		return "", nil, nil, nil, err
	}

	var mfData []byte
	present := map[string][]byte{}
	for _, e := range entries {
		switch e.Name {
		case ManifestName:
			if mfData != nil {
				return "", nil, nil, nil, fmt.Errorf("duplicate %s", ManifestName)
			}
			mfData = e.Data
		default:
			if _, ok := manifest.ParseShardName(e.Name); !ok {
				return "", nil, nil, nil, fmt.Errorf("unexpected entry %q: archive may only contain %s and numbered shards", e.Name, ManifestName)
			}
			if _, dup := present[e.Name]; dup {
				return "", nil, nil, nil, fmt.Errorf("duplicate shard entry %q", e.Name)
			}
			present[e.Name] = e.Data
		}
	}
	if mfData == nil {
		return "", nil, nil, nil, fmt.Errorf("archive has no %s", ManifestName)
	}
	var mf manifest.Manifest
	if err := json.Unmarshal(mfData, &mf); err != nil {
		return "", nil, nil, nil, fmt.Errorf("invalid manifest: %w", err)
	}
	if err := mf.Validate(); err != nil {
		return "", nil, nil, nil, fmt.Errorf("invalid manifest: %w", err)
	}

	rep := &Report{
		K:              mf.K,
		M:              mf.M,
		OriginalName:   mf.OriginalName,
		OriginalSHA256: mf.OriginalSHA256,
		MissingShards:  []int{},
		CorruptShards:  []int{},
		TrustNote:      TrustNote,
	}

	expected := map[int]manifest.ShardEntry{}
	for _, s := range mf.Shards {
		expected[s.Number] = s
	}
	valid := map[int][]byte{}
	for name, data := range present {
		num, _ := manifest.ParseShardName(name)
		if num >= mf.K+mf.M {
			return "", nil, nil, nil, fmt.Errorf("entry %q is not a legal shard number for k=%d m=%d", name, mf.K, mf.M)
		}
		entry := expected[num]
		if len(data) != mf.ShardLength || manifest.HexSHA256(data) != entry.SHA256 {
			rep.CorruptShards = append(rep.CorruptShards, num)
			continue
		}
		if _, dup := valid[num]; dup {
			return "", nil, nil, nil, fmt.Errorf("duplicate valid shard number %d", num)
		}
		valid[num] = data
	}
	for _, s := range mf.Shards {
		name := manifest.ShardName(s.Number)
		if _, ok := present[name]; !ok {
			rep.MissingShards = append(rep.MissingShards, s.Number)
		}
	}
	sort.Ints(rep.CorruptShards)
	sort.Ints(rep.MissingShards)

	fail := func(msg string) ([]byte, *Report) {
		rep.Status = "unrecoverable"
		rep.Error = msg
		return nil, rep
	}
	if len(valid) < mf.K {
		recovered, rep = fail(fmt.Sprintf("only %d intact shards available, need %d distinct shards", len(valid), mf.K))
		return mf.OriginalName, recovered, rep, nil, nil
	}
	nums := make([]int, 0, len(valid))
	for num := range valid {
		nums = append(nums, num)
	}
	sort.Ints(nums)
	chosen := nums[:mf.K]
	available := map[int][]byte{}
	for _, num := range chosen {
		available[num] = valid[num]
		rep.UsedShards = append(rep.UsedShards, num)
	}
	codec, err := reedsolomon.New(mf.K, mf.M)
	if err != nil {
		return "", nil, nil, nil, err
	}
	dataShards, err := codec.Reconstruct(available, mf.ShardLength)
	if err != nil {
		recovered, rep = fail(err.Error())
		return mf.OriginalName, recovered, rep, nil, nil
	}
	recovered = reedsolomon.Join(dataShards, mf.K, mf.OriginalLength)
	gotSHA := manifest.HexSHA256(recovered)
	rep.RecoveredSHA256 = gotSHA
	if gotSHA != mf.OriginalSHA256 {
		recovered, rep = fail("reconstructed file SHA-256 does not match manifest; refusing to output a file whose integrity cannot be verified")
		return mf.OriginalName, recovered, rep, nil, nil
	}
	rep.Status = "success"
	repJSON, err := json.MarshalIndent(rep, "", "  ")
	if err != nil {
		return "", nil, nil, nil, err
	}
	resultZIP, err = archive.Build([]archive.File{
		{Name: mf.OriginalName, Data: recovered},
		{Name: ReportName, Data: repJSON},
	})
	if err != nil {
		return "", nil, nil, nil, err
	}
	return mf.OriginalName, recovered, rep, resultZIP, nil
}
