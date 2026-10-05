// Package gf256 implements arithmetic over GF(2^8) with the primitive
// polynomial x^8 + x^4 + x^3 + x^2 + 1 (0x11d).
package gf256

// Field is GF(2^8) defined by polynomial 0x11d.
type Field struct {
	exp [512]byte
	log [256]byte
}

// New returns a field. Generator 2 is primitive for 0x11d, so the log and
// exp tables cover all 255 non-zero elements.
func New() *Field {
	var f Field
	var x uint16 = 1
	for i := 0; i < 255; i++ {
		f.exp[i] = byte(x)
		f.exp[i+255] = byte(x)
		f.log[x] = byte(i)
		x <<= 1
		if x&0x100 != 0 {
			x ^= 0x11d
		}
	}
	return &f
}

// Add is XOR (characteristic 2).
func (f *Field) Add(a, b byte) byte { return a ^ b }

// Mul multiplies two elements.
func (f *Field) Mul(a, b byte) byte {
	if a == 0 || b == 0 {
		return 0
	}
	return f.exp[int(f.log[a])+int(f.log[b])]
}

// Inv returns the multiplicative inverse; zero has no inverse.
func (f *Field) Inv(a byte) byte {
	if a == 0 {
		panic("gf256: inverse of zero")
	}
	return f.exp[255-int(f.log[a])]
}

// Div divides a by b.
func (f *Field) Div(a, b byte) byte {
	if b == 0 {
		panic("gf256: divide by zero")
	}
	if a == 0 {
		return 0
	}
	return f.exp[int(f.log[a])-int(f.log[b])+255]
}

// Pow returns a^n (n may be zero; 0^0 is 1 by field convention).
func (f *Field) Pow(a byte, n int) byte {
	if n == 0 {
		return 1
	}
	if a == 0 {
		return 0
	}
	return f.exp[(int(f.log[a])*n)%255]
}
