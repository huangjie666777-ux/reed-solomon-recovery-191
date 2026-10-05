package reedsolomon

import (
	"bytes"
	"math/rand"
	"testing"
)

func TestSystematicMatrix(t *testing.T) {
	c, err := New(4, 2)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 4; i++ {
		for j := 0; j < 4; j++ {
			want := byte(0)
			if i == j {
				want = 1
			}
			if c.gen[i][j] != want {
				t.Fatalf("top %d rows are not identity at [%d][%d]: got %d", 4, i, j, c.gen[i][j])
			}
		}
	}
}

func TestReconstructAllSubsets(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	for k := MinK; k <= MaxK; k++ {
		for m := MinM; m <= MaxM; m++ {
			codec, err := New(k, m)
			if err != nil {
				t.Fatalf("New(%d,%d): %v", k, m, err)
			}
			for _, origLen := range []int{1, k, k + 1, 1000} {
				data := make([]byte, origLen)
				rng.Read(data)
				shards, shardLen := Split(data, k)
				encoded := codec.Encode(shards, shardLen)
				n := k + m
				// Exhaustively choose every k-of-n combination.
				for mask := 0; mask < 1<<n; mask++ {
					if popcount(mask) != k {
						continue
					}
					avail := map[int][]byte{}
					for i := 0; i < n; i++ {
						if mask&(1<<i) != 0 {
							avail[i] = append([]byte(nil), encoded[i]...)
						}
					}
					rec, err := codec.Reconstruct(avail, shardLen)
					if err != nil {
						t.Fatalf("k=%d m=%d len=%d mask=%0*b: %v", k, m, origLen, n, mask, err)
					}
					got := Join(rec, k, origLen)
					if !bytes.Equal(got, data) {
						t.Fatalf("k=%d m=%d len=%d mask=%0*b: recovered bytes differ", k, m, origLen, n, mask)
					}
				}
			}
		}
	}
}

func TestReconstructRejectsWrongCount(t *testing.T) {
	codec, _ := New(3, 2)
	data := []byte("hello world!!")
	shards, shardLen := Split(data, 3)
	encoded := codec.Encode(shards, shardLen)
	if _, err := codec.Reconstruct(map[int][]byte{0: encoded[0], 1: encoded[1]}, shardLen); err == nil {
		t.Fatal("expected error with fewer than k shards")
	}
	if _, err := codec.Reconstruct(map[int][]byte{0: encoded[0]}, shardLen-1); err == nil {
		t.Fatal("expected error with wrong shard length")
	}
	if _, err := codec.Reconstruct(map[int][]byte{99: encoded[0], 1: encoded[1], 2: encoded[2]}, shardLen); err == nil {
		t.Fatal("expected error for out-of-range shard number")
	}
}

func TestNewValidation(t *testing.T) {
	if _, err := New(1, 2); err == nil {
		t.Fatal("k=1 must be rejected")
	}
	if _, err := New(8, 5); err == nil {
		t.Fatal("m=5 must be rejected")
	}
}

func popcount(x int) int {
	c := 0
	for x != 0 {
		x &= x - 1
		c++
	}
	return c
}
