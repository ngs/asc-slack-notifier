package webhook

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"strings"
)

// SignatureHeader is the header App Store Connect signs the raw body with.
const SignatureHeader = "x-apple-signature"

// signaturePrefix precedes the hex digest in the signature header value.
const signaturePrefix = "hmacsha256="

var (
	// ErrMissingSignature is returned when the signature header is absent.
	ErrMissingSignature = errors.New("missing x-apple-signature header")
	// ErrMalformedSignature is returned when the header value does not carry a
	// hex-encoded hmacsha256 digest.
	ErrMalformedSignature = errors.New("malformed x-apple-signature header")
	// ErrSignatureMismatch is returned when the digest does not match the body.
	ErrSignatureMismatch = errors.New("signature mismatch")
)

// ComputeSignature returns the hex-encoded HMAC-SHA256 digest of body under
// secret, matching the digest App Store Connect sends.
func ComputeSignature(secret string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}

// VerifySignature recomputes the HMAC-SHA256 of body and compares it against
// the header value in constant time.
func VerifySignature(secret string, body []byte, header string) error {
	header = strings.TrimSpace(header)
	if header == "" {
		return ErrMissingSignature
	}
	if len(header) <= len(signaturePrefix) || !strings.EqualFold(header[:len(signaturePrefix)], signaturePrefix) {
		return ErrMalformedSignature
	}
	got, err := hex.DecodeString(strings.TrimSpace(header[len(signaturePrefix):]))
	if err != nil {
		return ErrMalformedSignature
	}
	want, err := hex.DecodeString(ComputeSignature(secret, body))
	if err != nil {
		return ErrMalformedSignature
	}
	if !hmac.Equal(got, want) {
		return ErrSignatureMismatch
	}
	return nil
}

// VerifyRequestSignature verifies the raw body against the signature header of
// the request. Header lookup is case-insensitive via http.Header.Get.
func VerifyRequestSignature(secret string, body []byte, h http.Header) error {
	return VerifySignature(secret, body, h.Get(SignatureHeader))
}
