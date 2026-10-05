package httpapi

import (
	"archive/zip"
	"bytes"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"reed-solomon-recovery/internal/archive"
)

func multipartRequest(t *testing.T, field, filename string, content []byte, extraFields map[string]string) *http.Request {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	for k, v := range extraFields {
		if err := mw.WriteField(k, v); err != nil {
			t.Fatal(err)
		}
	}
	part, err := mw.CreateFormFile(field, filename)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write(content); err != nil {
		t.Fatal(err)
	}
	mw.Close()
	req := httptest.NewRequest(http.MethodPost, "/encode", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	return req
}

func TestEncodeAndRecoverHTTP(t *testing.T) {
	h := NewRouter()
	orig := bytes.Repeat([]byte("0123456789ABCDEF"), 100)

	req := multipartRequest(t, "file", "data.bin", orig, map[string]string{"k": "4", "m": "2"})
	req.URL.Path = "/encode"
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("encode status %d: %s", rec.Code, rec.Body.String())
	}
	encoded := rec.Body.Bytes()

	// Remove shards 0 and 4 from the archive before recovery.
	zr, err := zip.NewReader(bytes.NewReader(encoded), int64(len(encoded)))
	if err != nil {
		t.Fatal(err)
	}
	keep := []archive.File{}
	for _, zf := range zr.File {
		if zf.Name == "shard-00" || zf.Name == "shard-04" {
			continue
		}
		rc, _ := zf.Open()
		data, _ := io.ReadAll(rc)
		rc.Close()
		keep = append(keep, archive.File{Name: zf.Name, Data: data})
	}
	damaged, err := archive.Build(keep)
	if err != nil {
		t.Fatal(err)
	}
	req2 := multipartRequest(t, "archive", "damaged.zip", damaged, nil)
	req2.URL.Path = "/recover"
	rec2 := httptest.NewRecorder()
	h.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusOK {
		t.Fatalf("recover status %d: %s", rec2.Code, rec2.Body.String())
	}
	out, err := archive.Read(rec2.Body.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, f := range out {
		if f.Name == "data.bin" {
			found = true
			if !bytes.Equal(f.Data, orig) {
				t.Fatal("recovered content mismatch")
			}
		}
	}
	if !found {
		t.Fatal("recovered archive missing data.bin")
	}
}

func TestUnrecoverableHTTPStatus422(t *testing.T) {
	h := NewRouter()
	orig := []byte("hello world!!!")
	req := multipartRequest(t, "file", "f.txt", orig, map[string]string{"k": "3", "m": "1"})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatal(rec.Body.String())
	}
	entries, _ := archive.Read(rec.Body.Bytes())
	kept := []archive.File{}
	for _, e := range entries {
		if e.Name == "shard-00" || e.Name == "shard-01" {
			continue
		}
		kept = append(kept, e)
	}
	damaged, _ := archive.Build(kept)
	req2 := multipartRequest(t, "archive", "d.zip", damaged, nil)
	req2.URL.Path = "/recover"
	rec2 := httptest.NewRecorder()
	h.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusUnprocessableEntity {
		t.Fatalf("want 422, got %d: %s", rec2.Code, rec2.Body.String())
	}
	if !strings.Contains(rec2.Body.String(), "unrecoverable") {
		t.Fatalf("report missing status: %s", rec2.Body.String())
	}
}

func TestEncodeBadParams(t *testing.T) {
	h := NewRouter()
	req := multipartRequest(t, "file", "f", []byte("x"), map[string]string{"k": "9", "m": "1"})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d", rec.Code)
	}
}
