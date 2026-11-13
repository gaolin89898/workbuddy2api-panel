package gateway

import (
	"context"
	"net/http"
	"testing"
)

type forbiddenSupplierSource struct{ t *testing.T }

func (s forbiddenSupplierSource) GatewayAccounts() []Account {
	s.t.Error("queried another application's accounts")
	return nil
}
func (s forbiddenSupplierSource) GatewayCredential(context.Context, string) (Credential, error) {
	s.t.Error("queried another application's credentials")
	return Credential{}, nil
}

func TestSelectedSupplierCatalogDoesNotQueryOtherApplications(t *testing.T) {
	r := testRouter(t, "loomy", func(w http.ResponseWriter, req *http.Request) {
		loomyCatalog(w)
	}, Account{ID: "a"})
	r.Sources["qoder"] = forbiddenSupplierSource{t}
	models := r.ModelsForSupplier(context.Background(), "loomy")
	if len(models) != 1 || str(models[0]["id"]) != "loomy/chat" {
		t.Fatalf("wrong catalog: %v", models)
	}
	if models := r.ModelsForSupplier(context.Background(), "workbuddy-cn"); len(models) != 0 {
		t.Fatal("native provider catalog leaked into WorkBuddy selection")
	}
}

func TestSupplierModelBoundariesAndBareNames(t *testing.T) {
	for _, tc := range []struct{ supplier, model, want string }{
		{"auto", "cn:m", "cn:m"}, {"auto", "loomy/chat", "loomy/chat"},
		{"workbuddy-cn", "m", "cn:m"}, {"workbuddy-cn", "cn:m", "cn:m"},
		{"workbuddy-global", "m", "global:m"}, {"loomy", "chat", "loomy/chat"},
		{"loomy", "loomy/chat", "loomy/chat"}, {"trae", "qoder/chat", ""},
		{"workbuddy-cn", "global:m", ""}, {"loomy", "cn:m", ""},
		{"workbuddy-global", "loomy/chat", ""}, {"loomy", "", ""},
	} {
		got, err := ResolveSupplierModel(tc.supplier, tc.model)
		if got != tc.want || (err != nil) != (tc.want == "") {
			t.Fatalf("%+v: got %q err=%v", tc, got, err)
		}
	}
}
