package providers

import "testing"

func TestResolveRouteWithDefault(t *testing.T) {
	cases := []struct {
		provider, model, def string
		wantP, wantM         string
		wantErr              bool
	}{
		{"cerebras", "gpt-oss-120b", "", "cerebras", "gpt-oss-120b", false},
		{"", "gpt-oss-120b", "cerebras", "cerebras", "gpt-oss-120b", false},       // token scoped to a provider
		{"groq", "openai/gpt-oss-120b", "", "groq", "openai/gpt-oss-120b", false}, // explicit provider keeps slashed model
		{"", "openai/gpt-oss-120b", "groq", "groq", "openai/gpt-oss-120b", false}, // scope beats shorthand
		{"", "cerebras/gpt-oss-120b", "", "cerebras", "gpt-oss-120b", false},      // shorthand
		{"", "not-a-provider/model", "", "", "", true},                            // unknown prefix
		{"", "gpt-oss-120b", "", "", "", true},                                    // nothing to go on
	}
	for _, tc := range cases {
		p, m, err := ResolveRouteWithDefault(tc.provider, tc.model, tc.def)
		if (err != nil) != tc.wantErr {
			t.Fatalf("%+v: err=%v", tc, err)
		}
		if !tc.wantErr && (p != tc.wantP || m != tc.wantM) {
			t.Fatalf("%+v: got %s/%s", tc, p, m)
		}
	}
}
