package asc

import (
	"crypto/ecdsa"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"sync"
	"time"
)

// tokenLifetime is how long a minted JWT stays valid. App Store Connect rejects
// tokens with a lifetime longer than 20 minutes.
const tokenLifetime = 10 * time.Minute

// tokenRefreshWindow is the slack kept before expiry: a cached token with less
// than this much validity left is replaced.
const tokenRefreshWindow = time.Minute

// tokenAudience is the fixed audience claim required by App Store Connect.
const tokenAudience = "appstoreconnect-v1"

// tokenSource mints and caches the ES256 JWTs used to authenticate against the
// App Store Connect API.
type tokenSource struct {
	keyID    string
	issuerID string
	key      *ecdsa.PrivateKey

	mu      sync.Mutex
	token   string
	expires time.Time
}

// newTokenSource parses an ECDSA private key in PEM form and returns a source
// minting tokens for the given key and issuer.
func newTokenSource(keyID, issuerID string, privateKeyPEM []byte) (*tokenSource, error) {
	if keyID == "" {
		return nil, fmt.Errorf("asc: key ID is required")
	}
	if issuerID == "" {
		return nil, fmt.Errorf("asc: issuer ID is required")
	}
	key, err := parsePrivateKey(privateKeyPEM)
	if err != nil {
		return nil, err
	}
	return &tokenSource{keyID: keyID, issuerID: issuerID, key: key}, nil
}

// parsePrivateKey accepts both the PKCS#8 encoding Apple ships as a .p8 file
// and the SEC1 encoding produced by some tooling.
func parsePrivateKey(pemBytes []byte) (*ecdsa.PrivateKey, error) {
	block, _ := pem.Decode(pemBytes)
	if block == nil {
		return nil, fmt.Errorf("asc: private key is not valid PEM")
	}
	if key, err := x509.ParseECPrivateKey(block.Bytes); err == nil {
		return key, nil
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("asc: parse private key: %w", err)
	}
	key, ok := parsed.(*ecdsa.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("asc: private key is %T, want an ECDSA key", parsed)
	}
	return key, nil
}

// Token returns a JWT valid at now, reusing the cached one while it has more
// than tokenRefreshWindow of validity left.
func (t *tokenSource) Token(now time.Time) (string, error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.token != "" && now.Add(tokenRefreshWindow).Before(t.expires) {
		return t.token, nil
	}

	expires := now.Add(tokenLifetime)
	token, err := t.sign(now, expires)
	if err != nil {
		return "", err
	}
	t.token = token
	t.expires = expires
	return token, nil
}

// sign builds and signs the JWT. The signature is the raw R||S concatenation
// required by ES256, not the ASN.1 DER encoding.
func (t *tokenSource) sign(issuedAt, expires time.Time) (string, error) {
	header := map[string]string{"alg": "ES256", "kid": t.keyID, "typ": "JWT"}
	claims := map[string]any{
		"iss": t.issuerID,
		"iat": issuedAt.Unix(),
		"exp": expires.Unix(),
		"aud": tokenAudience,
	}

	headerSeg, err := encodeSegment(header)
	if err != nil {
		return "", fmt.Errorf("asc: encode JWT header: %w", err)
	}
	claimsSeg, err := encodeSegment(claims)
	if err != nil {
		return "", fmt.Errorf("asc: encode JWT claims: %w", err)
	}

	signingInput := headerSeg + "." + claimsSeg
	digest := sha256.Sum256([]byte(signingInput))
	r, s, err := ecdsa.Sign(rand.Reader, t.key, digest[:])
	if err != nil {
		return "", fmt.Errorf("asc: sign JWT: %w", err)
	}

	keyBytes := (t.key.Curve.Params().BitSize + 7) / 8
	sig := make([]byte, 2*keyBytes)
	r.FillBytes(sig[:keyBytes])
	s.FillBytes(sig[keyBytes:])

	return signingInput + "." + base64.RawURLEncoding.EncodeToString(sig), nil
}

func encodeSegment(v any) (string, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
