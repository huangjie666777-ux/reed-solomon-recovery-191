package httpapi

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"reed-solomon-recovery/internal/archive"
)

func TestEncodeAndRecover(t *testing.T) {
	server := NewServer()
	data := []byte("offline reed-solomon recovery test payload")
	encoded := mustEncode(t, server, data, 3, 2)
	bundle := mustReadZip(t, encoded)
	manifest := bundle[archive.ManifestName]
	input := writeZip(t, map[string][]byte{
		archive.ManifestName: manifest,
		archive.ShardName(2): bundle[archive.ShardName(2)],
		archive.ShardName(3): bundle[archive.ShardName(3)],
		archive.ShardName(4): bundle[archive.ShardName(4)],
	})

	request := httptest.NewRequest(http.MethodPost, "/recover", bytes.NewReader(input))
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", response.Code, response.Body.String())
	}
	recovered := mustReadZip(t, response.Body.Bytes())
	if !bytes.Equal(recovered["payload.txt"], data) {
		t.Fatal("recovered payload mismatch")
	}
	var report archive.RecoveryReport
	if err := json.Unmarshal(recovered[archive.ReportName], &report); err != nil {
		t.Fatal(err)
	}
	if report.Status != "recovered" || len(report.UsedShardNumbers) != 3 {
		t.Fatalf("unexpected report: %+v", report)
	}
}

func TestRecoverRejectsInsufficientValidShards(t *testing.T) {
	server := NewServer()
	encoded := mustEncode(t, server, []byte("payload for bad shard case"), 3, 2)
	bundle := mustReadZip(t, encoded)
	corrupt := append([]byte(nil), bundle[archive.ShardName(1)]...)
	corrupt[0] ^= 0xff
	input := writeZip(t, map[string][]byte{
		archive.ManifestName: bundle[archive.ManifestName],
		archive.ShardName(0): bundle[archive.ShardName(0)],
		archive.ShardName(1): corrupt,
		archive.ShardName(3): bundle[archive.ShardName(3)],
	})

	request := httptest.NewRequest(http.MethodPost, "/recover", bytes.NewReader(input))
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)
	if response.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422", response.Code)
	}
	var report archive.RecoveryReport
	if err := json.Unmarshal(response.Body.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	if len(report.MissingShardNumbers) != 2 || len(report.BadShardNumbers) != 1 {
		t.Fatalf("unexpected report: %+v", report)
	}
}

func TestRecoverRejectsManifestHashMismatch(t *testing.T) {
	server := NewServer()
	encoded := mustEncode(t, server, []byte("hash tamper case payload"), 2, 1)
	bundle := mustReadZip(t, encoded)
	var manifest archive.Manifest
	if err := json.Unmarshal(bundle[archive.ManifestName], &manifest); err != nil {
		t.Fatal(err)
	}
	manifest.OriginalSHA256 = strings.Repeat("0", 64)
	tamperedManifest, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	input := writeZip(t, map[string][]byte{
		archive.ManifestName: tamperedManifest,
		archive.ShardName(0): bundle[archive.ShardName(0)],
		archive.ShardName(1): bundle[archive.ShardName(1)],
	})
	request := httptest.NewRequest(http.MethodPost, "/recover", bytes.NewReader(input))
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)
	if response.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422", response.Code)
	}
}

func TestReadBundleRejectsDuplicateAndTraversal(t *testing.T) {
	var buffer bytes.Buffer
	zw := zip.NewWriter(&buffer)
	writeZipEntry(t, zw, archive.ManifestName, []byte(`{}`))
	writeZipEntry(t, zw, archive.ManifestName, []byte(`{}`))
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := archive.ReadBundle(buffer.Bytes()); err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("duplicate error = %v", err)
	}

	buffer.Reset()
	zw = zip.NewWriter(&buffer)
	writeZipEntry(t, zw, "../manifest.json", []byte(`{}`))
	zw.Close()
	if _, err := archive.ReadBundle(buffer.Bytes()); err == nil || !strings.Contains(err.Error(), "path") {
		t.Fatalf("traversal error = %v", err)
	}
}

func TestEncodeRejectsInvalidSize(t *testing.T) {
	server := NewServer()
	request := newEncodeRequest(t, []byte{}, 2, 1)
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", response.Code)
	}
}

func mustEncode(t *testing.T, handler http.Handler, data []byte, k, m int) []byte {
	t.Helper()
	request := newEncodeRequest(t, data, k, m)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("encode status = %d body = %s", response.Code, response.Body.String())
	}
	return response.Body.Bytes()
}

func newEncodeRequest(t *testing.T, data []byte, k, m int) *http.Request {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	if err := writer.WriteField("k", strconv.Itoa(k)); err != nil {
		t.Fatal(err)
	}
	if err := writer.WriteField("m", strconv.Itoa(m)); err != nil {
		t.Fatal(err)
	}
	part, err := writer.CreateFormFile("file", "payload.txt")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/encode", &body)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	return request
}
func writeZip(t *testing.T, entries map[string][]byte) []byte {
	t.Helper()
	var buffer bytes.Buffer
	zw := zip.NewWriter(&buffer)
	for name, content := range entries {
		writeZipEntry(t, zw, name, content)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func writeZipEntry(t *testing.T, zw *zip.Writer, name string, content []byte) {
	t.Helper()
	w, err := zw.Create(name)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write(content); err != nil {
		t.Fatal(err)
	}
}

func mustReadZip(t *testing.T, data []byte) map[string][]byte {
	t.Helper()
	zipReader, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	result := make(map[string][]byte)
	for _, file := range zipReader.File {
		readCloser, err := file.Open()
		if err != nil {
			t.Fatal(err)
		}
		content, err := io.ReadAll(readCloser)
		readCloser.Close()
		if err != nil {
			t.Fatal(err)
		}
		result[file.Name] = content
	}
	return result
}
