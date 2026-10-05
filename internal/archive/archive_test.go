package archive

import (
	"archive/zip"
	"bytes"
	"strings"
	"testing"
)

func TestBuildAndRead(t *testing.T) {
	files := []File{{Name: "a.txt", Data: []byte("hello")}, {Name: "b.bin", Data: []byte{0, 1, 2}}}
	z, err := Build(files)
	if err != nil {
		t.Fatal(err)
	}
	got, err := Read(z)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Name != "a.txt" || !bytes.Equal(got[1].Data, []byte{0, 1, 2}) {
		t.Fatalf("round trip mismatch: %+v", got)
	}
}

func TestUnsafeNames(t *testing.T) {
	for _, name := range []string{"../x", "/etc/passwd", "a/b", "a\\b", "..", "", "con.txt", "\x00abc"} {
		if SafeEntryName(name) {
			t.Fatalf("unsafe name accepted: %q", name)
		}
	}
}

func TestRejectTraversalZip(t *testing.T) {
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	fw, _ := w.Create("../escape.txt")
	fw.Write([]byte("evil"))
	w.Close()
	if _, err := Read(buf.Bytes()); err == nil {
		t.Fatal("traversal entry accepted")
	}
}

func TestRejectTooManyEntries(t *testing.T) {
	many := make([]File, MaxEntries+1)
	for i := range many {
		many[i] = File{Name: string(rune('a'+i)) + ".dat", Data: []byte("x")}
	}
	if _, err := Build(many); err == nil {
		t.Fatal("over-entry archive accepted by Build")
	}
}

func TestRejectTotalSize(t *testing.T) {
	big := []File{{Name: "big", Data: bytes.Repeat([]byte{1}, MaxTotalBytes+1)}}
	if _, err := Build(big); err == nil {
		t.Fatal("oversized entry accepted")
	}
}

func TestRejectDuplicateNames(t *testing.T) {
	if SafeEntryName("a/b") {
		t.Fatal("separator accepted")
	}
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	for _, n := range []string{"a", "a"} {
		fw, _ := w.Create(n)
		fw.Write([]byte("x"))
	}
	w.Close()
	if _, err := Read(buf.Bytes()); err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("want duplicate error, got %v", err)
	}
}
