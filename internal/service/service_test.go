package service

import (
	"archive/zip"
	"bytes"
	"fmt"
	"math/rand"
	"strings"
	"testing"

	"reed-solomon-recovery/internal/archive"
)

func extractEntries(t *testing.T, z []byte) map[string][]byte {
	t.Helper()
	files, err := archive.Read(z)
	if err != nil {
		t.Fatal(err)
	}
	m := map[string][]byte{}
	for _, f := range files {
		m[f.Name] = f.Data
	}
	return m
}

func damagedArchive(t *testing.T, encoded []byte, mutate map[string][]byte, remove map[string]bool, dup string) []byte {
	t.Helper()
	entries, err := archive.Read(encoded)
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	write := func(name string, data []byte) {
		fw, werr := zw.Create(name)
		if werr != nil {
			t.Fatal(werr)
		}
		if _, werr := fw.Write(data); werr != nil {
			t.Fatal(werr)
		}
	}
	for _, e := range entries {
		if remove[e.Name] {
			continue
		}
		if d, ok := mutate[e.Name]; ok {
			e.Data = d
		}
		write(e.Name, e.Data)
		if e.Name == dup {
			write(e.Name, e.Data)
		}
	}
	zw.Close()
	return buf.Bytes()
}

func TestEncodeRecoverRoundTrip(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	for _, tc := range []struct{ k, m, size int }{
		{2, 1, 1}, {3, 2, 100}, {8, 4, 1 << 20},
	} {
		data := make([]byte, tc.size)
		rng.Read(data)
		res, err := Encode("photo.dat", data, tc.k, tc.m)
		if err != nil {
			t.Fatalf("encode: %v", err)
		}
		entries := extractEntries(t, res.ZIP)
		if len(entries) != tc.k+tc.m+1 {
			t.Fatalf("entry count = %d", len(entries))
		}
		if _, ok := entries[ManifestName]; !ok {
			t.Fatal("manifest missing")
		}
		// Remove m shards; k intact shards must remain for every (k,m).
		removed := map[string]bool{}
		for i := 0; i < tc.m; i++ {
			removed[fmt.Sprintf("shard-%02d", i)] = true
		}
		damaged := damagedArchive(t, res.ZIP, nil, removed, "")
		name, recovered, report, resultZIP, err := Recover(damaged)
		if err != nil {
			t.Fatalf("recover: %v", err)
		}
		if report.Status != "success" || !bytes.Equal(recovered, data) {
			t.Fatalf("recovery failed: %+v", report)
		}
		if name != "photo.dat" || len(report.UsedShards) != tc.k {
			t.Fatalf("bad report: %+v", report)
		}
		out := extractEntries(t, resultZIP)
		if _, ok := out["photo.dat"]; !ok {
			t.Fatal("result zip missing original")
		}
		if _, ok := out[ReportName]; !ok {
			t.Fatal("result zip missing report")
		}
	}
}

func TestRecoverWithCorruptShard(t *testing.T) {
	data := []byte("the quick brown fox jumps over")
	res, err := Encode("f.txt", data, 3, 2)
	if err != nil {
		t.Fatal(err)
	}
	// Corrupt shard-00 and delete shard-01: 3 intact shards remain.
	mut := map[string][]byte{"shard-00": bytes.Repeat([]byte{0xff}, 11)}
	damaged := damagedArchive(t, res.ZIP, mut, map[string]bool{"shard-01": true}, "")
	_, recovered, report, _, err := Recover(damaged)
	if err != nil {
		t.Fatal(err)
	}
	if report.Status != "success" || !bytes.Equal(recovered, data) {
		t.Fatalf("expected success, got %+v", report)
	}
	if len(report.CorruptShards) != 1 || report.CorruptShards[0] != 0 {
		t.Fatalf("corrupt list wrong: %v", report.CorruptShards)
	}
	if len(report.MissingShards) != 1 || report.MissingShards[0] != 1 {
		t.Fatalf("missing list wrong: %v", report.MissingShards)
	}
}

func TestRecoverTooFewShards(t *testing.T) {
	data := []byte("abcdefghij")
	res, _ := Encode("f.txt", data, 3, 1)
	damaged := damagedArchive(t, res.ZIP, nil, map[string]bool{"shard-00": true, "shard-01": true}, "")
	_, recovered, report, resultZIP, err := Recover(damaged)
	if err != nil {
		t.Fatal(err)
	}
	if report.Status != "unrecoverable" || resultZIP != nil || recovered != nil {
		t.Fatalf("expected unrecoverable with no pseudo file, got %+v zip=%v", report, resultZIP != nil)
	}
	if !strings.Contains(report.Error, "need 3") {
		t.Fatalf("unexpected error: %s", report.Error)
	}
}

func TestRecoverRejectsDuplicateShardEntry(t *testing.T) {
	data := []byte("abcdefghij")
	res, _ := Encode("f.txt", data, 3, 1)
	damaged := damagedArchive(t, res.ZIP, nil, nil, "shard-00")
	if _, _, _, _, err := Recover(damaged); err == nil {
		t.Fatal("duplicate shard entry must be rejected")
	}
}

func TestRecoverRejectsUnexpectedEntry(t *testing.T) {
	data := []byte("abcdefghij")
	res, _ := Encode("f.txt", data, 3, 1)
	entries, _ := archive.Read(res.ZIP)
	entries = append(entries, archive.File{Name: "extra.txt", Data: []byte("nope")})
	z, _ := archive.Build(entries)
	if _, _, _, _, err := Recover(z); err == nil || !strings.Contains(err.Error(), "unexpected entry") {
		t.Fatalf("want unexpected-entry error, got %v", err)
	}
}

func TestRecoverFailsOnForgedOriginalHash(t *testing.T) {
	data := []byte("abcdefghij")
	res, _ := Encode("f.txt", data, 3, 1)
	entries, _ := archive.Read(res.ZIP)
	for i := range entries {
		if entries[i].Name != ManifestName {
			continue
		}
		mf := bytes.Replace(entries[i].Data, []byte(`"original_sha256": "`), []byte(`"original_sha256": "f`), 1)
		entries[i].Data = mf
	}
	z, _ := archive.Build(entries)
	_, recovered, report, resultZIP, err := Recover(z)
	if err != nil {
		if !strings.Contains(err.Error(), "invalid manifest") {
			t.Fatalf("unexpected: %v", err)
		}
		return
	}
	_ = recovered
	if report.Status == "success" || resultZIP != nil {
		t.Fatal("forged original hash must not produce a file")
	}
}

func TestEncodeValidation(t *testing.T) {
	if _, err := Encode("f", nil, 3, 2); err == nil {
		t.Fatal("empty file accepted")
	}
	if _, err := Encode("f", make([]byte, MaxFileSize+1), 3, 2); err == nil {
		t.Fatal("oversized file accepted")
	}
	if _, err := Encode("../f", []byte("x"), 3, 2); err == nil {
		t.Fatal("traversal name accepted")
	}
}
