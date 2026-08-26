package catalog

import "github.com/routatic/proxy/internal/config"

// catalogProviderAliases maps a models.dev provider id to the canonical
// provider name routatic dispatches to. models.dev groups some upstreams under
// ids that differ from routatic's provider identifiers — it calls AWS Bedrock
// "amazon-bedrock" and Cloudflare Workers AI "cloudflare-workers-ai". Without
// this mapping their models resolve to a provider name the dispatcher does not
// recognize, so they are silently unroutable.
//
// cloudflare-ai-gateway is intentionally omitted: its model ids are already
// namespaced (e.g. "anthropic/claude-sonnet-4", "workers-ai/@cf/..."), which
// the Cloudflare gateway model-id prefixing would corrupt. Only the direct
// Workers AI provider is aliased to cloudflare.
var catalogProviderAliases = map[string]string{
	"amazon-bedrock":        config.ProviderAWSBedrock,
	"cloudflare-workers-ai": config.ProviderCloudflare,
}

// canonicalProviderID returns the routatic canonical provider name for a
// models.dev provider id, or the id unchanged when no alias applies.
func canonicalProviderID(id string) string {
	if canonical, ok := catalogProviderAliases[id]; ok {
		return canonical
	}
	return id
}

// ingestProviderModels folds each dispatchable provider's own catalog
// (providers[<id>].models) into the top-level Models map, keyed
// "<canonical-provider>/<model-id>", and ensures a canonical Providers entry
// exists for it.
//
// models.dev stores reseller/gateway providers' catalogs (opencode-go,
// cloudflare-workers-ai, amazon-bedrock, ...) only inside their provider
// sub-maps, not in the top-level Models map the router resolves against. Only
// providers routatic can dispatch to are ingested; an existing flat entry wins
// over a sub-map duplicate. Callers must run this before validation and
// indexing so the ingested models participate in both.
func (c *Catalog) ingestProviderModels() {
	if c.Models == nil {
		c.Models = make(map[string]Model)
	}

	// Stage additions so the Providers map is not mutated mid-range.
	addProviders := make(map[string]Provider)
	addModels := make(map[string]Model)

	for id, provider := range c.Providers {
		if len(provider.Models) == 0 {
			continue
		}
		canonical := canonicalProviderID(id)
		if !config.IsKnownProvider(canonical) {
			continue
		}

		// Stage a canonical provider entry whose Name is the canonical id, so
		// resolution reports routatic's provider identity rather than models.dev's
		// display name ("OpenCode Go") or aliased id ("cloudflare-workers-ai").
		// Prefer an existing canonical-id entry as the source when one exists.
		if _, staged := addProviders[canonical]; !staged {
			source := provider
			if existing, ok := c.Providers[canonical]; ok {
				source = existing
			}
			source.Name = canonical
			source.Models = nil
			addProviders[canonical] = source
		}

		for modelID, model := range provider.Models {
			key := canonical + "/" + modelID
			if _, exists := c.Models[key]; exists {
				continue
			}
			if _, staged := addModels[key]; staged {
				continue
			}
			if model.ID == "" {
				model.ID = modelID
			}
			addModels[key] = model
		}
	}

	for id, provider := range addProviders {
		c.Providers[id] = provider
	}
	for key, model := range addModels {
		c.Models[key] = model
	}
}
