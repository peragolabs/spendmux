package providers

import (
	"encoding/json"
	"fmt"
	"strings"
)

// AWSCreds is stored encrypted as JSON in ProviderKey.EncryptedKey for AuthStyle aws-sigv4.
type AWSCreds struct {
	AccessKeyID     string `json:"access_key_id"`
	SecretAccessKey string `json:"secret_access_key"`
	SessionToken    string `json:"session_token,omitempty"`
	Region          string `json:"region"`
}

// VertexCreds is stored encrypted as JSON for AuthStyle vertex.
type VertexCreds struct {
	ServiceAccountJSON string `json:"service_account_json"`
	Project            string `json:"project"`
	Location           string `json:"location"`
}

func ParseAWSCreds(raw string) (AWSCreds, error) {
	var c AWSCreds
	if err := json.Unmarshal([]byte(raw), &c); err != nil {
		return c, fmt.Errorf("invalid bedrock credentials — re-save AWS keys on /keys")
	}
	c.AccessKeyID = strings.TrimSpace(c.AccessKeyID)
	c.SecretAccessKey = strings.TrimSpace(c.SecretAccessKey)
	c.SessionToken = strings.TrimSpace(c.SessionToken)
	c.Region = strings.TrimSpace(c.Region)
	if c.AccessKeyID == "" || c.SecretAccessKey == "" {
		return c, fmt.Errorf("bedrock needs access_key_id and secret_access_key")
	}
	if c.Region == "" {
		c.Region = "us-east-1"
	}
	return c, nil
}

func ParseVertexCreds(raw string) (VertexCreds, error) {
	var c VertexCreds
	if err := json.Unmarshal([]byte(raw), &c); err != nil {
		return c, fmt.Errorf("invalid vertex credentials — re-save service account on /keys")
	}
	c.ServiceAccountJSON = strings.TrimSpace(c.ServiceAccountJSON)
	c.Project = strings.TrimSpace(c.Project)
	c.Location = strings.TrimSpace(c.Location)
	if c.ServiceAccountJSON == "" {
		return c, fmt.Errorf("vertex needs a service account JSON")
	}
	if c.Project == "" {
		return c, fmt.Errorf("vertex needs a GCP project id")
	}
	if c.Location == "" {
		c.Location = "us-central1"
	}
	return c, nil
}

// MarshalAWSCreds builds the vault payload for Bedrock.
func MarshalAWSCreds(accessKey, secretKey, sessionToken, region string) (string, error) {
	c := AWSCreds{
		AccessKeyID:     strings.TrimSpace(accessKey),
		SecretAccessKey: strings.TrimSpace(secretKey),
		SessionToken:    strings.TrimSpace(sessionToken),
		Region:          strings.TrimSpace(region),
	}
	if c.AccessKeyID == "" || c.SecretAccessKey == "" {
		return "", fmt.Errorf("access key and secret are required")
	}
	if c.Region == "" {
		c.Region = "us-east-1"
	}
	b, err := json.Marshal(c) // #nosec G117 -- encrypted vault payload; never logged or returned
	return string(b), err
}

// MarshalVertexCreds builds the vault payload for Vertex AI.
func MarshalVertexCreds(saJSON, project, location string) (string, error) {
	c := VertexCreds{
		ServiceAccountJSON: strings.TrimSpace(saJSON),
		Project:            strings.TrimSpace(project),
		Location:           strings.TrimSpace(location),
	}
	if c.ServiceAccountJSON == "" {
		return "", fmt.Errorf("service account JSON is required")
	}
	if c.Project == "" {
		return "", fmt.Errorf("GCP project is required")
	}
	if c.Location == "" {
		c.Location = "us-central1"
	}
	// Validate JSON shape early so bad pastes fail at save time.
	var probe map[string]interface{}
	if err := json.Unmarshal([]byte(c.ServiceAccountJSON), &probe); err != nil {
		return "", fmt.Errorf("service account must be valid JSON")
	}
	b, err := json.Marshal(c)
	return string(b), err
}

// BedrockOpenAIBaseURL is the OpenAI-compatible Bedrock Runtime endpoint for a region.
func BedrockOpenAIBaseURL(region string) string {
	region = strings.TrimSpace(region)
	if region == "" {
		region = "us-east-1"
	}
	return fmt.Sprintf("https://bedrock-runtime.%s.amazonaws.com/openai/v1", region)
}

// VertexOpenAIBaseURL is the OpenAI-compatible Vertex AI endpoint.
func VertexOpenAIBaseURL(project, location string) string {
	project = strings.TrimSpace(project)
	location = strings.TrimSpace(location)
	if location == "" {
		location = "us-central1"
	}
	return fmt.Sprintf(
		"https://%s-aiplatform.googleapis.com/v1beta1/projects/%s/locations/%s/endpoints/openapi",
		location, project, location,
	)
}
