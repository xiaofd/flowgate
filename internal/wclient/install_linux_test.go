//go:build linux

package wclient

import (
	"bytes"
	"testing"
)

func TestGetAptCodename(t *testing.T) {
	codename := getAptCodename()
	if codename == "" {
		t.Fatal("expected non-empty apt codename")
	}
	t.Logf("detected codename: %s", codename)
}

func TestDearmorOpenPGP(t *testing.T) {
	sampleArmor := `-----BEGIN PGP PUBLIC KEY BLOCK-----
Version: GnuPG v2

aGVsbG8gd29ybGQgdGVzdCBvcGVucGdwIGtleXJpbmcgY29udGVudCAxMjM0NTY3ODkw
AQAB
=abcd
-----END PGP PUBLIC KEY BLOCK-----`

	decoded, err := dearmorOpenPGP([]byte(sampleArmor))
	if err != nil {
		t.Fatalf("dearmorOpenPGP failed: %v", err)
	}
	if len(decoded) == 0 {
		t.Fatal("expected decoded binary to have non-zero length")
	}

	raw := []byte("plain-binary-key")
	rawDecoded, err := dearmorOpenPGP(raw)
	if err != nil {
		t.Fatalf("unexpected error for raw bytes: %v", err)
	}
	if !bytes.Equal(rawDecoded, raw) {
		t.Fatal("expected raw bytes unchanged")
	}
}
