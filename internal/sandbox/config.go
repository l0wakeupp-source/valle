package sandbox

import "rick/internal/config"

// FromConfig converts the JSON-facing config shape into a resolved Policy.
//
// A nil block yields Default(), so a config that never mentions the sandbox
// still gets workspace confinement rather than nothing.
func FromConfig(c *config.SandboxConfig, workspace string) Policy {
	p := Default()
	if c == nil {
		return p.Normalize(workspace)
	}

	if m, ok := ParseMode(c.Mode); ok {
		p.Mode = m
	}
	switch c.Enforcement {
	case string(EnforceAuto), string(EnforceOS), string(EnforceStatic):
		p.Enforcement = Enforcement(c.Enforcement)
	}
	if c.Network != nil {
		p.Network = *c.Network
	}
	if c.KeepCredentials != nil {
		p.KeepCredentials = *c.KeepCredentials
	}

	p.AllowHosts = c.AllowHosts
	p.DenyHosts = c.DenyHosts
	p.WritableRoots = c.WritableRoots
	p.ReadableRoots = c.ReadableRoots
	p.DenyPaths = c.DenyPaths
	p.AllowEnv = c.AllowEnv
	p.DenyEnv = c.DenyEnv

	if c.MemoryMB > 0 {
		p.Limits.MemoryMB = c.MemoryMB
	}
	if c.CPUSeconds > 0 {
		p.Limits.CPUSeconds = c.CPUSeconds
	}
	if c.Processes > 0 {
		p.Limits.Processes = c.Processes
	}
	if c.FileSizeMB > 0 {
		p.Limits.FileSizeMB = c.FileSizeMB
	}

	return p.Normalize(workspace)
}

// ToConfig renders a Policy back into its JSON shape, for /config display and
// for persisting a policy the user changed at runtime.
func ToConfig(p Policy) *config.SandboxConfig {
	network, keep := p.Network, p.KeepCredentials
	return &config.SandboxConfig{
		Mode:            string(p.Mode),
		Enforcement:     string(p.Enforcement),
		Network:         &network,
		AllowHosts:      p.AllowHosts,
		DenyHosts:       p.DenyHosts,
		WritableRoots:   p.WritableRoots,
		ReadableRoots:   p.ReadableRoots,
		DenyPaths:       p.DenyPaths,
		AllowEnv:        p.AllowEnv,
		DenyEnv:         p.DenyEnv,
		KeepCredentials: &keep,
		MemoryMB:        p.Limits.MemoryMB,
		CPUSeconds:      p.Limits.CPUSeconds,
		Processes:       p.Limits.Processes,
		FileSizeMB:      p.Limits.FileSizeMB,
	}
}
