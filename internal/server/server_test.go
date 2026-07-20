package server

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAuthorized(t *testing.T) {
	t.Parallel()
	request := httptest.NewRequest(http.MethodPost, "/run", nil)
	if authorized(request, "secret") {
		t.Fatal("missing token should not authorize")
	}
	request.Header.Set("Authorization", "Bearer secret")
	if !authorized(request, "secret") {
		t.Fatal("valid token should authorize")
	}
	if !authorized(request, "") {
		t.Fatal("empty configured token should allow requests")
	}
}

func TestValidateBinding(t *testing.T) {
	t.Parallel()
	if err := validateBinding("127.0.0.1:8080", ""); err != nil {
		t.Fatal(err)
	}
	if err := validateBinding("0.0.0.0:8080", ""); err == nil {
		t.Fatal("expected public binding without a token to fail")
	}
	if err := validateBinding("0.0.0.0:8080", "secret"); err != nil {
		t.Fatal(err)
	}
}
