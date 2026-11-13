package main

import "github.com/linguo2625469/workbuddy2api-panel/internal/gateway"

func (rt *appRuntime) initProviders() {
	rt.providers = gateway.New(map[string]gateway.Source{"loomy": rt.loomy, "minimax": rt.minimax, "qoder": rt.qoder, "trae": rt.trae, "antigravity": rt.antigravity, "doubao": rt.doubao})
	rt.providers.MaxInFlight = rt.cfg.Pool.MaxInFlight
	rt.providers.Cooldown = rt.cfg.SoftRateDur
}
