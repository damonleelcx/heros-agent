package adminidentity

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"encoding/base64"
	"math/big"
	"testing"
)

// jwkec_test.go covers the EC branch of publicKeyFromJWK, and exists because the on-curve refusal had
// no test at all — it was rewritten to route through `crypto/ecdh` (the API the `IsOnCurve`
// deprecation names) and a green suite would have said nothing either way.
//
// Accepting an off-curve point is the invalid-curve attack: an attacker supplies a point on a weaker
// curve sharing the same field, and the arithmetic leaks the private scalar of whoever operates on
// it. So the refusal is verified directly rather than assumed from the fact that valid keys work.

func ecJWK(t *testing.T, crv string, x, y []byte) jwk {
	t.Helper()
	return jwk{
		Kty: "EC", Crv: crv,
		X: base64.RawURLEncoding.EncodeToString(x),
		Y: base64.RawURLEncoding.EncodeToString(y),
	}
}

func TestPublicKeyFromJWKAcceptsARealPoint(t *testing.T) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	got, err := publicKeyFromJWK(ecJWK(t, "P-256", key.X.Bytes(), key.Y.Bytes()))
	if err != nil {
		t.Fatalf("a point generated on P-256 was refused: %v", err)
	}
	pub, ok := got.(*ecdsa.PublicKey)
	if !ok {
		t.Fatalf("got %T, want *ecdsa.PublicKey", got)
	}
	if pub.X.Cmp(key.X) != 0 || pub.Y.Cmp(key.Y) != 0 {
		t.Fatal("the returned key is not the point that went in")
	}
}

// A coordinate SHORTER than the field size must be left-padded, not misaligned. This is the case a
// hand-built uncompressed encoding gets wrong, and it fails only for the ~1-in-256 keys whose
// coordinate happens to have a leading zero byte — so it is constructed deliberately rather than
// waited for.
func TestPublicKeyFromJWKLeftPadsAShortCoordinate(t *testing.T) {
	var key *ecdsa.PublicKey
	for range 4000 {
		k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if err != nil {
			t.Fatalf("generate: %v", err)
		}
		// Bytes() drops leading zeros, so this is a key whose X is naturally short on the wire.
		if len(k.X.Bytes()) < 32 {
			key = &k.PublicKey
			break
		}
	}
	if key == nil {
		t.Skip("no key with a short X coordinate was generated; nothing to assert")
	}
	if _, err := publicKeyFromJWK(ecJWK(t, "P-256", key.X.Bytes(), key.Y.Bytes())); err != nil {
		t.Fatalf("a valid point with a short X was refused, so the padding is misaligned: %v", err)
	}
}

func TestPublicKeyFromJWKRefusesAnOffCurvePoint(t *testing.T) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	// y+1 is not on the curve: the curve equation admits only ±y for a given x.
	offY := new(big.Int).Add(key.Y, big.NewInt(1))
	if _, err := publicKeyFromJWK(ecJWK(t, "P-256", key.X.Bytes(), offY.Bytes())); err == nil {
		t.Fatal("an off-curve point was accepted — this is the invalid-curve attack")
	}
}

func TestPublicKeyFromJWKRefusesAnOverWideCoordinate(t *testing.T) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	wide := append([]byte{0xff}, key.X.Bytes()...) // 33 bytes on a 32-byte curve
	if _, err := publicKeyFromJWK(ecJWK(t, "P-256", wide, key.Y.Bytes())); err == nil {
		t.Fatal("a coordinate wider than the curve was accepted")
	}
}

func TestPublicKeyFromJWKRefusesAnUnknownCurve(t *testing.T) {
	if _, err := publicKeyFromJWK(ecJWK(t, "P-224", []byte{1}, []byte{2})); err == nil {
		t.Fatal("an unsupported curve was accepted")
	}
}
