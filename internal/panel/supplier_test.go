package panel

import (
	"context"
	"net/http/httptest"
	"testing"

	"github.com/linguo2625469/workbuddy2api-panel/internal/gateway"
	"github.com/linguo2625469/workbuddy2api-panel/internal/livecfg"
	"github.com/linguo2625469/workbuddy2api-panel/internal/pool"
	"github.com/linguo2625469/workbuddy2api-panel/internal/upstream"
)

type unusedSupplier struct{ t *testing.T }

func (s unusedSupplier) GatewayAccounts() []gateway.Account {
	s.t.Error("model endpoint queried accounts from an unselected application")
	return nil
}
func (s unusedSupplier) GatewayCredential(context.Context, string) (gateway.Credential, error) {
	s.t.Error("model endpoint queried credentials from an unselected application")
	return gateway.Credential{}, nil
}

func TestPanelSelectedSupplierDoesNotFallbackToOtherCatalogs(t *testing.T) {
	live := livecfg.New(livecfg.Snapshot{GatewaySupplier: "loomy"})
	p := New(Config{
		Pool: pool.New(""), Upstream: upstream.New(), Live: live,
		Providers: gateway.New(map[string]gateway.Source{"qoder": unusedSupplier{t}}),
	})
	w := httptest.NewRecorder()
	p.ServeHTTP(w, httptest.NewRequest("GET", "/panel/api/models", nil))
	if w.Code != 503 {
		t.Fatalf("missing selected application must not borrow a catalog: %d %s", w.Code, w.Body.String())
	}
}
