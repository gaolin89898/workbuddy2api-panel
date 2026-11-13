package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/linguo2625469/workbuddy2api-panel/internal/gateway"
	"github.com/linguo2625469/workbuddy2api-panel/internal/prompt"
	"github.com/linguo2625469/workbuddy2api-panel/internal/reqlog"
	"github.com/linguo2625469/workbuddy2api-panel/internal/session"
	"github.com/linguo2625469/workbuddy2api-panel/internal/usage"
)

func (h *Handler) providerChat(w http.ResponseWriter, r *http.Request, body []byte, stream bool) {
	st := newChatStat(time.Now(), body, stream)
	if tr := requestTraceFrom(r); tr != nil {
		tr.stat = st
		st.requestID = tr.id
		st.clientIP, st.userAgent = tr.clientIP, tr.userAgent
	}
	defer st.done()
	if _, hide := h.loadLive().HiddenModels[st.model]; hide {
		st.status = 400
		st.outcome = reqlog.OutcomeHTTPError
		writeOpenAIError(w, 400, "model_unavailable", "model is hidden by gateway configuration")
		return
	}
	if h.cfg.Providers == nil {
		st.status = 503
		st.outcome = reqlog.OutcomeHTTPError
		writeOpenAIError(w, 503, "provider_unavailable", "provider gateway is unavailable")
		return
	}
	key := session.ExtractKey(body)
	if h.cfg.PromptText != "" {
		switch h.cfg.PromptMode {
		case "custom":
			body = prompt.Rewrite(body, h.cfg.PromptText)
		case "append":
			body = prompt.Append(body, h.cfg.PromptText)
		}
	}
	started := false
	var completion gateway.Completion
	err := h.cfg.Providers.Chat(r.Context(), body, key, func(chunk map[string]any) error {
		if !stream {
			return completion.Add(chunk)
		}
		raw, err := json.Marshal(chunk)
		if err != nil {
			return err
		}
		if !started {
			w.Header().Set("Content-Type", "text/event-stream")
			w.Header().Set("Cache-Control", "no-cache")
			w.Header().Set("X-Accel-Buffering", "no")
			w.WriteHeader(200)
			started = true
		}
		if _, err = fmt.Fprintf(w, "data: %s\n\n", raw); err != nil {
			return err
		}
		return http.NewResponseController(w).Flush()
	}, func(a gateway.Attempt) {
		st.attempts++
		st.uid, st.nick = a.Account, a.Nickname
		st.status = a.Status
		if st.ttfb == 0 && a.TTFB > 0 {
			st.ttfb = a.Started.Sub(st.start) + a.TTFB
		}
		delta := usageDeltaFromResponse(map[string]any{"usage": a.Usage})
		hit, miss, hasCache := cacheTokensFromUsage(a.Usage)
		st.promptTokens += delta.PromptTokens
		st.completionTokens += delta.CompletionTokens
		st.totalTokens += delta.TotalTokens
		if delta.HasCompletionTokens {
			if st.toks < 0 {
				st.toks = 0
			}
			st.toks += int(delta.CompletionTokens)
		}
		credit, hasCredit := a.Usage["credit"].(float64)
		if !hasCredit {
			credit, hasCredit = a.Usage["credits"].(float64)
		}
		st.credit += credit
		st.hasCredit = st.hasCredit || hasCredit
		st.cacheHit += hit
		st.cacheMiss += miss
		st.hasCache = st.hasCache || hasCache
		d := usage.Delta{PromptTokens: delta.PromptTokens, HasPromptTokens: delta.HasPromptTokens, CompletionTokens: delta.CompletionTokens, HasCompletion: delta.HasCompletionTokens, TotalTokens: delta.TotalTokens, HasTotal: delta.HasTotalTokens, Credit: credit, HasCredit: hasCredit, HasCacheTokens: hasCache, CacheHitTokens: hit, CacheMissTokens: miss, LatencyMs: time.Since(a.Started).Milliseconds(), HasLatency: true}
		if seconds := (time.Since(a.Started) - a.TTFB).Seconds(); a.TTFB > 0 && seconds > 0 && delta.HasCompletionTokens {
			d.HasTPS = true
			d.TokensPerSecond = float64(delta.CompletionTokens) / seconds
		}
		h.cfg.Usage.Add(a.Started, a.Provider, a.Account, a.Model, d, a.Err == nil)
	})
	if err != nil {
		st.status = gateway.Status(err)
		st.outcome = reqlog.OutcomeHTTPError
		if r.Context().Err() != nil {
			st.outcome = reqlog.OutcomeInterrupted
			return
		}
		if started {
			st.outcome = reqlog.OutcomeStreamError
			raw, _ := json.Marshal(map[string]any{"error": map[string]any{"message": err.Error(), "type": "upstream_error", "code": "upstream_error"}})
			fmt.Fprintf(w, "event: error\ndata: %s\n\n", raw)
			_ = http.NewResponseController(w).Flush()
			return
		}
		writeOpenAIError(w, st.status, "upstream_error", err.Error())
		return
	}
	st.status = 200
	st.outcome = reqlog.OutcomeSuccess
	if stream {
		fmt.Fprint(w, "data: [DONE]\n\n")
		_ = http.NewResponseController(w).Flush()
	} else {
		writeJSON(w, 200, completion.Response())
	}
}
