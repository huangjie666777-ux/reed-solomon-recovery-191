package reedsolomon

import (
	"errors"
	"fmt"
	"sort"

	"reed-solomon-recovery/internal/gf256"
)

var ErrSingularMatrix = errors.New("singular matrix")

// Vandermonde builds V where V[i][j] = (i+1)^j in GF(256).
func Vandermonde(rows, columns int) [][]byte {
	v := make([][]byte, rows)
	for i := range v {
		v[i] = make([]byte, columns)
		base := byte(i + 1)
		v[i][0] = 1
		for j := 1; j < columns; j++ {
			v[i][j] = gf256.Mul(v[i][j-1], base)
		}
	}
	return v
}

// Invert returns the GF256 inverse of a square matrix.
func Invert(matrix [][]byte) ([][]byte, error) {
	n := len(matrix)
	if n == 0 {
		return nil, errors.New("empty matrix")
	}
	inverse := make([][]byte, n)
	for i, row := range matrix {
		if len(row) != n {
			return nil, fmt.Errorf("row %d has length %d, want %d", i, len(row), n)
		}
		inverse[i] = make([]byte, 2*n)
		copy(inverse[i], row)
		inverse[i][n+i] = 1
	}

	for column := 0; column < n; column++ {
		pivot := column
		for pivot < n && inverse[pivot][column] == 0 {
			pivot++
		}
		if pivot == n {
			return nil, ErrSingularMatrix
		}
		inverse[column], inverse[pivot] = inverse[pivot], inverse[column]

		pivotInverse, ok := gf256.Inverse(inverse[column][column])
		if !ok {
			return nil, ErrSingularMatrix
		}
		for j := 0; j < 2*n; j++ {
			inverse[column][j] = gf256.Mul(inverse[column][j], pivotInverse)
		}

		for row := 0; row < n; row++ {
			if row == column {
				continue
			}
			factor := inverse[row][column]
			if factor == 0 {
				continue
			}
			for j := 0; j < 2*n; j++ {
				inverse[row][j] ^= gf256.Mul(factor, inverse[column][j])
			}
		}
	}

	result := make([][]byte, n)
	for i := range result {
		result[i] = inverse[i][n:]
	}
	return result, nil
}

// SystemGenerator builds V * inverse(V's first k rows).
func SystemGenerator(total, k int) ([][]byte, error) {
	if total < k || k <= 0 || total > 255 {
		return nil, fmt.Errorf("invalid generator dimensions %d x %d", total, k)
	}
	v := Vandermonde(total, k)
	topInverse, err := Invert(v[:k])
	if err != nil {
		return nil, err
	}
	generator := make([][]byte, total)
	for i := range generator {
		generator[i] = make([]byte, k)
		for j := 0; j < k; j++ {
			for column := 0; column < k; column++ {
				generator[i][j] ^= gf256.Mul(v[i][column], topInverse[column][j])
			}
		}
	}
	return generator, nil
}

// Split divides data into k equal shards, zero-padding the final shard.
func Split(data []byte, k int) [][]byte {
	shardLength := (len(data) + k - 1) / k
	shards := make([][]byte, k)
	for i := range shards {
		start := i * shardLength
		if start >= len(data) {
			shards[i] = make([]byte, shardLength)
			continue
		}
		end := start + shardLength
		if end > len(data) {
			end = len(data)
		}
		shards[i] = make([]byte, shardLength)
		copy(shards[i], data[start:end])
	}
	return shards
}

// Encode returns k data shards followed by m parity shards.
func Encode(data []byte, k, m int) ([][]byte, int, [][]byte, error) {
	generator, err := SystemGenerator(k+m, k)
	if err != nil {
		return nil, 0, nil, err
	}
	shards := Split(data, k)
	shardLength := len(shards[0])
	encoded := make([][]byte, k+m)
	for i := 0; i < k; i++ {
		encoded[i] = append([]byte(nil), shards[i]...)
	}
	for i := k; i < k+m; i++ {
		encoded[i] = make([]byte, shardLength)
		for j := 0; j < k; j++ {
			coefficient := generator[i][j]
			if coefficient == 0 {
				continue
			}
			for offset, value := range shards[j] {
				encoded[i][offset] ^= gf256.Mul(coefficient, value)
			}
		}
	}
	return encoded, shardLength, generator, nil
}

// Reconstruct recovers data from k available shard-number/shard pairs.
func Reconstruct(generator [][]byte, k int, available map[int][]byte) ([]byte, error) {
	if len(generator) < k {
		return nil, errors.New("generator is too small")
	}
	numbers := make([]int, 0, len(available))
	for number := range available {
		if number < 0 || number >= len(generator) {
			return nil, fmt.Errorf("shard number %d is out of range", number)
		}
		numbers = append(numbers, number)
	}
	if len(numbers) != k {
		return nil, fmt.Errorf("need exactly %d selected shards, got %d", k, len(numbers))
	}
	sort.Ints(numbers)
	selected := make([][]byte, k)
	for i, number := range numbers {
		if i > 0 && numbers[i-1] == number {
			return nil, fmt.Errorf("duplicate shard number %d", number)
		}
		selected[i] = generator[number]
	}
	inverse, err := Invert(selected)
	if err != nil {
		return nil, err
	}

	shardLength := len(available[numbers[0]])
	for _, number := range numbers {
		if len(available[number]) != shardLength {
			return nil, fmt.Errorf("shard %d has inconsistent length", number)
		}
	}
	result := make([][]byte, k)
	for j := 0; j < k; j++ {
		result[j] = make([]byte, shardLength)
		for i, number := range numbers {
			coefficient := inverse[j][i]
			if coefficient == 0 {
				continue
			}
			for offset, value := range available[number] {
				result[j][offset] ^= gf256.Mul(coefficient, value)
			}
		}
	}

	var data []byte
	for _, shard := range result {
		data = append(data, shard...)
	}
	return data, nil
}
