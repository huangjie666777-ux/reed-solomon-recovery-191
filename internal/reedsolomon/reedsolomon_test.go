package reedsolomon

import (
	"bytes"
	"testing"

	"reed-solomon-recovery/internal/gf256"
)

func TestVandermondeAndSystematicMatrix(t *testing.T) {
	v := Vandermonde(3, 2)
	if v[2][1] != 3 {
		t.Fatalf("V[2][1] = %d, want 3", v[2][1])
	}
	generator, err := SystemGenerator(3, 2)
	if err != nil {
		t.Fatal(err)
	}
	identity := [][]byte{{1, 0}, {0, 1}}
	for i := range identity {
		if !bytes.Equal(generator[i], identity[i]) {
			t.Fatalf("generator row %d = %v, want identity row %v", i, generator[i], identity[i])
		}
	}
}

func TestInvertIdentity(t *testing.T) {
	matrix := [][]byte{{3, 0}, {0, 5}}
	inverse, err := Invert(matrix)
	if err != nil {
		t.Fatal(err)
	}
	for i := range matrix {
		for j := range matrix {
			product := gf256.Mul(matrix[i][0], inverse[0][j]) ^ gf256.Mul(matrix[i][1], inverse[1][j])
			want := byte(0)
			if i == j {
				want = 1
			}
			if product != want {
				t.Fatalf("matrix product[%d][%d] = %d, want %d", i, j, product, want)
			}
		}
	}
}

func TestReconstructEverySubset(t *testing.T) {
	for k := 2; k <= 8; k++ {
		for m := 1; m <= 4; m++ {
			data := make([]byte, k*3+((k+m)%5))
			for i := range data {
				data[i] = byte(i*17 + k*7 + m)
			}
			shards, _, generator, err := Encode(data, k, m)
			if err != nil {
				t.Fatal(err)
			}
			for mask := 0; mask < 1<<(k+m); mask++ {
				if countBits(mask) != k {
					continue
				}
				available := make(map[int][]byte, k)
				for number := 0; number < k+m; number++ {
					if mask&(1<<number) != 0 {
						available[number] = shards[number]
					}
				}
				reconstructed, err := Reconstruct(generator, k, available)
				if err != nil {
					t.Fatalf("k=%d m=%d subset=%b: %v", k, m, mask, err)
				}
				if !bytes.Equal(reconstructed[:len(data)], data) {
					t.Fatalf("k=%d m=%d subset=%b data mismatch", k, m, mask)
				}
			}
		}
	}
}

func countBits(value int) int {
	count := 0
	for value != 0 {
		count += value & 1
		value >>= 1
	}
	return count
}
