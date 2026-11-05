package providers_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/vrannang1/spendmux/internal/providers"
)

func TestSignAWSRequestV4SetsAuth(t *testing.T) {
	t.Parallel()
	body := []byte(`{"model":"x","messages":[]}`)
	req, err := http.NewRequest("POST", "https://bedrock-runtime.us-east-1.amazonaws.com/openai/v1/chat/completions", strings.NewReader(string(body)))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	creds := providers.AWSCreds{
		AccessKeyID:     "AKIAIOSFODNN7EXAMPLE",
		SecretAccessKey: "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY",
		Region:          "us-east-1",
	}
	if err := providers.SignAWSRequestV4(req, creds, body); err != nil {
		t.Fatal(err)
	}
	auth := req.Header.Get("Authorization")
	if !strings.HasPrefix(auth, "AWS4-HMAC-SHA256 Credential=") {
		t.Fatalf("auth: %s", auth)
	}
	if !strings.Contains(auth, "AKIAIOSFODNN7EXAMPLE/") {
		t.Fatalf("missing access key in auth: %s", auth)
	}
	if req.Header.Get("X-Amz-Date") == "" || req.Header.Get("X-Amz-Content-Sha256") == "" {
		t.Fatal("missing signing headers")
	}
	creds.SessionToken = "token"
	req2, _ := http.NewRequest("POST", req.URL.String(), strings.NewReader(string(body)))
	req2.Header.Set("Content-Type", "application/json")
	_ = providers.SignAWSRequestV4(req2, creds, body)
	if req2.Header.Get("X-Amz-Security-Token") != "token" {
		t.Fatal("expected session token header")
	}
}
