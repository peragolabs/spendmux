package keys

import (
	"fmt"
	"strings"

	"github.com/peragolabs/spendmux/models"
)

// EnforceKeyScope rejects requests outside a virtual key's provider/model allowlist.
// Empty AllowedProvider / AllowedModel means unrestricted.
func EnforceKeyScope(pk models.PlatformAPIKey, provider, model string) error {
	wantP := strings.ToLower(strings.TrimSpace(pk.AllowedProvider))
	wantM := strings.TrimSpace(pk.AllowedModel)
	gotP := strings.ToLower(strings.TrimSpace(provider))
	gotM := strings.TrimSpace(model)

	if wantP != "" && wantP != gotP {
		return fmt.Errorf("this api key is scoped to provider %q (got %q)", wantP, gotP)
	}
	if wantM != "" && wantM != gotM {
		return fmt.Errorf("this api key is scoped to model %q (got %q)", wantM, gotM)
	}
	return nil
}
