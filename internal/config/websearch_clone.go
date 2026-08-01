package config

// CloneWebSearchConfig returns a detached snapshot safe for a running tool call.
// UI settings can be edited while a search is in flight; slices, maps, and
// pointer fields must not be shared with that call.
func CloneWebSearchConfig(in *WebSearchConfig) *WebSearchConfig {
	if in == nil {
		return nil
	}
	out := *in
	out.AllowDomains = append([]string(nil), in.AllowDomains...)
	out.DenyDomains = append([]string(nil), in.DenyDomains...)
	if in.Parallel != nil {
		value := *in.Parallel
		out.Parallel = &value
	}
	if in.Providers != nil {
		out.Providers = make(map[string]WebSearchProviderConfig, len(in.Providers))
		for name, provider := range in.Providers {
			copyProvider := provider
			copyProvider.Instances = append([]string(nil), provider.Instances...)
			copyProvider.IncludeDomains = append([]string(nil), provider.IncludeDomains...)
			copyProvider.ExcludeDomains = append([]string(nil), provider.ExcludeDomains...)
			if provider.Enabled != nil {
				value := *provider.Enabled
				copyProvider.Enabled = &value
			}
			if provider.MaxAgeHours != nil {
				value := *provider.MaxAgeHours
				copyProvider.MaxAgeHours = &value
			}
			out.Providers[name] = copyProvider
		}
	}
	return &out
}
