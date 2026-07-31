package adminidentity

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
)

// jws.go verifies a compact JWS against a JWKS, and does nothing else (P22 task 6.1).
//
// # Why this is written rather than imported
//
// `go.mod` has ten direct dependencies and every one of them is load-bearing. A JWT library on the
// OPERATOR authentication path is code inside the highest-blast-radius boundary in the platform, and
// the part that is actually hard — RSA, ECDSA, SHA-2 — is already in the standard library. What is
// left is a header parse, an algorithm allowlist and a key lookup.
//
// # The three ways a hand-written verifier is broken, and where each is closed
//
//  1. `alg: "none"`, or an algorithm the TOKEN chooses. `jwsAlgorithms` is an allowlist and `none` is
//     absent from it, so a token cannot name its way past verification.
//  2. HMAC confusion — an attacker signs with the RSA public key as an HMAC secret, and a verifier
//     that dispatches on `alg` accepts it. No `HS*` algorithm exists here at all, so the confusion has
//     nowhere to land.
//  3. Trusting `kid` to select ANY key. The key set comes from the issuer's own discovery document;
//     `kid` only chooses among those. A set with several keys and no `kid` match is a refusal, not a
//     loop that tries them all.
//
// The mirror of this file is `web/console/src/lib/idp/jwt.ts`, which makes the same three decisions
// for the customer seam. They are separate because they are in separate processes in separate
// languages — but the ALLOWLIST is the thing to keep identical, and both are asserted by their own
// tests against the same three failures.

// jwsSpec describes how one JWS algorithm is verified.
type jwsSpec struct {
	hash crypto.Hash
	kty  string
	// pss selects RSA-PSS rather than PKCS#1 v1.5.
	pss bool
	// ecBytes is the fixed length of each of R and S in a JWS ECDSA signature.
	ecBytes int
}

// jwsAlgorithms is the allowlist. Asymmetric only, so an HMAC algorithm has no landing site.
var jwsAlgorithms = map[string]jwsSpec{
	"RS256": {hash: crypto.SHA256, kty: "RSA"},
	"RS384": {hash: crypto.SHA384, kty: "RSA"},
	"RS512": {hash: crypto.SHA512, kty: "RSA"},
	"PS256": {hash: crypto.SHA256, kty: "RSA", pss: true},
	"PS384": {hash: crypto.SHA384, kty: "RSA", pss: true},
	"PS512": {hash: crypto.SHA512, kty: "RSA", pss: true},
	"ES256": {hash: crypto.SHA256, kty: "EC", ecBytes: 32},
	"ES384": {hash: crypto.SHA384, kty: "EC", ecBytes: 48},
	"ES512": {hash: crypto.SHA512, kty: "EC", ecBytes: 66},
}

// jwk is the subset of a JWKS entry this package reads.
type jwk struct {
	Kty string `json:"kty"`
	Kid string `json:"kid"`
	Use string `json:"use"`
	Alg string `json:"alg"`
	N   string `json:"n"`
	E   string `json:"e"`
	Crv string `json:"crv"`
	X   string `json:"x"`
	Y   string `json:"y"`
}

type jwkSet struct {
	Keys []jwk `json:"keys"`
}

// ErrJWSInvalid is every signature-layer failure. One error, so probing cannot tell a bad signature
// from an unknown key from an unsupported algorithm.
var ErrJWSInvalid = errors.New("adminidentity: the token did not verify")

// verifyCompactJWS checks `header.payload.signature` against a key set and returns the raw payload.
//
// It does NOT interpret the payload. `iss`, `aud`, `nonce` and the time bounds belong to the caller's
// federation rules, and keeping them out of here is what stops an "I'll just check the audience here
// too" from quietly becoming the only audience check, in a file the caller's tests do not read.
func verifyCompactJWS(token string, keys []jwk) ([]byte, error) {
	headerB64, rest, ok := cut(token, '.')
	if !ok {
		return nil, ErrJWSInvalid
	}
	payloadB64, signatureB64, ok := cut(rest, '.')
	if !ok {
		return nil, ErrJWSInvalid
	}

	headerBytes, err := base64.RawURLEncoding.DecodeString(headerB64)
	if err != nil {
		return nil, ErrJWSInvalid
	}
	var header struct {
		Alg string `json:"alg"`
		Kid string `json:"kid"`
	}
	if err := json.Unmarshal(headerBytes, &header); err != nil {
		return nil, ErrJWSInvalid
	}
	spec, allowed := jwsAlgorithms[header.Alg]
	if !allowed {
		// `alg: none` and every HS* land here, because they are simply absent from the map.
		return nil, fmt.Errorf("%w: unsupported algorithm", ErrJWSInvalid)
	}

	signature, err := base64.RawURLEncoding.DecodeString(signatureB64)
	if err != nil {
		return nil, ErrJWSInvalid
	}
	signed := []byte(headerB64 + "." + payloadB64)

	candidates := make([]jwk, 0, len(keys))
	for _, k := range keys {
		if k.Kty != spec.kty {
			continue
		}
		if k.Use != "" && k.Use != "sig" {
			continue
		}
		candidates = append(candidates, k)
	}
	usable := candidates
	if header.Kid != "" {
		matched := make([]jwk, 0, 1)
		for _, k := range candidates {
			if k.Kid == header.Kid {
				matched = append(matched, k)
			}
		}
		switch {
		case len(matched) > 0:
			usable = matched
		case len(candidates) == 1:
			// An IdP that rotated without republishing `kid`. Usable, because there is exactly one key
			// it could be — trying SEVERAL on a `kid` miss would turn a key set into an oracle.
			usable = candidates
		default:
			return nil, fmt.Errorf("%w: no key matches", ErrJWSInvalid)
		}
	}
	if len(usable) == 0 {
		return nil, fmt.Errorf("%w: no key matches", ErrJWSInvalid)
	}

	digest := sum(spec.hash, signed)
	for _, k := range usable {
		pub, err := publicKeyFromJWK(k)
		if err != nil {
			continue // a malformed key in the set is skipped, never a reason to accept the token
		}
		if verifySignature(pub, spec, digest, signature) {
			payload, err := base64.RawURLEncoding.DecodeString(payloadB64)
			if err != nil {
				return nil, ErrJWSInvalid
			}
			return payload, nil
		}
	}
	return nil, fmt.Errorf("%w: signature", ErrJWSInvalid)
}

func cut(s string, sep byte) (string, string, bool) {
	for i := 0; i < len(s); i++ {
		if s[i] == sep {
			return s[:i], s[i+1:], true
		}
	}
	return s, "", false
}

func sum(h crypto.Hash, data []byte) []byte {
	switch h {
	case crypto.SHA256:
		d := sha256.Sum256(data)
		return d[:]
	case crypto.SHA384:
		d := sha512.Sum384(data)
		return d[:]
	default:
		d := sha512.Sum512(data)
		return d[:]
	}
}

func verifySignature(pub any, spec jwsSpec, digest, signature []byte) bool {
	switch key := pub.(type) {
	case *rsa.PublicKey:
		if spec.pss {
			return rsa.VerifyPSS(key, spec.hash, digest, signature, &rsa.PSSOptions{SaltLength: rsa.PSSSaltLengthEqualsHash}) == nil
		}
		return rsa.VerifyPKCS1v15(key, spec.hash, digest, signature) == nil
	case *ecdsa.PublicKey:
		// JWS ECDSA signatures are the raw R‖S pair, NOT the DER structure `ecdsa.VerifyASN1` expects.
		// Getting this wrong makes every valid ECDSA token fail in a way that looks exactly like a
		// wrong key, which is a long afternoon.
		if len(signature) != spec.ecBytes*2 {
			return false
		}
		r := new(big.Int).SetBytes(signature[:spec.ecBytes])
		s := new(big.Int).SetBytes(signature[spec.ecBytes:])
		return ecdsa.Verify(key, digest, r, s)
	default:
		return false
	}
}

func publicKeyFromJWK(k jwk) (any, error) {
	switch k.Kty {
	case "RSA":
		nBytes, err := base64.RawURLEncoding.DecodeString(k.N)
		if err != nil {
			return nil, err
		}
		eBytes, err := base64.RawURLEncoding.DecodeString(k.E)
		if err != nil {
			return nil, err
		}
		if len(eBytes) == 0 || len(eBytes) > 4 {
			return nil, errors.New("adminidentity: RSA exponent out of range")
		}
		padded := make([]byte, 4)
		copy(padded[4-len(eBytes):], eBytes)
		return &rsa.PublicKey{N: new(big.Int).SetBytes(nBytes), E: int(binary.BigEndian.Uint32(padded))}, nil
	case "EC":
		curve, ok := jwkCurves[k.Crv]
		if !ok {
			return nil, errors.New("adminidentity: unsupported EC curve")
		}
		xBytes, err := base64.RawURLEncoding.DecodeString(k.X)
		if err != nil {
			return nil, err
		}
		yBytes, err := base64.RawURLEncoding.DecodeString(k.Y)
		if err != nil {
			return nil, err
		}
		// Validate the point through `ecdh`, then verify with `ecdsa`. See jwkECDHCurves for why the
		// on-curve check does not go through `elliptic.Curve.IsOnCurve`.
		//
		// RFC 7518 §6.2.1.2 requires X and Y to be the curve's full field size, so the uncompressed
		// SEC 1 encoding is built at exactly that width. A SHORTER coordinate is left-padded rather
		// than refused — a leading zero byte dropped by a producer is common and harmless — but a
		// LONGER one is refused, because it is not the number it claims to be at this width.
		ecdhCurve, ok := jwkECDHCurves[k.Crv]
		if !ok {
			return nil, errors.New("adminidentity: unsupported EC curve")
		}
		size := (curve.Params().BitSize + 7) / 8
		if len(xBytes) > size || len(yBytes) > size {
			return nil, errors.New("adminidentity: EC coordinate is wider than the curve")
		}
		point := make([]byte, 1+2*size)
		point[0] = 4 // uncompressed
		copy(point[1+size-len(xBytes):1+size], xBytes)
		copy(point[1+2*size-len(yBytes):], yBytes)
		if _, err := ecdhCurve.NewPublicKey(point); err != nil {
			// A point off the curve is an invalid-curve attack, not a typo.
			return nil, errors.New("adminidentity: EC point is not on the curve")
		}
		return &ecdsa.PublicKey{Curve: curve, X: new(big.Int).SetBytes(xBytes), Y: new(big.Int).SetBytes(yBytes)}, nil
	default:
		return nil, errors.New("adminidentity: unsupported key type")
	}
}
