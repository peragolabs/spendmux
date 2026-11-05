package providers

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// SignAWSRequestV4 signs req in-place for Bedrock (service = bedrock) using SigV4.
// Body must match what will be sent; Content-Length should already be set if needed.
func SignAWSRequestV4(req *http.Request, creds AWSCreds, body []byte) error {
	region := creds.Region
	if region == "" {
		region = "us-east-1"
	}
	service := "bedrock"
	now := time.Now().UTC()
	amzDate := now.Format("20060102T150405Z")
	dateStamp := now.Format("20060102")

	payloadHash := sha256Hex(body)
	req.Header.Set("X-Amz-Date", amzDate)
	req.Header.Set("X-Amz-Content-Sha256", payloadHash)
	if creds.SessionToken != "" {
		req.Header.Set("X-Amz-Security-Token", creds.SessionToken)
	}
	if req.Header.Get("Host") == "" {
		req.Header.Set("Host", req.URL.Host)
	}

	canonicalHeaders, signedHeaders := canonicalHeadersAWS(req)
	canonicalRequest := strings.Join([]string{
		req.Method,
		req.URL.EscapedPath(),
		req.URL.RawQuery, // usually empty for chat/completions
		canonicalHeaders,
		signedHeaders,
		payloadHash,
	}, "\n")

	credentialScope := strings.Join([]string{dateStamp, region, service, "aws4_request"}, "/")
	stringToSign := strings.Join([]string{
		"AWS4-HMAC-SHA256",
		amzDate,
		credentialScope,
		sha256Hex([]byte(canonicalRequest)),
	}, "\n")

	signingKey := aws4SigningKey(creds.SecretAccessKey, dateStamp, region, service)
	signature := hex.EncodeToString(hmacSHA256(signingKey, stringToSign))

	auth := fmt.Sprintf(
		"AWS4-HMAC-SHA256 Credential=%s/%s, SignedHeaders=%s, Signature=%s",
		creds.AccessKeyID, credentialScope, signedHeaders, signature,
	)
	req.Header.Set("Authorization", auth)
	return nil
}

func canonicalHeadersAWS(req *http.Request) (canonical, signed string) {
	// Required signed headers for Bedrock OpenAI-compat POSTs.
	keys := []string{"content-type", "host", "x-amz-content-sha256", "x-amz-date"}
	if req.Header.Get("X-Amz-Security-Token") != "" {
		keys = append(keys, "x-amz-security-token")
	}
	var b strings.Builder
	var signedParts []string
	for _, k := range keys {
		v := req.Header.Get(http.CanonicalHeaderKey(k))
		if k == "host" && v == "" {
			v = req.URL.Host
		}
		if v == "" && k == "content-type" {
			v = "application/json"
			req.Header.Set("Content-Type", v)
		}
		b.WriteString(k)
		b.WriteByte(':')
		b.WriteString(strings.TrimSpace(v))
		b.WriteByte('\n')
		signedParts = append(signedParts, k)
	}
	return b.String(), strings.Join(signedParts, ";")
}

func aws4SigningKey(secret, dateStamp, region, service string) []byte {
	kDate := hmacSHA256([]byte("AWS4"+secret), dateStamp)
	kRegion := hmacSHA256(kDate, region)
	kService := hmacSHA256(kRegion, service)
	return hmacSHA256(kService, "aws4_request")
}

func hmacSHA256(key []byte, data string) []byte {
	m := hmac.New(sha256.New, key)
	_, _ = m.Write([]byte(data))
	return m.Sum(nil)
}

func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
