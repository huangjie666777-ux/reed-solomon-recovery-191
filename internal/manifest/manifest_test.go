package manifest

import "testing"

func TestShardNameRoundTrip(t *testing.T) {
	for _, n := range []int{0, 1, 11} {
		name := ShardName(n)
		got, ok := ParseShardName(name)
		if !ok || got != n {
			t.Fatalf("round trip %d -> %q -> %d (%v)", n, name, got, ok)
		}
	}
	for _, bad := range []string{"shard-1", "shard-001", "shard-123", "foo", "shard-ab"} {
		if _, ok := ParseShardName(bad); ok {
			t.Fatalf("%q parsed unexpectedly", bad)
		}
	}
}

func TestValidate(t *testing.T) {
	good := func() *Manifest {
		return &Manifest{
			FormatVersion:  FormatVersion,
			K:              3,
			M:              2,
			OriginalLength: 10,
			ShardLength:    4,
			OriginalName:   "a.bin",
			OriginalSHA256: "0000000000000000000000000000000000000000000000000000000000000000",
			Shards: []ShardEntry{
				{Number: 0, Name: "shard-00", SHA256: "0000000000000000000000000000000000000000000000000000000000000000"},
				{Number: 1, Name: "shard-01", SHA256: "0000000000000000000000000000000000000000000000000000000000000000"},
				{Number: 2, Name: "shard-02", SHA256: "0000000000000000000000000000000000000000000000000000000000000000"},
				{Number: 3, Name: "shard-03", SHA256: "0000000000000000000000000000000000000000000000000000000000000000"},
				{Number: 4, Name: "shard-04", SHA256: "0000000000000000000000000000000000000000000000000000000000000000"},
			},
		}
	}
	if err := good().Validate(); err != nil {
		t.Fatalf("good manifest rejected: %v", err)
	}
	m := good()
	m.FormatVersion = 2
	if err := m.Validate(); err == nil {
		t.Fatal("bad version accepted")
	}
	m = good()
	m.Shards[3].Number = 0
	if err := m.Validate(); err == nil {
		t.Fatal("duplicate number accepted")
	}
	m = good()
	m.Shards[1].Name = "shard-02"
	if err := m.Validate(); err == nil {
		t.Fatal("duplicate/name mismatch accepted")
	}
	m = good()
	m.OriginalName = "../evil"
	if err := m.Validate(); err == nil {
		t.Fatal("path traversal name accepted")
	}
	m = good()
	m.ShardLength = 5
	if err := m.Validate(); err == nil {
		t.Fatal("inconsistent shard length accepted")
	}
}
