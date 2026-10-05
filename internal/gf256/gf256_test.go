package gf256

import "testing"

func TestMulAndInverse(t *testing.T) {
	inverse, ok := Inverse(0x53)
	if !ok {
		t.Fatal("expected inverse")
	}
	if got := Mul(0x53, inverse); got != 1 {
		t.Fatalf("Mul(0x53, %#x) = %#x, want 1", inverse, got)
		t.Fatal("multiplicative inverse check failed")
	}
	if _, ok := Inverse(0); ok {
		t.Fatal("zero has no inverse")
	}
}
