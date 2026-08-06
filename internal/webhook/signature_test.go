package webhook

import (
	"errors"
	"net/http"
	"testing"
)

// Apple's documented test vector for the webhook signature.
const (
	docSecret    = "This is my secret"
	docBody      = "Hello, World!"
	docSignature = "7f062172b01cb00b53ca068614674a3d982a34062a0f5d37687d5e3377e54657"
)

func TestComputeSignatureMatchesAppleVector(t *testing.T) {
	if got := ComputeSignature(docSecret, []byte(docBody)); got != docSignature {
		t.Fatalf("ComputeSignature = %q, want %q", got, docSignature)
	}
}

func TestVerifySignature(t *testing.T) {
	tests := []struct {
		name   string
		secret string
		body   string
		header string
		want   error
	}{
		{
			name:   "valid documented vector",
			secret: docSecret,
			body:   docBody,
			header: "hmacsha256=" + docSignature,
			want:   nil,
		},
		{
			name:   "uppercase hex digest",
			secret: docSecret,
			body:   docBody,
			header: "hmacsha256=7F062172B01CB00B53CA068614674A3D982A34062A0F5D37687D5E3377E54657",
			want:   nil,
		},
		{
			name:   "missing header",
			secret: docSecret,
			body:   docBody,
			header: "",
			want:   ErrMissingSignature,
		},
		{
			name:   "bad prefix",
			secret: docSecret,
			body:   docBody,
			header: "sha256=" + docSignature,
			want:   ErrMalformedSignature,
		},
		{
			name:   "prefix without digest",
			secret: docSecret,
			body:   docBody,
			header: "hmacsha256=",
			want:   ErrMalformedSignature,
		},
		{
			name:   "non hex digest",
			secret: docSecret,
			body:   docBody,
			header: "hmacsha256=zzzz",
			want:   ErrMalformedSignature,
		},
		{
			name:   "digest mismatch",
			secret: docSecret,
			body:   "Hello, World!!",
			header: "hmacsha256=" + docSignature,
			want:   ErrSignatureMismatch,
		},
		{
			name:   "wrong secret",
			secret: "another secret",
			body:   docBody,
			header: "hmacsha256=" + docSignature,
			want:   ErrSignatureMismatch,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := VerifySignature(tt.secret, []byte(tt.body), tt.header)
			if !errors.Is(err, tt.want) {
				t.Fatalf("VerifySignature error = %v, want %v", err, tt.want)
			}
		})
	}
}

func TestVerifyRequestSignatureIsHeaderCaseInsensitive(t *testing.T) {
	h := http.Header{}
	h.Set("X-Apple-Signature", "hmacsha256="+docSignature)

	if err := VerifyRequestSignature(docSecret, []byte(docBody), h); err != nil {
		t.Fatalf("VerifyRequestSignature: %v", err)
	}
}
