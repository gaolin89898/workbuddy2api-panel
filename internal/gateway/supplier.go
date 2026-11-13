package gateway

import (
	"fmt"
	"strings"
)

// NormalizeSupplier validates the gateway-wide routing choice. Empty legacy
// configurations keep automatic model-prefix routing.
func NormalizeSupplier(value string) (string, error) {
	value = strings.ToLower(strings.TrimSpace(value))
	switch value {
	case "", "auto":
		return "auto", nil
	case "workbuddy-cn", "workbuddy-global", "loomy", "minimax", "qoder", "trae", "antigravity", "doubao":
		return value, nil
	default:
		return "", fmt.Errorf("gateway.supplier: unsupported application %q", value)
	}
}

// SupplierAllowsModel applies the same catalog boundary to panel and API output.
func SupplierAllowsModel(supplier, model string) bool {
	if supplier == "" || supplier == "auto" {
		return true
	}
	switch supplier {
	case "workbuddy-cn":
		return strings.HasPrefix(model, "cn:")
	case "workbuddy-global":
		return strings.HasPrefix(model, "global:")
	default:
		return strings.HasPrefix(model, supplier+"/")
	}
}

// ResolveSupplierModel accepts bare names for the selected application, while
// rejecting an explicit model from another application instead of falling back.
func ResolveSupplierModel(supplier, model string) (string, error) {
	if supplier == "" || supplier == "auto" {
		return model, nil
	}
	if model == "" {
		return "", fmt.Errorf("model is required")
	}
	_, _, native := Split(model)
	explicit := native || strings.HasPrefix(model, "cn:") || strings.HasPrefix(model, "global:")
	if explicit {
		if !SupplierAllowsModel(supplier, model) {
			return "", fmt.Errorf("model %q is not supplied by selected application %s", model, supplier)
		}
		return model, nil
	}
	switch supplier {
	case "workbuddy-cn":
		return "cn:" + model, nil
	case "workbuddy-global":
		return "global:" + model, nil
	default:
		return supplier + "/" + model, nil
	}
}
