package sandbox

import (
	"os"
	"strings"

	"rick/internal/glob"
)

// credentialPatterns are environment variables a sandboxed command has no
// business reading. Matched case-insensitively as globs.
var credentialPatterns = []string{
	"*_API_KEY", "*_APIKEY", "*_TOKEN", "*_SECRET", "*_PASSWORD", "*_PASSWD",
	"*_CREDENTIALS", "*_PRIVATE_KEY", "*_ACCESS_KEY", "*_SESSION_TOKEN",
	"ANTHROPIC_*", "OPENAI_*", "OPENROUTER_*", "GROQ_*", "DEEPSEEK_*",
	"GEMINI_*", "GOOGLE_API_*", "TOGETHER_*", "MISTRAL_*", "COHERE_*",
	"AWS_*", "AZURE_*", "GCP_*", "GOOGLE_APPLICATION_CREDENTIALS",
	"GITHUB_TOKEN", "GH_TOKEN", "GITLAB_TOKEN", "NPM_TOKEN", "PYPI_*",
	"DOCKER_PASSWORD", "DOCKERHUB_*", "SLACK_*", "STRIPE_*", "TWILIO_*",
	"SENTRY_*", "DATABASE_URL", "REDIS_URL", "MONGO_URL", "POSTGRES_*",
	"MYSQL_*", "SSH_AUTH_SOCK", "RICK_*",
}

// essentialVars always survive scrubbing; without them most toolchains break.
var essentialVars = []string{
	"PATH", "HOME", "USER", "USERNAME", "LOGNAME", "SHELL", "TERM", "TMPDIR",
	"TEMP", "TMP", "LANG", "LC_*", "TZ", "PWD", "OLDPWD",
	// Windows needs a surprising amount of scaffolding to run anything.
	"SYSTEMROOT", "SYSTEMDRIVE", "WINDIR", "COMSPEC", "PATHEXT",
	"PROGRAMFILES", "PROGRAMFILES(X86)", "PROGRAMDATA", "COMMONPROGRAMFILES",
	"APPDATA", "LOCALAPPDATA", "USERPROFILE", "HOMEDRIVE", "HOMEPATH",
	"NUMBER_OF_PROCESSORS", "PROCESSOR_ARCHITECTURE", "OS",
	// Toolchain roots that are paths, not secrets.
	"GOPATH", "GOROOT", "GOCACHE", "GOMODCACHE", "GOFLAGS", "CARGO_HOME",
	"RUSTUP_HOME", "JAVA_HOME", "NODE_PATH", "NVM_DIR", "PYTHONPATH",
	"VIRTUAL_ENV", "CONDA_PREFIX", "MSYSTEM", "PS_MODULE_PATH",
}

// Environ builds the environment for a sandboxed command from the parent
// environment, applying credential scrubbing and network blackholing.
func Environ(p Policy, extra ...string) []string {
	base := os.Environ()
	out := make([]string, 0, len(base)+len(extra)+8)

	for _, kv := range base {
		key, _, ok := strings.Cut(kv, "=")
		if !ok {
			continue
		}
		if p.keepVar(key) {
			out = append(out, kv)
		}
	}

	// Signal to child processes that they are confined, and keep pagers off.
	out = append(out,
		"RICK=1",
		"RICK_SANDBOX="+string(p.Mode),
		"GIT_PAGER=cat", "PAGER=cat", "TERM=dumb", "NO_COLOR=1",
	)

	if !p.Network {
		// Well-behaved HTTP clients honour these; the OS layer is what stops
		// the rest. Pointing at a closed loopback port fails fast instead of
		// hanging on a DNS timeout.
		const blackhole = "http://127.0.0.1:9"
		out = append(out,
			"http_proxy="+blackhole, "HTTP_PROXY="+blackhole,
			"https_proxy="+blackhole, "HTTPS_PROXY="+blackhole,
			"all_proxy="+blackhole, "ALL_PROXY="+blackhole,
			"no_proxy=", "NO_PROXY=",
			"NPM_CONFIG_OFFLINE=true", "GOFLAGS=-mod=mod", "GOPROXY=off",
			"PIP_NO_INDEX=1", "CARGO_NET_OFFLINE=true",
		)
	}

	out = append(out, extra...)
	return out
}

// keepVar decides whether an environment variable survives into the sandbox.
func (p Policy) keepVar(key string) bool {
	upper := strings.ToUpper(key)

	// An explicit deny always wins.
	if matchesAny(p.DenyEnv, upper) {
		return false
	}
	// An explicit allow overrides the credential heuristics.
	if matchesAny(p.AllowEnv, upper) {
		return true
	}
	if matchesAny(essentialVars, upper) {
		return true
	}
	if p.KeepCredentials || p.Mode == ModeOff || p.Mode == ModeTrusted {
		return true
	}
	return !matchesAny(credentialPatterns, upper)
}

func matchesAny(patterns []string, upper string) bool {
	for _, pat := range patterns {
		if glob.Match(strings.ToUpper(pat), upper) {
			return true
		}
	}
	return false
}

// ScrubbedCount reports how many variables the policy would strip, for the
// /sandbox display.
func ScrubbedCount(p Policy) int {
	n := 0
	for _, kv := range os.Environ() {
		key, _, ok := strings.Cut(kv, "=")
		if ok && !p.keepVar(key) {
			n++
		}
	}
	return n
}
