package gf256

import "testing"

func TestFieldTables(t *testing.T) {
	f := New()
	// Generator 2 must have order 255, so every non-zero element appears once.
	seen := map[byte]int{}
	for i := 0; i < 255; i++ {
		seen[f.exp[i]]++
	}
	if len(seen) != 255 {
		t.Fatalf("exp table covers %d distinct non-zero elements, want 255", len(seen))
	}
	for v, count := range seen {
		if v != 0 && count != 1 {
			t.Fatalf("element %d appears %d times", v, count)
		}
	}
}

func TestMulDivInv(t *testing.T) {
	f := New()
	// 2^8 reduced by 0x11d: 0x100 ^ 0x11d = 0x1d.
	if got := f.Mul(2, 0x80); got != 0x1d {
		t.Fatalf("2 * 0x80 = %#02x, want 0x1d", got)
	}
	for a := 1; a < 256; a++ {
		a := byte(a)
		if got := f.Mul(a, f.Inv(a)); got != 1 {
			t.Fatalf("%d * inv(%d) = %d, want 1", a, a, got)
		}
		for b := 1; b < 256; b++ {
			b := byte(b)
			p := f.Mul(a, b)
			if f.Div(p, b) != a {
				t.Fatalf("div inconsistent for %d,%d", a, b)
			}
		}
	}
	if f.Mul(0, 57) != 0 || f.Div(0, 57) != 0 {
		t.Fatal("zero arithmetic wrong")
	}
}

func TestPow(t *testing.T) {
	f := New()
	if f.Pow(5, 0) != 1 {
		t.Fatal("a^0 must be 1")
	}
	if f.Pow(3, 255) != 1 {
		t.Fatal("a^255 must be 1")
	}
	acc := byte(1)
	for n := 1; n <= 300; n++ {
		acc = f.Mul(acc, 3)
		if f.Pow(3, n) != acc {
			t.Fatalf("3^%d mismatch", n)
		}
	}
}
