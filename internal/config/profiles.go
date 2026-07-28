package config

import (
	"fmt"
	"sort"
	"strings"
)

// Built-in profile names.
const (
	ProfileReadonly = "readonly"
	ProfileStandard = "standard"
	ProfileTrusted  = "trusted"
	ProfileCI       = "ci"
)

// BuiltinProfiles returns the permission profiles rick ships with.
//
// These exist so a user can write `"extends": ["ci"]` instead of restating
// twenty glob rules, and so /permission has meaningful presets to switch
// between. Every one of them can be overridden by declaring a profile with the
// same name in rick.json.
func BuiltinProfiles() map[string]Permission {
	no, yes := false, true

	return map[string]Permission{
		// readonly: inspect a codebase and change nothing. The sandbox is
		// read-only with no network, so even a prompt-injected command cannot
		// reach out or leave a trace.
		ProfileReadonly: {
			Default: PermDeny,
			Read:    PermAllow,
			Edit:    PermDeny,
			Write:   PermDeny,
			WebF:    PermAsk,
			Bash: map[string]string{
				"*":            PermDeny,
				"ls*":          PermAllow,
				"cat*":         PermAllow,
				"pwd":          PermAllow,
				"echo*":        PermAllow,
				"find*":        PermAllow,
				"grep*":        PermAllow,
				"rg*":          PermAllow,
				"git status*":  PermAllow,
				"git diff*":    PermAllow,
				"git log*":     PermAllow,
				"git show*":    PermAllow,
				"git blame*":   PermAllow,
				"go vet*":      PermAllow,
				"go list*":     PermAllow,
				"wc*":          PermAllow,
				"head*":        PermAllow,
				"tail*":        PermAllow,
			},
			Tools: map[string]string{
				"read": PermAllow, "grep": PermAllow, "glob": PermAllow,
				"list": PermAllow, "tree": PermAllow, "code_symbols": PermAllow,
				"todo*": PermAllow, "write": PermDeny, "edit": PermDeny,
				"apply_patch": PermDeny,
			},
			Sandbox: &SandboxConfig{Mode: "read-only", Network: &no},
		},

		// standard: the everyday default. Writes inside the workspace are
		// waved through, anything that leaves the machine or the directory
		// asks first.
		ProfileStandard: {
			Default: PermAllow,
			Read:    PermAllow,
			Edit:    PermAsk,
			Write:   PermAsk,
			WebF:    PermAllow,
			Bash: map[string]string{
				"*":            PermAsk,
				"ls*":          PermAllow,
				"cat*":         PermAllow,
				"pwd":          PermAllow,
				"echo*":        PermAllow,
				"git status*":  PermAllow,
				"git diff*":    PermAllow,
				"git log*":     PermAllow,
				"git show*":    PermAllow,
				"git add*":     PermAllow,
				"git commit*":  PermAsk,
				"go build*":    PermAllow,
				"go test*":     PermAllow,
				"go vet*":      PermAllow,
				"npm test*":    PermAllow,
				"npm run*":     PermAllow,
				"rm *":         PermAsk,
				"rm -rf*":      PermAsk,
				"git push*":    PermAsk,
				"curl*":        PermAsk,
				"wget*":        PermAsk,
				"sudo*":        PermDeny,
				"shutdown*":    PermDeny,
				"reboot*":      PermDeny,
				"mkfs*":        PermDeny,
				"dd if=*":      PermDeny,
			},
			Paths: map[string]string{
				"**/.env":         PermAsk,
				"**/.env.*":       PermAsk,
				"**/id_rsa":       PermDeny,
				"**/id_ed25519":   PermDeny,
				"**/.ssh/**":      PermDeny,
				"**/.aws/**":      PermDeny,
				"**/credentials*": PermAsk,
				"**/*.pem":        PermDeny,
				"**/*.key":        PermAsk,
			},
			Sandbox: &SandboxConfig{Mode: "workspace-write", Network: &yes},
		},

		// trusted: the user vouches for this workspace. Still not yolo — the
		// destructive-command denies stay, because a typo is not a trust
		// decision.
		ProfileTrusted: {
			Default: PermAllow,
			Read:    PermAllow,
			Edit:    PermAllow,
			Write:   PermAllow,
			WebF:    PermAllow,
			Bash: map[string]string{
				"*":         PermAllow,
				"sudo*":     PermAsk,
				"shutdown*": PermDeny,
				"reboot*":   PermDeny,
				"mkfs*":     PermDeny,
				"dd if=*":   PermAsk,
			},
			Paths: map[string]string{
				"**/.ssh/**": PermAsk,
				"**/.aws/**": PermAsk,
			},
			Sandbox: &SandboxConfig{Mode: "trusted", Network: &yes},
		},

		// ci: unattended automation. Nothing may prompt, because there is no
		// human to answer — so every rule is allow or deny and the sandbox is
		// enforced at the OS level or the run fails.
		ProfileCI: {
			Default: PermDeny,
			Read:    PermAllow,
			Edit:    PermAllow,
			Write:   PermAllow,
			WebF:    PermDeny,
			Bash: map[string]string{
				"*":           PermDeny,
				"ls*":         PermAllow,
				"cat*":        PermAllow,
				"echo*":       PermAllow,
				"pwd":         PermAllow,
				"go build*":   PermAllow,
				"go test*":    PermAllow,
				"go vet*":     PermAllow,
				"npm test*":   PermAllow,
				"npm run*":    PermAllow,
				"npm ci":      PermAllow,
				"pytest*":     PermAllow,
				"make*":       PermAllow,
				"git status*": PermAllow,
				"git diff*":   PermAllow,
				"git log*":    PermAllow,
			},
			Paths: map[string]string{
				"**/.env":    PermDeny,
				"**/.ssh/**": PermDeny,
				"**/.aws/**": PermDeny,
			},
			Sandbox: &SandboxConfig{
				Mode: "workspace-write", Enforcement: "os", Network: &no,
			},
		},
	}
}

// ProfileNames lists every available profile, built-ins plus user-defined.
func ProfileNames(cfg Config) []string {
	seen := map[string]bool{}
	var out []string
	for name := range BuiltinProfiles() {
		seen[name] = true
		out = append(out, name)
	}
	for name := range cfg.Profiles {
		if !seen[name] {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}

// LookupProfile resolves a profile by name, preferring a user override.
func LookupProfile(cfg Config, name string) (Permission, bool) {
	if p, ok := cfg.Profiles[name]; ok {
		return p, true
	}
	p, ok := BuiltinProfiles()[name]
	return p, ok
}

// ResolvePermission flattens a permission block's inheritance chain into a
// single effective policy.
//
// Later profiles in Extends override earlier ones, and the block's own fields
// override every profile. Cycles are broken rather than fatal: a profile
// already on the stack is skipped, so a self-referential config degrades to
// "ignore the loop" instead of hanging.
func ResolvePermission(cfg Config, p *Permission) *Permission {
	if p == nil {
		if std, ok := LookupProfile(cfg, ProfileStandard); ok {
			return &std
		}
		return &Permission{Default: PermAsk}
	}
	out := resolveInto(cfg, p, map[string]bool{}, 0)
	return &out
}

// ResolveProfileByName flattens a named profile.
func ResolveProfileByName(cfg Config, name string) (*Permission, error) {
	base, ok := LookupProfile(cfg, name)
	if !ok {
		return nil, fmt.Errorf("unknown permission profile %q (have: %s)",
			name, strings.Join(ProfileNames(cfg), ", "))
	}
	out := resolveInto(cfg, &base, map[string]bool{name: true}, 0)
	return &out, nil
}

const maxProfileDepth = 8

func resolveInto(cfg Config, p *Permission, visiting map[string]bool, depth int) Permission {
	if p == nil || depth > maxProfileDepth {
		return Permission{}
	}

	var acc Permission
	for _, name := range p.Extends {
		if visiting[name] {
			continue // cycle: ignore rather than recurse forever
		}
		parent, ok := LookupProfile(cfg, name)
		if !ok {
			continue
		}
		visiting[name] = true
		resolved := resolveInto(cfg, &parent, visiting, depth+1)
		delete(visiting, name)
		acc = mergePermission(acc, resolved)
	}

	return mergePermission(acc, *p)
}

// mergePermission layers over onto base. Non-empty scalars replace; maps merge
// key by key with over winning.
func mergePermission(base, over Permission) Permission {
	out := base
	out.Extends = nil // the chain is already flattened

	if over.Default != "" {
		out.Default = over.Default
	}
	if over.Edit != "" {
		out.Edit = over.Edit
	}
	if over.Write != "" {
		out.Write = over.Write
	}
	if over.Read != "" {
		out.Read = over.Read
	}
	if over.WebF != "" {
		out.WebF = over.WebF
	}

	out.Bash = mergeLevels(out.Bash, over.Bash)
	out.Tools = mergeLevels(out.Tools, over.Tools)
	out.Paths = mergeLevels(out.Paths, over.Paths)
	out.Hosts = mergeLevels(out.Hosts, over.Hosts)

	if over.Sandbox != nil {
		out.Sandbox = mergeSandbox(out.Sandbox, over.Sandbox)
	}
	return out
}

func mergeLevels(base, over map[string]string) map[string]string {
	if len(base) == 0 && len(over) == 0 {
		return nil
	}
	out := make(map[string]string, len(base)+len(over))
	for k, v := range base {
		out[k] = v
	}
	for k, v := range over {
		out[k] = v
	}
	return out
}

func mergeSandbox(base, over *SandboxConfig) *SandboxConfig {
	if base == nil {
		cp := *over
		return &cp
	}
	out := *base
	if over.Mode != "" {
		out.Mode = over.Mode
	}
	if over.Enforcement != "" {
		out.Enforcement = over.Enforcement
	}
	if over.Network != nil {
		out.Network = over.Network
	}
	if over.KeepCredentials != nil {
		out.KeepCredentials = over.KeepCredentials
	}
	if len(over.AllowHosts) > 0 {
		out.AllowHosts = over.AllowHosts
	}
	if len(over.DenyHosts) > 0 {
		out.DenyHosts = over.DenyHosts
	}
	if len(over.WritableRoots) > 0 {
		out.WritableRoots = append(append([]string{}, out.WritableRoots...), over.WritableRoots...)
	}
	if len(over.ReadableRoots) > 0 {
		out.ReadableRoots = append(append([]string{}, out.ReadableRoots...), over.ReadableRoots...)
	}
	if len(over.DenyPaths) > 0 {
		out.DenyPaths = append(append([]string{}, out.DenyPaths...), over.DenyPaths...)
	}
	if len(over.AllowEnv) > 0 {
		out.AllowEnv = append(append([]string{}, out.AllowEnv...), over.AllowEnv...)
	}
	if len(over.DenyEnv) > 0 {
		out.DenyEnv = append(append([]string{}, out.DenyEnv...), over.DenyEnv...)
	}
	if over.MemoryMB > 0 {
		out.MemoryMB = over.MemoryMB
	}
	if over.CPUSeconds > 0 {
		out.CPUSeconds = over.CPUSeconds
	}
	if over.Processes > 0 {
		out.Processes = over.Processes
	}
	if over.FileSizeMB > 0 {
		out.FileSizeMB = over.FileSizeMB
	}
	return &out
}
