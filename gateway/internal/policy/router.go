package policy

import (
	"encoding/json"
	"fmt"

	"github.com/miqui/kind-llm-gateway/gateway/internal/tenants"
)

// RouterConfig is the routing table used by ResolveTarget, sourced from
// config.Config (LLAMA_CHAT_URL / LLAMA_EMBED_URL) plus a model->workload
// map and a workload-tier->target map. Both maps are config-driven so the
// mechanism is testable without hardcoding tenant/model data here.
type RouterConfig struct {
	ChatUpstreamURL  string
	EmbedUpstreamURL string

	// Models maps a configured model name to its workload class ("chat" or
	// "embed"). An empty string means "no explicit workload"; the tier map
	// is consulted instead. A model absent from this map entirely is
	// unconfigured/unknown.
	Models map[string]string

	// TierTargets maps a tenant's WorkloadClass/CostTier to a workload
	// ("chat" or "embed"), used when a model has no explicit workload.
	// Defaults to standard/economy -> chat if unset.
	TierTargets map[string]string
}

const (
	embeddingsPath = "/v1/embeddings"

	workloadChat  = "chat"
	workloadEmbed = "embed"
)

// RouteError is a typed, JSON-serializable routing rejection returned by
// ResolveTarget.
type RouteError struct {
	Type       string
	Message    string
	StatusCode int
}

func (e *RouteError) Error() string {
	return fmt.Sprintf("%s: %s", e.Type, e.Message)
}

type routeErrorEnvelope struct {
	Error routeErrorBody `json:"error"`
}

type routeErrorBody struct {
	Type    string `json:"type"`
	Message string `json:"message"`
}

// JSON renders the error in the API's typed error envelope shape:
// {"error":{"type":"...","message":"..."}}.
func (e *RouteError) JSON() ([]byte, error) {
	return json.Marshal(routeErrorEnvelope{Error: routeErrorBody{Type: e.Type, Message: e.Message}})
}

// ResolveTarget determines the upstream URL for a request, applying (in
// order): the always-embed rule for /v1/embeddings, the tenant model
// allowlist, model existence in the routing table, and finally
// workload-class/cost-tier-driven target selection for models without an
// explicit workload assignment.
func ResolveTarget(cfg RouterConfig, tenant tenants.Tenant, model string, path string) (string, error) {
	if !allowed(tenant.AllowedModels, model) {
		return "", &RouteError{
			Type:       "model_not_allowed",
			Message:    fmt.Sprintf("model %s not allowed for tenant %s", model, tenant.Name),
			StatusCode: 403,
		}
	}

	workload, ok := cfg.Models[model]
	if !ok {
		return "", &RouteError{
			Type:       "model_not_found",
			Message:    fmt.Sprintf("model %s is not configured", model),
			StatusCode: 404,
		}
	}

	// /v1/embeddings always routes to the embed upstream, regardless of the
	// model's configured workload.
	if path == embeddingsPath {
		return cfg.EmbedUpstreamURL, nil
	}

	if workload == "" {
		workload = tierWorkload(cfg.TierTargets, tenant.CostTier)
	}

	if workload == workloadEmbed {
		return cfg.EmbedUpstreamURL, nil
	}
	return cfg.ChatUpstreamURL, nil
}

func allowed(allowedModels []string, model string) bool {
	for _, m := range allowedModels {
		if m == model {
			return true
		}
	}
	return false
}

// tierWorkload resolves a cost tier to a workload class using the
// config-driven tier map, defaulting standard/economy (and anything else
// unmapped) to "chat" for now.
func tierWorkload(tierTargets map[string]string, costTier string) string {
	if w, ok := tierTargets[costTier]; ok && w != "" {
		return w
	}
	return workloadChat
}
