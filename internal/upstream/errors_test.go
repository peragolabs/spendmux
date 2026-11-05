package upstream

import "testing"

func TestHumanizeError(t *testing.T) {
	cases := []struct{ body, want string }{
		{`[{"error":{"code":503,"message":"This model is currently experiencing high demand.","status":"UNAVAILABLE"}}]`, "google: This model is currently experiencing high demand. (HTTP 503)"},
		{`{"error":{"message":"Invalid API key","type":"invalid_request_error"}}`, "google: Invalid API key (HTTP 503)"},
		{`{"error":"rate limited"}`, "google: rate limited (HTTP 503)"},
		{`{"detail":"Not found"}`, "google: Not found (HTTP 503)"},
		{`<html><body>Bad gateway</body></html>`, "google: Service Unavailable (HTTP 503)"},
		{``, "google: Service Unavailable (HTTP 503)"},
	}
	for _, c := range cases {
		if got := HumanizeError("google", 503, []byte(c.body)); got != c.want {
			t.Errorf("body %q: got %q want %q", c.body, got, c.want)
		}
	}
}
