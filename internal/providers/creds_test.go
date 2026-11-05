package providers_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/vrannang1/spendmux/internal/providers"
)

func TestMarshalParseAWSCreds(t *testing.T) {
	t.Parallel()
	raw, err := providers.MarshalAWSCreds("AKIAEXAMPLE", "secret", "sess", "eu-west-1")
	if err != nil {
		t.Fatal(err)
	}
	c, err := providers.ParseAWSCreds(raw)
	if err != nil {
		t.Fatal(err)
	}
	if c.AccessKeyID != "AKIAEXAMPLE" || c.SecretAccessKey != "secret" || c.SessionToken != "sess" || c.Region != "eu-west-1" {
		t.Fatalf("%+v", c)
	}
	if _, err := providers.MarshalAWSCreds("", "x", "", ""); err == nil {
		t.Fatal("expected error")
	}
	if _, err := providers.ParseAWSCreds("not-json"); err == nil {
		t.Fatal("expected parse error")
	}
}

func TestMarshalParseVertexCreds(t *testing.T) {
	t.Parallel()
	sa := `{"type":"service_account","project_id":"p"}`
	raw, err := providers.MarshalVertexCreds(sa, "my-proj", "europe-west1")
	if err != nil {
		t.Fatal(err)
	}
	c, err := providers.ParseVertexCreds(raw)
	if err != nil {
		t.Fatal(err)
	}
	if c.Project != "my-proj" || c.Location != "europe-west1" || c.ServiceAccountJSON != sa {
		t.Fatalf("%+v", c)
	}
	if _, err := providers.MarshalVertexCreds("not-json", "p", "l"); err == nil {
		t.Fatal("expected invalid SA json")
	}
	def, err := providers.MarshalVertexCreds(sa, "p", "")
	if err != nil {
		t.Fatal(err)
	}
	parsed, _ := providers.ParseVertexCreds(def)
	if parsed.Location != "us-central1" {
		t.Fatalf("default location: %s", parsed.Location)
	}
}

func TestEndpointBuilders(t *testing.T) {
	t.Parallel()
	b := providers.BedrockOpenAIBaseURL("us-west-2")
	if !strings.Contains(b, "bedrock-runtime.us-west-2.amazonaws.com/openai/v1") {
		t.Fatal(b)
	}
	v := providers.VertexOpenAIBaseURL("proj", "us-central1")
	if !strings.Contains(v, "us-central1-aiplatform.googleapis.com") || !strings.Contains(v, "/projects/proj/") {
		t.Fatal(v)
	}
	var probe map[string]string
	_ = json.Unmarshal([]byte(`{}`), &probe)
}
