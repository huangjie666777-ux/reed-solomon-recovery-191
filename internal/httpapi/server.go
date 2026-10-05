package httpapi

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"reed-solomon-recovery/internal/archive"
	"reed-solomon-recovery/internal/reedsolomon"
)

type Server struct {
	router http.Handler
}

type errorResponse struct {
	Error string `json:"error"`
}

func NewServer() *Server {
	r := chi.NewRouter()
	r.Get("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	r.Post("/encode", handleEncode)
	r.Post("/recover", handleRecover)
	return &Server{router: r}
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.router.ServeHTTP(w, r)
}

func Router() http.Handler {
	return NewServer()
}

func handleEncode(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, archive.MaxTotalSize)
	if err := r.ParseMultipartForm(archive.MaxTotalSize); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	defer func() {
		if r.MultipartForm != nil {
			_ = r.MultipartForm.RemoveAll()
		}
	}()

	k, err := parsePositiveFormInt(r, "k")
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	m, err := parsePositiveFormInt(r, "m")
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		writeError(w, http.StatusBadRequest, fmt.Errorf("missing file field: %w", err))
		return
	}
	defer file.Close()

	data, err := io.ReadAll(io.LimitReader(file, 1<<20+1))
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if err := archive.ValidateParameters(k, m, len(data)); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	name := safeUploadName(header.Filename)
	if err := archive.ValidateSafeName(name); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Errorf("invalid file name: %w", err))
		return
	}

	shards, shardLength, _, err := reedsolomon.Encode(data, k, m)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	manifest := archive.NewManifest(k, m, len(data), shardLength, name, data, shards)
	result, err := archive.WriteEncodeZip(manifest, shards)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", `attachment; filename="encoded.zip"`)
	_, _ = w.Write(result)
}

func handleRecover(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, archive.MaxTotalSize)
	input, err := io.ReadAll(io.LimitReader(r.Body, archive.MaxTotalSize+1))
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if int64(len(input)) > archive.MaxTotalSize {
		writeError(w, http.StatusBadRequest, errors.New("upload exceeds 4 MiB"))
		return
	}

	bundle, err := archive.ReadBundle(input)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	valid, missing, bad := archive.ClassifyShards(bundle)
	used := sortedNumbers(valid)
	baseReport := archive.RecoveryReport{
		K:                   bundle.Manifest.K,
		M:                   bundle.Manifest.M,
		OriginalName:        bundle.Manifest.OriginalName,
		OriginalSHA256:      bundle.Manifest.OriginalSHA256,
		MissingShardNumbers: normalizeInts(missing),
		BadShardNumbers:     normalizeInts(bad),
		UsedShardNumbers:    normalizeInts(used),
		TrustNotice:         "A matching SHA-256 verifies reconstruction against the supplied manifest; it does not prove the file's origin or manifest trustworthiness.",
	}

	fail := func(message string) {
		report := baseReport
		report.Status = "unrecoverable"
		report.Error = message
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnprocessableEntity)
		_ = json.NewEncoder(w).Encode(report)
	}
	if len(valid) < bundle.Manifest.K {
		fail(fmt.Sprintf("need %d valid shards, have %d", bundle.Manifest.K, len(valid)))
		return
	}

	generator, err := reedsolomon.SystemGenerator(bundle.Manifest.K+bundle.Manifest.M, bundle.Manifest.K)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	selected := make(map[int][]byte, bundle.Manifest.K)
	for _, number := range used[:bundle.Manifest.K] {
		selected[number] = valid[number]
	}
	baseReport.UsedShardNumbers = normalizeInts(used[:bundle.Manifest.K])
	reconstructed, err := reedsolomon.Reconstruct(generator, bundle.Manifest.K, selected)
	if err != nil {
		fail(fmt.Sprintf("matrix reconstruction failed: %v", err))
		return
	}
	if len(reconstructed) < bundle.Manifest.OriginalLength {
		fail("reconstruction is shorter than the recorded original length")
		return
	}
	original := reconstructed[:bundle.Manifest.OriginalLength]
	actualHash := sha256Hex(original)
	if actualHash != bundle.Manifest.OriginalSHA256 {
		report := baseReport
		report.Status = "unrecoverable"
		report.RecoveredSHA256 = actualHash
		report.Error = "reconstructed file SHA-256 does not match manifest"
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnprocessableEntity)
		_ = json.NewEncoder(w).Encode(report)
		return
	}

	report := baseReport
	report.Status = "recovered"
	report.RecoveredSHA256 = actualHash
	result, err := archive.WriteRecoveryZip(bundle.Manifest.OriginalName, original, report)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", `attachment; filename="recovered.zip"`)
	_, _ = w.Write(result)
}

func parsePositiveFormInt(r *http.Request, field string) (int, error) {
	value := strings.TrimSpace(r.FormValue(field))
	if value == "" {
		return 0, fmt.Errorf("missing %s", field)
	}
	number, err := strconv.Atoi(value)
	if err != nil {
		return 0, fmt.Errorf("invalid %s: %w", field, err)
	}
	return number, nil
}

func safeUploadName(name string) string {
	name = filepath.Base(filepath.ToSlash(name))
	name = strings.TrimSpace(name)
	if name == "" || name == "." || name == "/" {
		return "original.bin"
	}
	return name
}

func sortedNumbers(values map[int][]byte) []int {
	numbers := make([]int, 0, len(values))
	for number := range values {
		numbers = append(numbers, number)
	}
	sort.Ints(numbers)
	return numbers
}

func normalizeInts(values []int) []int {
	if len(values) == 0 {
		return []int{}
	}
	return append([]int(nil), values...)
}

func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func writeError(w http.ResponseWriter, status int, err error) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(errorResponse{Error: err.Error()})
}
