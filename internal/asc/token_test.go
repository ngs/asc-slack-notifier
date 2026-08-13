package asc

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"math/big"
	"strings"
	"testing"
	"time"
)

// testKeyPEM generates a P-256 key and returns it in PKCS#8 PEM form, the
// encoding Apple ships as a .p8 file.
func testKeyPEM(t *testing.T) ([]byte, *ecdsa.PrivateKey) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatalf("MarshalPKCS8PrivateKey: %v", err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}), key
}

func TestTokenClaimsAndSignature(t *testing.T) {
	keyPEM, key := testKeyPEM(t)
	ts, err := newTokenSource("KEYID123", "issuer-uuid", keyPEM)
	if err != nil {
		t.Fatalf("newTokenSource: %v", err)
	}

	now := time.Date(2025, 4, 16, 5, 0, 0, 0, time.UTC)
	token, err := ts.Token(now)
	if err != nil {
		t.Fatalf("Token: %v", err)
	}

	segs := strings.Split(token, ".")
	if len(segs) != 3 {
		t.Fatalf("token has %d segments, want 3: %q", len(segs), token)
	}

	var header struct{ Alg, Kid, Typ string }
	decodeSegment(t, segs[0], &header)
	if header.Alg != "ES256" || header.Kid != "KEYID123" || header.Typ != "JWT" {
		t.Errorf("header = %+v", header)
	}

	var claims struct {
		Iss string `json:"iss"`
		Aud string `json:"aud"`
		Iat int64  `json:"iat"`
		Exp int64  `json:"exp"`
	}
	decodeSegment(t, segs[1], &claims)
	if claims.Iss != "issuer-uuid" {
		t.Errorf("iss = %q, want issuer-uuid", claims.Iss)
	}
	if claims.Aud != tokenAudience {
		t.Errorf("aud = %q, want %q", claims.Aud, tokenAudience)
	}
	if claims.Iat != now.Unix() {
		t.Errorf("iat = %d, want %d", claims.Iat, now.Unix())
	}
	if got := claims.Exp - claims.Iat; got != int64(tokenLifetime.Seconds()) {
		t.Errorf("exp-iat = %ds, want %ds", got, int64(tokenLifetime.Seconds()))
	}

	sig, err := base64.RawURLEncoding.DecodeString(segs[2])
	if err != nil {
		t.Fatalf("decode signature: %v", err)
	}
	if len(sig) != 64 {
		t.Fatalf("signature is %d bytes, want the raw 64-byte R||S form", len(sig))
	}
	digest := sha256.Sum256([]byte(segs[0] + "." + segs[1]))
	r := new(big.Int).SetBytes(sig[:32])
	s := new(big.Int).SetBytes(sig[32:])
	if !ecdsa.Verify(&key.PublicKey, digest[:], r, s) {
		t.Error("signature does not verify against the signing key")
	}
}

func TestTokenCaching(t *testing.T) {
	keyPEM, _ := testKeyPEM(t)
	ts, err := newTokenSource("KEYID123", "issuer-uuid", keyPEM)
	if err != nil {
		t.Fatalf("newTokenSource: %v", err)
	}

	now := time.Date(2025, 4, 16, 5, 0, 0, 0, time.UTC)
	first, err := ts.Token(now)
	if err != nil {
		t.Fatalf("Token: %v", err)
	}

	cached, err := ts.Token(now.Add(time.Minute))
	if err != nil {
		t.Fatalf("Token: %v", err)
	}
	if cached != first {
		t.Error("token was reminted while the cached one was still fresh")
	}

	// Less than tokenRefreshWindow of validity left: a new token is minted.
	renewed, err := ts.Token(now.Add(tokenLifetime - 30*time.Second))
	if err != nil {
		t.Fatalf("Token: %v", err)
	}
	if renewed == first {
		t.Error("token was reused past the refresh window")
	}
}

func TestNewTokenSourceRejectsBadKeys(t *testing.T) {
	keyPEM, _ := testKeyPEM(t)

	rsaKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("rsa.GenerateKey: %v", err)
	}
	rsaDER, err := x509.MarshalPKCS8PrivateKey(rsaKey)
	if err != nil {
		t.Fatalf("MarshalPKCS8PrivateKey: %v", err)
	}
	rsaPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: rsaDER})

	tests := []struct {
		name     string
		keyID    string
		issuerID string
		pem      []byte
	}{
		{name: "missing key ID", issuerID: "issuer", pem: keyPEM},
		{name: "missing issuer ID", keyID: "KEYID", pem: keyPEM},
		{name: "not PEM", keyID: "KEYID", issuerID: "issuer", pem: []byte("not a key")},
		{name: "RSA key", keyID: "KEYID", issuerID: "issuer", pem: rsaPEM},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := newTokenSource(tt.keyID, tt.issuerID, tt.pem); err == nil {
				t.Fatal("error = nil, want an error")
			}
		})
	}
}

func TestParsePrivateKeyAcceptsSEC1(t *testing.T) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	der, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatalf("MarshalECPrivateKey: %v", err)
	}
	sec1 := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: der})

	parsed, err := parsePrivateKey(sec1)
	if err != nil {
		t.Fatalf("parsePrivateKey: %v", err)
	}
	if !parsed.Equal(key) {
		t.Error("parsed key differs from the generated one")
	}
}

func decodeSegment(t *testing.T, seg string, v any) {
	t.Helper()
	raw, err := base64.RawURLEncoding.DecodeString(seg)
	if err != nil {
		t.Fatalf("decode segment %q: %v", seg, err)
	}
	if err := json.Unmarshal(raw, v); err != nil {
		t.Fatalf("unmarshal segment %q: %v", raw, err)
	}
}
