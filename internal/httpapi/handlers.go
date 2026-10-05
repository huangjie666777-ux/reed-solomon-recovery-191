// Package httpapi exposes the offline Reed-Solomon encode/recover HTTP API.
package httpapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"reed-solomon-recovery/internal/manifest"
	"reed-solomon-recovery/internal/service"
)

const (
	// encodeBodyLimit is a generous cap covering multipart overhead; the
	// embedded file itself is bounded separately to 1 MiB.
	encodeBodyLimit = 8 << 20
	// recoverBodyLimit covers a damaged ZIP whose uncompressed total is <= 4 MiB.
	recoverBodyLimit = 8 << 20
)

// NewRouter builds the application router.
func NewRouter() http.Handler {
	r := chi.NewRouter()
	r.Get("/", handleIndex)
	r.Post("/encode", handleEncode)
	r.Post("/recover", handleRecover)
	r.NotFound(func(w http.ResponseWriter, req *http.Request) {
		writeError(w, http.StatusNotFound, "not found")
	})
	r.MethodNotAllowed(func(w http.ResponseWriter, req *http.Request) {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	})
	return r
}

type errorBody struct {
	Error string `json:"error"`
}

func writeError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(errorBody{Error: msg})
}

func handleIndex(w http.ResponseWriter, req *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	fmt.Fprintln(w, "Offline Reed-Solomon erasure recovery service.")
	fmt.Fprintln(w, "POST /encode   multipart fields: file, k(2-8), m(1-4)")
	fmt.Fprintln(w, "POST /recover  multipart field: archive (a ZIP with manifest.json plus surviving shards)")
}

func handleEncode(w http.ResponseWriter, req *http.Request) {
	req.Body = http.MaxBytesReader(w, req.Body, encodeBodyLimit)
	if err := req.ParseMultipartForm(encodeBodyLimit); err != nil {
		var mbErr *http.MaxBytesError
		if errors.As(err, &mbErr) {
			writeError(w, http.StatusRequestEntityTooLarge, "request body too large")
			return
		}
		writeError(w, http.StatusBadRequest, "invalid multipart form: "+err.Error())
		return
	}
	k, err := parseKM(req.FormValue("k"), 2, 8, "k")
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	m, err := parseKM(req.FormValue("m"), 1, 4, "m")
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	file, header, err := req.FormFile("file")
	if err != nil {
		writeError(w, http.StatusBadRequest, "missing or unreadable multipart 'file' field")
		return
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, service.MaxFileSize+1))
	if err != nil {
		writeError(w, http.StatusBadRequest, "cannot read uploaded file")
		return
	}
	if len(data) < service.MinFileSize {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("file must be at least %d byte", service.MinFileSize))
		return
	}
	if len(data) > service.MaxFileSize {
		writeError(w, http.StatusRequestEntityTooLarge, fmt.Sprintf("file must be at most %d bytes (1 MiB)", service.MaxFileSize))
		return
	}
	name := sanitizeUploadName(header.Filename)
	if !manifest.SafeOriginalName(name) {
		writeError(w, http.StatusBadRequest, "invalid or empty file name")
		return
	}
	result, err := service.Encode(name, data, k, m)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", "attachment; filename=encoded.zip")
	_, _ = w.Write(result.ZIP)
}

func handleRecover(w http.ResponseWriter, req *http.Request) {
	req.Body = http.MaxBytesReader(w, req.Body, recoverBodyLimit)
	if err := req.ParseMultipartForm(recoverBodyLimit); err != nil {
		var mbErr *http.MaxBytesError
		if errors.As(err, &mbErr) {
			writeError(w, http.StatusRequestEntityTooLarge, "request body too large")
			return
		}
		writeError(w, http.StatusBadRequest, "invalid multipart form: "+err.Error())
		return
	}
	arch, header, err := req.FormFile("archive")
	if err != nil {
		writeError(w, http.StatusBadRequest, "missing or unreadable multipart 'archive' field")
		return
	}
	defer arch.Close()
	zipBytes, err := io.ReadAll(io.LimitReader(arch, recoverBodyLimit+1))
	if err != nil {
		writeError(w, http.StatusBadRequest, "cannot read uploaded archive")
		return
	}
	if len(zipBytes) > recoverBodyLimit {
		writeError(w, http.StatusRequestEntityTooLarge, "archive too large")
		return
	}
	_ = header

	_, _, report, resultZIP, err := service.Recover(zipBytes)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if report.Status != "success" {
		repJSON, _ := json.MarshalIndent(report, "", "  ")
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusUnprocessableEntity)
		_, _ = w.Write(repJSON)
		return
	}
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", "attachment; filename=recovered.zip")
	_, _ = w.Write(resultZIP)
}

func parseKM(s string, min, max int, label string) (int, error) {
	if s == "" {
		return 0, fmt.Errorf("missing required integer field %s", label)
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0, fmt.Errorf("%s must be an integer in [%d,%d]", label, min, max)
	}
	if n < min || n > max {
		return 0, fmt.Errorf("%s must be in [%d,%d], got %d", label, min, max, n)
	}
	return n, nil
}

// sanitizeUploadName strips any client-supplied path components; the server
// only ever stores the base name inside the archive.
func sanitizeUploadName(name string) string {
	for i := len(name) - 1; i >= 0; i-- {
		if name[i] == '/' || name[i] == '\\' {
			name = name[i+1:]
			break
		}
	}
	return name
}
