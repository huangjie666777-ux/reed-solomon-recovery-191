package gf256

// Add returns addition in GF(2^8). Addition and subtraction are XOR.
func Add(a, b byte) byte {
	return a ^ b
}

// Mul returns multiplication in GF(2^8) using the AES-independent
// irreducible polynomial x^8+x^4+x^3+x^2+1 represented by 0x11d.
func Mul(a, b byte) byte {
	var product byte
	addend := uint16(a)
	for b != 0 {
		if b&1 != 0 {
			product ^= byte(addend)
		}
		b >>= 1
		shifted := addend << 1
		if shifted&0x100 != 0 {
			addend = (shifted ^ 0x11d) & 0xff
		} else {
			addend = shifted & 0xff
		}
	}
	return product
}

// Pow returns base^exponent. exponent zero is the multiplicative identity.
func Pow(base byte, exponent int) byte {
	result := byte(1)
	for i := 0; i < exponent; i++ {
		result = Mul(result, base)
	}
	return result
}

// Inverse returns the multiplicative inverse. Zero has no inverse.
func Inverse(a byte) (byte, bool) {
	if a == 0 {
		return 0, false
	}
	return Pow(a, 254), true
}
