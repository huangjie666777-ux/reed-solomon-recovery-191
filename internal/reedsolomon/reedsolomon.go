// Package reedsolomon implements a systematic (k, k+m) Reed-Solomon code over
// GF(2^8). The non-systematic Vandermonde generator V has rows indexed by the
// field elements 1..(k+m): V[i][j] = (i+1)^j, indices starting at 0.
// Right-multiplying V by the inverse of its first k rows yields the
// systematic generator G: the first k rows are the identity (data shards are
// preserved) and any k rows of G form an invertible k*k matrix.
package reedsolomon

import (
	"errors"
	"fmt"

	"reed-solomon-recovery/internal/gf256"
)

const (
	MinK = 2
	MaxK = 8
	MinM = 1
	MaxM = 4
	MaxN = 12
)

// Codec is a configured systematic Reed-Solomon codec.
type Codec struct {
	field *gf256.Field
	K, M  int
	// gen is the (k+m)*k systematic generator matrix.
	gen [][]byte
}

// New builds the systematic generator matrix for data shards k and parity
// shards m. Shard number i (0-based) corresponds to row i of G.
func New(k, m int) (*Codec, error) {
	if k < MinK || k > MaxK {
		return nil, fmt.Errorf("k must be in [%d,%d], got %d", MinK, MaxK, k)
	}
	if m < MinM || m > MaxM {
		return nil, fmt.Errorf("m must be in [%d,%d], got %d", MinM, MaxM, m)
	}
	f := gf256.New()
	n := k + m
	v := make([][]byte, n)
	for i := 0; i < n; i++ {
		v[i] = make([]byte, k)
		base := byte(i + 1)
		for j := 0; j < k; j++ {
			v[i][j] = f.Pow(base, j)
		}
	}
	inv, err := invert(f, v[:k])
	if err != nil {
		return nil, fmt.Errorf("vandermonde top block singular: %w", err)
	}
	gen := make([][]byte, n)
	for i := 0; i < n; i++ {
		gen[i] = mulMatrix(f, v[i], inv)
	}
	return &Codec{field: f, K: k, M: m, gen: gen}, nil
}

// Encode turns k data shards into k+m shards in place: the returned slice's
// first k entries alias data; parity shards are newly allocated. Every shard
// must (and does) have shardLen bytes.
func (c *Codec) Encode(data [][]byte, shardLen int) [][]byte {
	out := make([][]byte, c.K+c.M)
	copy(out, data)
	for i := c.K; i < c.K+c.M; i++ {
		p := make([]byte, shardLen)
		for j := 0; j < c.K; j++ {
			coef := c.gen[i][j]
			if coef == 0 {
				continue
			}
			dj := data[j]
			for off := 0; off < shardLen; off++ {
				p[off] ^= c.field.Mul(coef, dj[off])
			}
		}
		out[i] = p
	}
	return out
}

// Reconstruct recovers all k data shards from any k available shards.
// available maps shard number (0..k+m-1) to shard bytes. Exactly k distinct,
// valid shard numbers must be present and each shard must have shardLen bytes.
func (c *Codec) Reconstruct(available map[int][]byte, shardLen int) ([][]byte, error) {
	if len(available) != c.K {
		return nil, fmt.Errorf("need exactly %d distinct valid shards, got %d", c.K, len(available))
	}
	nums := make([]int, 0, c.K)
	for num := range available {
		if num < 0 || num >= c.K+c.M {
			return nil, fmt.Errorf("shard number %d out of range", num)
		}
		if len(available[num]) != shardLen {
			return nil, fmt.Errorf("shard %d length %d != shard length %d", num, len(available[num]), shardLen)
		}
		nums = append(nums, num)
	}
	// Deterministic order (the result is independent of it).
	for i := 1; i < len(nums); i++ {
		for j := i; j > 0 && nums[j-1] > nums[j]; j-- {
			nums[j-1], nums[j] = nums[j], nums[j-1]
		}
	}
	a := make([][]byte, c.K)
	for i, num := range nums {
		a[i] = c.gen[num]
	}
	inv, err := invert(c.field, a)
	if err != nil {
		return nil, fmt.Errorf("selected rows not invertible: %w", err)
	}
	data := make([][]byte, c.K)
	for j := 0; j < c.K; j++ {
		data[j] = make([]byte, shardLen)
	}
	for i, num := range nums {
		src := available[num]
		for j := 0; j < c.K; j++ {
			coef := inv[j][i]
			if coef == 0 {
				continue
			}
			for off := 0; off < shardLen; off++ {
				data[j][off] ^= c.field.Mul(coef, src[off])
			}
		}
	}
	return data, nil
}

// ShardSize reports the zero-padded shard length for an original size.
func ShardSize(origLen, k int) int {
	return (origLen + k - 1) / k
}

// Split divides data into k equal-length shards, zero-padding the tail.
func Split(data []byte, k int) ([][]byte, int) {
	shardLen := ShardSize(len(data), k)
	shards := make([][]byte, k)
	for i := 0; i < k; i++ {
		s := make([]byte, shardLen)
		off := i * shardLen
		if off < len(data) {
			copy(s, data[off:min(len(data), off+shardLen)])
		}
		shards[i] = s
	}
	return shards, shardLen
}

// Join concatenates k shards and trims the zero padding back to origLen.
func Join(shards [][]byte, k, origLen int) []byte {
	shardLen := ShardSize(origLen, k)
	out := make([]byte, 0, origLen)
	for i := 0; i < k; i++ {
		end := min(shardLen, origLen-len(out))
		if end <= 0 {
			break
		}
		out = append(out, shards[i][:end]...)
	}
	return out
}

// invert computes the inverse of an n*n matrix over GF(2^8) using Gauss-Jordan
// elimination on [A|I].
func invert(f *gf256.Field, a [][]byte) ([][]byte, error) {
	n := len(a)
	for _, row := range a {
		if len(row) != n {
			return nil, errors.New("matrix not square")
		}
	}
	m := make([][]byte, n)
	for i := 0; i < n; i++ {
		m[i] = make([]byte, 2*n)
		copy(m[i], a[i])
		m[i][n+i] = 1
	}
	for col := 0; col < n; col++ {
		pivot := -1
		for r := col; r < n; r++ {
			if m[r][col] != 0 {
				pivot = r
				break
			}
		}
		if pivot < 0 {
			return nil, errors.New("matrix is singular")
		}
		m[col], m[pivot] = m[pivot], m[col]
		pv := m[col][col]
		if pv != 1 {
			pinv := f.Inv(pv)
			for j := 0; j < 2*n; j++ {
				m[col][j] = f.Mul(m[col][j], pinv)
			}
		}
		for r := 0; r < n; r++ {
			if r == col || m[r][col] == 0 {
				continue
			}
			factor := m[r][col]
			for j := 0; j < 2*n; j++ {
				m[r][j] ^= f.Mul(factor, m[col][j])
			}
		}
	}
	inv := make([][]byte, n)
	for i := 0; i < n; i++ {
		inv[i] = make([]byte, n)
		copy(inv[i], m[i][n:])
	}
	return inv, nil
}

// mulMatrix multiplies a 1*k row vector by a k*k matrix.
func mulMatrix(f *gf256.Field, row []byte, mat [][]byte) []byte {
	k := len(row)
	out := make([]byte, k)
	for j := 0; j < k; j++ {
		var v byte
		for i := 0; i < k; i++ {
			v ^= f.Mul(row[i], mat[i][j])
		}
		out[j] = v
	}
	return out
}
