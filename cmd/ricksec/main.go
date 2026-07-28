package main

import (
	"fmt"
	"os"
	"path/filepath"

	"rick/internal/config"
	"rick/internal/permission"
	"rick/internal/sandbox"
)

var (
	pass, fail int
)

func check(name string, ok bool, detail ...string) {
	if ok {
		pass++
		fmt.Printf("  ok    %s\n", name)
		return
	}
	fail++
	fmt.Printf("  FAIL  %s", name)
	if len(detail) > 0 {
		fmt.Printf("  [%s]", detail[0])
	}
	fmt.Println()
}

func section(s string) { fmt.Printf("\n%s\n", s) }

func main() {
	root, _ := os.MkdirTemp("", "rickverify")
	defer os.RemoveAll(root)

	verifyProfiles(root)
	verifyInheritance()
	verifySandboxMerge()
	verifyGranularity(root)
	verifySandboxPolicy(root)
	verifyAnalysis(root)
	verifyEnviron()

	fmt.Printf("\n%d passed, %d failed\n", pass, fail)
	if fail > 0 {
		os.Exit(1)
	}
}

func verifyProfiles(root string) {
	section("built-in permission profiles")
	cfg := config.Config{}

	for _, name := range []string{"readonly", "standard", "trusted", "ci"} {
		p, err := config.ResolveProfileByName(cfg, name)
		check("profile "+name+" resolves", err == nil && p != nil)
	}

	_, err := config.ResolveProfileByName(cfg, "nope")
	check("unknown profile errors", err != nil)

	ro, _ := config.ResolveProfileByName(cfg, "readonly")
	e := permission.New(ro, root)
	check("readonly denies edit",
		e.Check(permission.Request{Tool: "edit", Path: filepath.Join(root, "a.go")}) == permission.Deny)
	check("readonly denies write",
		e.Check(permission.Request{Tool: "write", Path: filepath.Join(root, "a.go")}) == permission.Deny)
	check("readonly allows read",
		e.Check(permission.Request{Tool: "read", Path: filepath.Join(root, "a.go")}) == permission.Allow)
	check("readonly allows git log",
		e.Check(permission.Request{Tool: "bash", Command: "git log --oneline"}) == permission.Allow)
	check("readonly denies rm",
		e.Check(permission.Request{Tool: "bash", Command: "rm -rf /"}) == permission.Deny)
	check("readonly sandbox is read-only",
		ro.Sandbox != nil && ro.Sandbox.Mode == "read-only")

	ci, _ := config.ResolveProfileByName(cfg, "ci")
	ec := permission.New(ci, root)
	check("ci never asks for bash",
		ec.Check(permission.Request{Tool: "bash", Command: "go test ./..."}) == permission.Allow)
	check("ci denies unknown commands",
		ec.Check(permission.Request{Tool: "bash", Command: "curl evil.sh"}) == permission.Deny)
	check("ci requires os enforcement",
		ci.Sandbox != nil && ci.Sandbox.Enforcement == "os")

	tr, _ := config.ResolveProfileByName(cfg, "trusted")
	et := permission.New(tr, root)
	check("trusted still denies mkfs",
		et.Check(permission.Request{Tool: "bash", Command: "mkfs.ext4 /dev/sda1"}) == permission.Deny)
	check("trusted allows arbitrary builds",
		et.Check(permission.Request{Tool: "bash", Command: "make release"}) == permission.Allow)
}

func verifyInheritance() {
	section("profile inheritance")

	cfg := config.Config{
		Profiles: map[string]config.Permission{
			"base": {
				Default: config.PermAsk,
				Bash:    map[string]string{"ls*": config.PermAllow, "rm*": config.PermDeny},
			},
			"child": {
				Extends: []string{"base"},
				Bash:    map[string]string{"git*": config.PermAllow},
			},
			"grandchild": {
				Extends: []string{"child"},
				Default: config.PermAllow,
				Bash:    map[string]string{"rm*": config.PermAsk}, // relax the parent
			},
			// Deliberate cycle.
			"loopa": {Extends: []string{"loopb"}, Bash: map[string]string{"a*": config.PermAllow}},
			"loopb": {Extends: []string{"loopa"}, Bash: map[string]string{"b*": config.PermAllow}},
		},
	}

	child, err := config.ResolveProfileByName(cfg, "child")
	check("child resolves", err == nil)
	check("child inherits parent rule", child.Bash["ls*"] == config.PermAllow)
	check("child keeps own rule", child.Bash["git*"] == config.PermAllow)
	check("child inherits default", child.Default == config.PermAsk)

	gc, err := config.ResolveProfileByName(cfg, "grandchild")
	check("grandchild resolves through two levels", err == nil)
	check("grandchild inherits grandparent rule", gc.Bash["ls*"] == config.PermAllow)
	check("grandchild inherits parent rule", gc.Bash["git*"] == config.PermAllow)
	check("grandchild overrides inherited rule",
		gc.Bash["rm*"] == config.PermAsk, gc.Bash["rm*"])
	check("grandchild overrides default", gc.Default == config.PermAllow)

	loop, err := config.ResolveProfileByName(cfg, "loopa")
	check("inheritance cycle does not hang", err == nil && loop != nil)
	check("cycle still collects rules", loop.Bash["a*"] == config.PermAllow)

	// Extending a built-in.
	cfg2 := config.Config{
		Profiles: map[string]config.Permission{
			"strict-ci": {
				Extends: []string{"ci"},
				Bash:    map[string]string{"docker*": config.PermDeny},
			},
		},
	}
	sci, err := config.ResolveProfileByName(cfg2, "strict-ci")
	check("user profile extends a built-in", err == nil)
	check("built-in rules carried over", sci.Bash["go test*"] == config.PermAllow)
	check("user rule added", sci.Bash["docker*"] == config.PermDeny)
	check("built-in sandbox carried over", sci.Sandbox != nil && sci.Sandbox.Enforcement == "os")

	verifySandboxMerge()
}

// verifySandboxMerge covers the precedence bug where adopting a profile's
// sandbox wholesale silently discarded the deny_paths and resource limits the
// user had written in the top-level block.
func verifySandboxMerge() {
	yes, no := true, false

	global := &config.SandboxConfig{
		Mode:       "workspace-write",
		Network:    &yes,
		DenyPaths:  []string{"**/.ssh/**", "**/.aws/**"},
		MemoryMB:   4096,
		CPUSeconds: 600,
	}
	// What a profile typically carries: a mode, and little else.
	profile := &config.SandboxConfig{Mode: "read-only", Network: &no}

	// extends: the hand-written global block should win on mode.
	inherited := config.MergeSandbox(profile, global)
	check("inherited profile keeps user deny_paths", len(inherited.DenyPaths) == 2)
	check("inherited profile keeps user cpu limit", inherited.CPUSeconds == 600)
	check("inherited profile keeps user memory limit", inherited.MemoryMB == 4096)
	check("global block wins mode on extends",
		inherited.Mode == "workspace-write", inherited.Mode)

	// explicit --permission-profile: the chosen profile should win on mode.
	explicit := config.MergeSandbox(global, profile)
	check("explicit profile still keeps user deny_paths", len(explicit.DenyPaths) == 2)
	check("explicit profile still keeps user cpu limit", explicit.CPUSeconds == 600)
	check("explicit profile wins mode", explicit.Mode == "read-only", explicit.Mode)
	check("explicit profile wins network", explicit.Network != nil && !*explicit.Network)

	// Merging with a nil side must not panic or lose data.
	check("merge with nil src keeps everything",
		len(config.MergeSandbox(global, nil).DenyPaths) == 2)
	check("merge with nil dst adopts src",
		config.MergeSandbox(nil, profile).Mode == "read-only")
}

func verifyGranularity(root string) {
	section("permission granularity")

	perm := &config.Permission{
		Default: config.PermAllow,
		Edit:    config.PermAllow,
		Read:    config.PermAllow,
		Paths: map[string]string{
			"**/.env":    config.PermAsk,
			"**/.ssh/**": config.PermDeny,
			"**/*.pem":   config.PermDeny,
		},
		Hosts: map[string]string{
			"*":               config.PermAsk,
			"*.internal.corp": config.PermAllow,
			"evil.com":        config.PermDeny,
		},
		Tools: map[string]string{
			"mcp_*":        config.PermAsk,
			"mcp_readonly": config.PermAllow,
			"dangerous_*":  config.PermDeny,
		},
	}
	e := permission.New(perm, root)

	check("path deny beats coarse allow",
		e.Check(permission.Request{Tool: "edit", Path: filepath.Join(root, ".ssh", "config")}) == permission.Deny)
	check("path ask beats coarse allow",
		e.Check(permission.Request{Tool: "edit", Path: filepath.Join(root, ".env")}) == permission.Ask)
	check("pem files denied",
		e.Check(permission.Request{Tool: "edit", Path: filepath.Join(root, "certs", "key.pem")}) == permission.Deny)
	check("ordinary file allowed",
		e.Check(permission.Request{Tool: "edit", Path: filepath.Join(root, "main.go")}) == permission.Allow)
	check("path deny applies to reads too",
		e.Check(permission.Request{Tool: "read", Path: filepath.Join(root, ".ssh", "id_rsa")}) == permission.Deny)

	check("host wildcard asks",
		e.Check(permission.Request{Tool: "webfetch", Host: "example.com"}) == permission.Ask)
	check("specific host allowed",
		e.Check(permission.Request{Tool: "webfetch", Host: "api.internal.corp"}) == permission.Allow)
	check("denied host blocked",
		e.Check(permission.Request{Tool: "webfetch", Host: "evil.com"}) == permission.Deny)

	check("mcp glob asks",
		e.Check(permission.Request{Tool: "mcp_github"}) == permission.Ask)
	check("exact mcp rule beats glob",
		e.Check(permission.Request{Tool: "mcp_readonly"}) == permission.Allow)
	check("dangerous tools denied",
		e.Check(permission.Request{Tool: "dangerous_exec"}) == permission.Deny)

	d := e.Resolve(permission.Request{Tool: "edit", Path: filepath.Join(root, ".ssh", "x")})
	check("decision reports the matching rule",
		d.Rule == "path:**/.ssh/**", d.Rule)

	// Session grants and yolo.
	req := permission.Request{Tool: "bash", Command: "npm install left-pad"}
	strict := permission.New(&config.Permission{Default: config.PermAsk}, root)
	before := strict.Check(req)
	strict.GrantSession(permission.SessionKey(req))
	after := strict.Check(req)
	check("session grant upgrades ask to allow",
		before == permission.Ask && after == permission.Allow)
	check("session grants are listed", len(strict.SessionGrants()) == 1)
	strict.ClearSessionGrants()
	check("session grants can be cleared",
		strict.Check(req) == permission.Ask && len(strict.SessionGrants()) == 0)

	y := permission.New(&config.Permission{Default: config.PermDeny,
		Bash: map[string]string{"*": config.PermDeny}}, root)
	check("deny before yolo",
		y.Check(permission.Request{Tool: "bash", Command: "rm -rf /"}) == permission.Deny)
	y.SetYolo(true)
	check("yolo overrides every deny",
		y.Check(permission.Request{Tool: "bash", Command: "rm -rf /"}) == permission.Allow)
	check("yolo is reported", y.Yolo())
	y.SetYolo(false)
	check("yolo can be turned back off",
		y.Check(permission.Request{Tool: "bash", Command: "rm -rf /"}) == permission.Deny)

	// Compound commands take the strictest level.
	c := permission.New(&config.Permission{
		Default: config.PermAllow,
		Bash: map[string]string{
			"*": config.PermAllow, "git status*": config.PermAllow, "sudo*": config.PermDeny,
		},
	}, root)
	check("compound command takes the strictest level",
		c.Check(permission.Request{Tool: "bash", Command: "git status && sudo reboot"}) == permission.Deny)

	// Path guard: writes outside the root escalate to ask.
	g := permission.New(&config.Permission{Default: config.PermAllow, Edit: config.PermAllow}, root)
	check("write inside root allowed",
		g.Check(permission.Request{Tool: "edit", Path: filepath.Join(root, "a.go")}) == permission.Allow)
	outside := filepath.Join(filepath.Dir(root), "outside.go")
	check("write outside root escalates to ask",
		g.Check(permission.Request{Tool: "edit", Path: outside}) == permission.Ask)
}

func verifySandboxPolicy(root string) {
	section("sandbox policy")

	// Use a nested workspace: root itself sits in TempDir, which is always
	// writable, so a parent-escape check against it would be meaningless.
	nested := filepath.Join(root, "proj")
	_ = os.MkdirAll(nested, 0o755)

	def := sandbox.Default().Normalize(nested)
	check("default mode is workspace-write", def.Mode == sandbox.ModeWorkspace)
	check("default allows network", def.Network)
	check("default has memory limit", def.Limits.MemoryMB > 0)
	check("workspace is writable", def.Writable(filepath.Join(nested, "x.go")))
	// A sibling under TempDir stays writable on purpose — builds need scratch
	// space — so the meaningful escape checks are against real user paths.
	check("home dir is not writable", !def.Writable(mustHome("notes.txt")))
	check("system dir is not writable", !def.Writable("C:/Windows/system32/x.dll"))
	check("arbitrary drive path is not writable", !def.Writable("D:/data/x.txt"))
	check("temp dir is writable", def.Writable(filepath.Join(os.TempDir(), "x.go")))
	check("everything is readable by default", def.Readable("C:/Windows/notepad.exe"))

	ro := sandbox.Policy{Mode: sandbox.ModeReadOnly}.Normalize(nested)
	check("read-only forces network off", !ro.Network)
	check("read-only forbids all writes", !ro.WritesAllowed())
	check("read-only rejects workspace writes", !ro.Writable(filepath.Join(nested, "x.go")))

	off := sandbox.Off().Normalize(nested)
	check("off is unconfined", !off.Confined())
	check("off permits any write", off.Writable("C:/Windows/system32/x.dll"))

	for _, in := range []string{"read-only", "readonly", "ro", "workspace", "ws", "trusted", "off"} {
		_, ok := sandbox.ParseMode(in)
		check("ParseMode accepts "+in, ok)
	}
	_, ok := sandbox.ParseMode("banana")
	check("ParseMode rejects nonsense", !ok)

	// deny_paths must win over a writable root.
	dp := sandbox.Policy{
		Mode:      sandbox.ModeWorkspace,
		DenyPaths: []string{"**/secrets/**"},
	}.Normalize(root)
	check("deny_paths blocks a workspace subdir",
		!dp.Writable(filepath.Join(root, "secrets", "k.txt")))
	check("deny_paths leaves siblings writable",
		dp.Writable(filepath.Join(root, "src", "k.txt")))

	// Config round-trip.
	yes := true
	cfg := &config.SandboxConfig{
		Mode: "read-only", Enforcement: "os", Network: &yes,
		MemoryMB: 512, CPUSeconds: 60,
	}
	p := sandbox.FromConfig(cfg, root)
	check("FromConfig applies mode", p.Mode == sandbox.ModeReadOnly)
	check("FromConfig applies enforcement", p.Enforcement == sandbox.EnforceOS)
	check("read-only overrides configured network", !p.Network)
	check("FromConfig applies limits", p.Limits.MemoryMB == 512 && p.Limits.CPUSeconds == 60)
	back := sandbox.ToConfig(p)
	check("ToConfig round-trips mode", back.Mode == "read-only")
	check("ToConfig round-trips limits", back.MemoryMB == 512)

	// Holder swaps.
	h := sandbox.NewHolder(def)
	check("holder returns its policy", h.Policy().Mode == sandbox.ModeWorkspace)
	h.SetMode(sandbox.ModeReadOnly)
	check("holder swaps mode", h.Policy().Mode == sandbox.ModeReadOnly)
	check("holder keeps workspace across swaps", h.Policy().Workspace == def.Workspace)
	h.SetMode(sandbox.ModeWorkspace)
	h.SetNetwork(false)
	check("holder toggles network", !h.Policy().Network)

	var nilHolder *sandbox.Holder
	check("nil holder is safe", !nilHolder.Policy().Confined())
}

func verifyAnalysis(root string) {
	section("static command analysis")

	ws := sandbox.Default().Normalize(root)
	ro := sandbox.Policy{Mode: sandbox.ModeReadOnly}.Normalize(root)
	nonet := sandbox.Policy{Mode: sandbox.ModeWorkspace, Network: false}.Normalize(root)
	off := sandbox.Off().Normalize(root)

	has := func(p sandbox.Policy, cmd string) bool {
		return len(sandbox.Violations(p, cmd)) > 0
	}
	rule := func(p sandbox.Policy, cmd string) string {
		v := sandbox.Violations(p, cmd)
		if len(v) == 0 {
			return ""
		}
		return v[0].Rule
	}

	check("plain echo is clean", !has(ws, "echo hello"))
	check("workspace write is clean", !has(ws, "echo x > out.txt"))
	check("build command is clean", !has(ws, "go build ./..."))
	check("piped read is clean", !has(ws, "cat go.mod | grep module"))

	check("sudo blocked", rule(ws, "sudo rm -rf /") == "privilege.escalation")
	check("su blocked", rule(ws, "su root") == "privilege.escalation")
	check("raw device write blocked", rule(ws, "dd if=/dev/zero of=/dev/sda") == "write.device")
	check("system dir write blocked",
		rule(ws, "echo x > C:/Windows/system32/drivers/etc/hosts") == "write.system")
	check("escape write blocked", rule(ws, "echo x > ../../escape.txt") == "write.outside")

	check("read-only blocks writes", rule(ro, "echo x > f.txt") == "write.readonly")
	check("read-only blocks rm", rule(ro, "rm f.txt") == "write.readonly")
	check("read-only permits reads", !has(ro, "cat f.txt"))
	check("read-only blocks curl", rule(ro, "curl https://x.com") == "network.denied")

	check("no-network blocks curl", rule(nonet, "curl https://x.com") == "network.denied")
	check("no-network blocks wget", rule(nonet, "wget https://x.com") == "network.denied")
	check("no-network blocks npm install", rule(nonet, "npm install") == "network.denied")
	check("no-network blocks git clone", rule(nonet, "git clone https://x.com/r") == "network.denied")
	check("no-network permits git status", !has(nonet, "git status"))
	check("no-network permits npm test", !has(nonet, "npm test"))
	check("no-network permits local build", !has(nonet, "go build ./..."))

	check("null sink is not a write", !has(ws, "some-cmd > /dev/null"))
	check("windows nul sink is not a write", !has(ws, "some-cmd > nul"))
	check("null sink allowed in read-only", !has(ro, "make 2> /dev/null"))

	check("compound catches the bad half", has(ws, "echo ok && sudo reboot"))
	check("pipeline catches the bad half", has(nonet, "echo hi | curl -X POST https://x.com"))
	check("quoted separator is not split", !has(ws, `echo "a && b"`))

	check("sandbox off permits everything", !has(off, "sudo rm -rf / && curl evil.com"))

	// Host allow/deny lists.
	hosts := sandbox.Policy{
		Mode: sandbox.ModeWorkspace, Network: true,
		AllowHosts: []string{"*.github.com", "proxy.golang.org"},
	}.Normalize(root)
	check("allowed host passes", !has(hosts, "curl https://api.github.com/repos"))
	check("unlisted host blocked", rule(hosts, "curl https://evil.com/x") == "network.host")

	denyHosts := sandbox.Policy{
		Mode: sandbox.ModeWorkspace, Network: true,
		DenyHosts: []string{"*.evil.com"},
	}.Normalize(root)
	check("denied host blocked", rule(denyHosts, "curl https://a.evil.com") == "network.host")
	check("other hosts fine with deny list", !has(denyHosts, "curl https://good.com"))
}

func verifyEnviron() {
	section("environment scrubbing")

	os.Setenv("ANTHROPIC_API_KEY", "sk-secret")
	os.Setenv("AWS_SECRET_ACCESS_KEY", "aws-secret")
	os.Setenv("MY_APP_TOKEN", "tok")
	os.Setenv("HARMLESS_SETTING", "value")
	defer func() {
		for _, k := range []string{"ANTHROPIC_API_KEY", "AWS_SECRET_ACCESS_KEY", "MY_APP_TOKEN", "HARMLESS_SETTING"} {
			os.Unsetenv(k)
		}
	}()

	root, _ := os.MkdirTemp("", "envcheck")
	defer os.RemoveAll(root)
	p := sandbox.Default().Normalize(root)
	env := sandbox.Environ(p)

	seen := map[string]string{}
	for _, kv := range env {
		if k, v, ok := splitKV(kv); ok {
			seen[k] = v
		}
	}

	check("provider key stripped", seen["ANTHROPIC_API_KEY"] == "")
	check("aws secret stripped", seen["AWS_SECRET_ACCESS_KEY"] == "")
	check("generic token stripped", seen["MY_APP_TOKEN"] == "")
	check("harmless var kept", seen["HARMLESS_SETTING"] == "value")
	check("PATH preserved", seen["PATH"] != "")
	check("sandbox marker set", seen["RICK_SANDBOX"] == string(p.Mode))
	check("pager disabled", seen["GIT_PAGER"] == "cat")

	nonet := sandbox.Policy{Mode: sandbox.ModeWorkspace, Network: false}.Normalize(root)
	nenv := map[string]string{}
	for _, kv := range sandbox.Environ(nonet) {
		if k, v, ok := splitKV(kv); ok {
			nenv[k] = v
		}
	}
	check("proxy blackholed when offline", nenv["HTTPS_PROXY"] == "http://127.0.0.1:9")
	check("goproxy off when offline", nenv["GOPROXY"] == "off")
	check("pip offline when offline", nenv["PIP_NO_INDEX"] == "1")

	trusted := sandbox.Policy{Mode: sandbox.ModeTrusted}.Normalize(root)
	tenv := map[string]string{}
	for _, kv := range sandbox.Environ(trusted) {
		if k, v, ok := splitKV(kv); ok {
			tenv[k] = v
		}
	}
	check("trusted mode keeps credentials", tenv["ANTHROPIC_API_KEY"] == "sk-secret")

	keep := sandbox.Policy{Mode: sandbox.ModeWorkspace, KeepCredentials: true}.Normalize(root)
	kenv := map[string]string{}
	for _, kv := range sandbox.Environ(keep) {
		if k, v, ok := splitKV(kv); ok {
			kenv[k] = v
		}
	}
	check("keep_credentials opts out of scrubbing", kenv["ANTHROPIC_API_KEY"] == "sk-secret")

	allow := sandbox.Policy{
		Mode: sandbox.ModeWorkspace, AllowEnv: []string{"MY_APP_TOKEN"},
	}.Normalize(root)
	aenv := map[string]string{}
	for _, kv := range sandbox.Environ(allow) {
		if k, v, ok := splitKV(kv); ok {
			aenv[k] = v
		}
	}
	check("allow_env rescues one variable", aenv["MY_APP_TOKEN"] == "tok")
	check("allow_env does not rescue the rest", aenv["ANTHROPIC_API_KEY"] == "")

	deny := sandbox.Policy{
		Mode: sandbox.ModeWorkspace, DenyEnv: []string{"HARMLESS_*"},
	}.Normalize(root)
	denv := map[string]string{}
	for _, kv := range sandbox.Environ(deny) {
		if k, v, ok := splitKV(kv); ok {
			denv[k] = v
		}
	}
	check("deny_env strips an otherwise-kept var", denv["HARMLESS_SETTING"] == "")
}

func splitKV(kv string) (string, string, bool) {
	for i := 0; i < len(kv); i++ {
		if kv[i] == '=' {
			return kv[:i], kv[i+1:], true
		}
	}
	return "", "", false
}

// mustHome builds a path inside the user's home directory, which must never be
// writable under workspace confinement.
func mustHome(name string) string {
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join("C:/Users/nobody", name)
	}
	return filepath.Join(home, name)
}
