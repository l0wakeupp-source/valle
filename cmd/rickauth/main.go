// Command rickauth verifies the /auth provider flow: protocol autodetection
// against real HTTP servers of both flavors, credential persistence, and the
// TUI state machine driven headlessly.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"rick/internal/config"
	"rick/internal/permission"
	"rick/internal/provider"
	"rick/internal/provider/catalog"
	"rick/internal/session"
	"rick/internal/theme"
	"rick/internal/tools"
	"rick/internal/tui"
)

var pass, fail int
var failures []string

func check(name string, cond bool, detail ...string) {
	if cond {
		pass++
		fmt.Printf("  PASS  %s\n", name)
		return
	}
	fail++
	msg := name
	if len(detail) > 0 {
		msg += " — " + strings.Join(detail, " ")
	}
	failures = append(failures, msg)
	fmt.Printf("  FAIL  %s\n", msg)
}

func section(s string) { fmt.Printf("\n== %s ==\n", s) }

// openAIServer speaks the OpenAI /models shape and requires Bearer auth.
func openAIServer(wantKey string) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/models") {
			http.NotFound(w, r)
			return
		}
		if wantKey != "" && r.Header.Get("Authorization") != "Bearer "+wantKey {
			w.WriteHeader(401)
			fmt.Fprint(w, `{"error":{"message":"invalid api key"}}`)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"object":"list","data":[
			{"id":"gpt-5","object":"model","context_length":400000},
			{"id":"gpt-5-mini","object":"model","context_length":400000},
			{"id":"o4-mini","object":"model"}]}`)
	}))
}

// anthropicServer speaks the Anthropic /models shape and requires x-api-key.
func anthropicServer(wantKey string) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/models") {
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("x-api-key") != wantKey {
			w.WriteHeader(401)
			fmt.Fprint(w, `{"type":"error","error":{"message":"invalid x-api-key"}}`)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"data":[
			{"type":"model","id":"claude-sonnet-4-5","display_name":"Claude Sonnet 4.5"},
			{"type":"model","id":"claude-opus-4-5","display_name":"Claude Opus 4.5"}]}`)
	}))
}

// versionlessServer only answers at /v1/models, so the probe must retry with
// the suffix appended.
func versionlessServer() *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"object":"list","data":[{"id":"local-model","object":"model"}]}`)
	}))
}

func main() {
	termenv.SetDefaultOutput(termenv.NewOutput(os.Stdout, termenv.WithProfile(termenv.TrueColor)))
	lipgloss.SetColorProfile(termenv.TrueColor)

	tmp, err := os.MkdirTemp("", "rick-auth-")
	if err != nil {
		panic(err)
	}
	defer os.RemoveAll(tmp)
	// Isolate the credential store from the real one.
	os.Setenv("RICK_HOME", filepath.Join(tmp, "cfg"))
	os.Setenv("RICK_DATA", filepath.Join(tmp, "data"))
	for _, v := range []string{"ANTHROPIC_API_KEY", "OPENAI_API_KEY", "OPENROUTER_API_KEY",
		"GEMINI_API_KEY", "GOOGLE_API_KEY", "GROQ_API_KEY", "DEEPSEEK_API_KEY", "XAI_API_KEY"} {
		os.Unsetenv(v)
	}

	testCatalog()
	testProbe()
	testCredentials()
	testRealWorldEndpoints()
	testAuthUI(tmp)
	testPartialUI(tmp)
	testURLEntry(tmp)
	testDirtyKeys(tmp)
	testGatewayPrefix(tmp)

	fmt.Printf("\n%d passed, %d failed\n", pass, fail)
	if fail > 0 {
		fmt.Println("\nfailures:")
		for _, f := range failures {
			fmt.Println("  - " + f)
		}
		os.Exit(1)
	}
}

func testCatalog() {
	section("provider catalog")

	check("catalog is populated", len(catalog.Registry) >= 25, fmt.Sprint(len(catalog.Registry)))

	must := []string{"anthropic", "openai", "openrouter", "nous", "zai", "deepseek",
		"xai", "gemini", "groq", "kimi", "alibaba", "ollama", "lmstudio"}
	missing := []string{}
	for _, id := range must {
		if _, ok := catalog.Get(id); !ok {
			missing = append(missing, id)
		}
	}
	check("all headline providers present", len(missing) == 0, fmt.Sprint(missing))

	// Ordering: the big three lead the list.
	check("anthropic is first", catalog.Registry[0].ID == "anthropic", catalog.Registry[0].ID)
	check("openai is second", catalog.Registry[1].ID == "openai", catalog.Registry[1].ID)

	bad := []string{}
	ids := map[string]bool{}
	for _, e := range catalog.Registry {
		if ids[e.ID] {
			bad = append(bad, "duplicate:"+e.ID)
		}
		ids[e.ID] = true
		if e.Name == "" || e.Auth == "" || e.Flavor == "" {
			bad = append(bad, "incomplete:"+e.ID)
		}
		if e.Auth == catalog.AuthAPIKey && e.BaseURL == "" {
			bad = append(bad, "nourl:"+e.ID)
		}
		if e.Flavor != catalog.FlavorOpenAI && e.Flavor != catalog.FlavorAnthropic {
			bad = append(bad, "badflavor:"+e.ID)
		}
	}
	check("every entry is well formed", len(bad) == 0, fmt.Sprint(bad))

	a, _ := catalog.Get("anthropic")
	check("anthropic uses the anthropic protocol", a.Flavor == catalog.FlavorAnthropic)
	check("anthropic needs a key", a.NeedsKey())

	o, _ := catalog.Get("ollama")
	check("ollama needs no key", !o.NeedsKey(), o.Auth)

	n, _ := catalog.Get("nous")
	check("nous is an oauth provider", n.Auth == catalog.AuthDeviceCode, n.Auth)

	mm, _ := catalog.Get("minimax")
	check("minimax is detected as anthropic-flavored", mm.Flavor == catalog.FlavorAnthropic)

	// Env var pickup.
	os.Setenv("GLM_API_KEY", "glm-from-env")
	z, _ := catalog.Get("zai")
	key, name := z.EnvKey()
	check("env var is picked up", key == "glm-from-env" && name == "GLM_API_KEY", key+" "+name)
	os.Unsetenv("GLM_API_KEY")
	key2, _ := z.EnvKey()
	check("no env var means no key", key2 == "", key2)
}

func testProbe() {
	section("endpoint probing & protocol autodetection")

	ctx := context.Background()

	// OpenAI-shaped.
	oai := openAIServer("sk-test")
	defer oai.Close()
	res := catalog.Probe(ctx, oai.URL, "sk-test")
	check("openai endpoint probes clean", res.Err == nil, fmt.Sprint(res.Err))
	check("openai protocol detected", res.Flavor == catalog.FlavorOpenAI, res.Flavor)
	check("openai models fetched", len(res.Models) == 3, fmt.Sprint(len(res.Models)))
	if len(res.Models) == 3 {
		check("model ids parsed", res.Models[0].ID == "gpt-5", res.Models[0].ID)
		check("context length parsed", res.Models[0].Context == 400000, fmt.Sprint(res.Models[0].Context))
	}

	// Anthropic-shaped — same probe call, no hints.
	ant := anthropicServer("sk-ant-test")
	defer ant.Close()
	res2 := catalog.Probe(ctx, ant.URL, "sk-ant-test")
	check("anthropic endpoint probes clean", res2.Err == nil, fmt.Sprint(res2.Err))
	check("ANTHROPIC PROTOCOL AUTODETECTED", res2.Flavor == catalog.FlavorAnthropic, res2.Flavor)
	check("anthropic models fetched", len(res2.Models) == 2, fmt.Sprint(len(res2.Models)))
	if len(res2.Models) == 2 {
		check("display_name used as the model name",
			res2.Models[0].Name == "Claude Opus 4.5" || res2.Models[0].Name == "Claude Sonnet 4.5",
			res2.Models[0].Name)
	}

	// Wrong key must fail, not silently succeed.
	res3 := catalog.Probe(ctx, oai.URL, "sk-wrong")
	check("bad key is rejected", res3.Err != nil, "probe unexpectedly succeeded")
	if res3.Err != nil {
		check("auth error is surfaced", strings.Contains(res3.Err.Error(), "401"), res3.Err.Error())
	}

	// Missing /v1 must be repaired automatically.
	vl := versionlessServer()
	defer vl.Close()
	res4 := catalog.Probe(ctx, vl.URL, "")
	check("missing /v1 suffix is auto-corrected", res4.Err == nil, fmt.Sprint(res4.Err))
	check("corrected URL is returned", strings.HasSuffix(res4.BaseURL, "/v1"), res4.BaseURL)

	// Dead endpoint.
	res5 := catalog.Probe(ctx, "http://127.0.0.1:9/nothing", "k")
	check("unreachable endpoint reports an error", res5.Err != nil)

	res6 := catalog.Probe(ctx, "", "k")
	check("empty URL is rejected", res6.Err != nil)
}

func testCredentials() {
	section("credential store")

	creds, err := config.LoadCredentials()
	check("empty store loads cleanly", err == nil && len(creds.Providers) == 0, fmt.Sprint(err))

	creds.Set("myprovider", config.Credential{
		Type: "openai", APIKey: "sk-secret", BaseURL: "https://api.example.com/v1",
		Label: "My Provider", Models: []string{"model-a", "model-b"}, Custom: true,
	})
	check("save succeeds", creds.Save() == nil)

	reloaded, err := config.LoadCredentials()
	check("store round-trips", err == nil && len(reloaded.Providers) == 1, fmt.Sprint(err))
	got := reloaded.Providers["myprovider"]
	check("api key persisted", got.APIKey == "sk-secret", got.APIKey)
	check("base url persisted", got.BaseURL == "https://api.example.com/v1", got.BaseURL)
	check("models persisted", len(got.Models) == 2, fmt.Sprint(len(got.Models)))
	check("custom flag persisted", got.Custom)

	// File permissions must be owner-only where the OS supports it.
	if st, err := os.Stat(config.AuthPath()); err == nil {
		mode := st.Mode().Perm()
		check("auth file is not world-readable", mode&0o077 == 0 || os.Getenv("OS") == "Windows_NT",
			fmt.Sprintf("%o", mode))
	}

	// Merge into a config: existing pins must win.
	cfg, _ := config.Defaults()
	cfg.Providers = map[string]config.Provider{
		"myprovider": {BaseURL: "https://pinned.example.com/v1"},
	}
	config.MergeCredentials(&cfg, reloaded)
	check("credential fills in a missing key",
		cfg.Providers["myprovider"].APIKey == "sk-secret", cfg.Providers["myprovider"].APIKey)
	check("project config pin is NOT overridden",
		cfg.Providers["myprovider"].BaseURL == "https://pinned.example.com/v1",
		cfg.Providers["myprovider"].BaseURL)

	reloaded.Set("withdefault", config.Credential{Models: []string{"m1", "m2"}, Default: "m2"})
	check("preferred model wins over first",
		config.FirstConfiguredModel(reloaded, "withdefault") == "withdefault/m2",
		config.FirstConfiguredModel(reloaded, "withdefault"))
	check("first model used when no default",
		config.FirstConfiguredModel(reloaded, "myprovider") == "myprovider/model-a",
		config.FirstConfiguredModel(reloaded, "myprovider"))

	reloaded.Remove("myprovider")
	reloaded.Remove("withdefault")
	_ = reloaded.Save()
	after, _ := config.LoadCredentials()
	check("removal persists", len(after.Providers) == 0, fmt.Sprint(len(after.Providers)))
}

// ---- TUI driving ----

// send drives Update and, like the real event loop, executes the commands it
// returns. tea.Batch produces a tea.BatchMsg holding further Cmds, so those
// are run too — otherwise async work (the /auth probe) never happens.
//
// spinnerTickMsg is dropped instead of being fed back: it reschedules itself
// forever and would hang the harness.
func send(m *tui.Model, msgs ...tea.Msg) *tui.Model {
	for _, msg := range msgs {
		nm, cmd := m.Update(msg)
		m = nm.(*tui.Model)
		m = runCmd(m, cmd, 0)
	}
	return m
}

func runCmd(m *tui.Model, cmd tea.Cmd, depth int) *tui.Model {
	if cmd == nil || depth > 4 {
		return m
	}
	out := cmd()
	if out == nil {
		return m
	}
	if batch, ok := out.(tea.BatchMsg); ok {
		for _, c := range batch {
			m = runCmd(m, c, depth+1)
		}
		return m
	}
	if isTick(out) {
		return m
	}
	nm, next := m.Update(out)
	m = nm.(*tui.Model)
	return runCmd(m, next, depth+1)
}

// isTick reports whether a message is a self-rescheduling timer.
func isTick(msg tea.Msg) bool {
	return strings.Contains(fmt.Sprintf("%T", msg), "TickMsg") ||
		strings.Contains(fmt.Sprintf("%T", msg), "readAgentMsg")
}

func typeStr(m *tui.Model, s string) *tui.Model {
	for _, r := range s {
		m = send(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	return m
}

func key(m *tui.Model, k string) *tui.Model {
	var msg tea.KeyMsg
	switch k {
	case "enter":
		msg = tea.KeyMsg{Type: tea.KeyEnter}
	case "esc":
		msg = tea.KeyMsg{Type: tea.KeyEsc}
	case "down":
		msg = tea.KeyMsg{Type: tea.KeyDown}
	case "up":
		msg = tea.KeyMsg{Type: tea.KeyUp}
	case "backspace":
		msg = tea.KeyMsg{Type: tea.KeyBackspace}
	default:
		msg = tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(k)}
	}
	return send(m, msg)
}

func newModel(tmp string) *tui.Model {
	cwd := filepath.Join(tmp, "proj")
	_ = os.MkdirAll(cwd, 0o755)
	loaded, _ := config.Load(cwd)
	themes := theme.Load()
	todos := tools.NewTodoStore()
	reg := tools.NewRegistry()
	reg.Register(tools.ReadTool{})
	store, _ := session.NewStore(filepath.Join(tmp, "sess"))
	snaps, _ := session.NewSnapshotter(cwd, filepath.Join(tmp, "snap"))

	m := tui.New(tui.Deps{
		Loaded: loaded, Themes: themes, Registry: reg, Todos: todos,
		Perms: permission.New(loaded.Config.Permission, cwd),
		Store: store, Snapshots: snaps,
		Providers: map[string]provider.Provider{},
		Cwd:       cwd, Version: "vtest",
	})
	return send(m, tea.WindowSizeMsg{Width: 110, Height: 36})
}

func testAuthUI(tmp string) {
	section("/auth flow in the TUI")

	oai := openAIServer("sk-live")
	defer oai.Close()
	ant := anthropicServer("sk-ant-live")
	defer ant.Close()

	m := newModel(tmp)

	// /models with nothing configured must redirect into /auth.
	m.InputSetValue("/models")
	m = key(m, "enter")
	check("/models with no providers opens /auth", m.AuthActive(), "auth not opened")
	m = key(m, "esc")
	check("esc closes /auth", !m.AuthActive())

	// Open it directly.
	m.InputSetValue("/auth")
	m = key(m, "enter")
	check("/auth opens the provider list", m.AuthActive())

	v := m.View()
	check("list shows numbered rows", strings.Contains(v, " 1 ") && strings.Contains(v, " 2 "), "no numbers")
	check("list shows Anthropic", strings.Contains(v, "Anthropic"))
	check("list shows OpenRouter", strings.Contains(v, "OpenRouter"))
	check("list offers the add option", strings.Contains(v, "add a custom provider"))
	check("nothing is connected yet", !strings.Contains(v, "✓"), "unexpected checkmark")
	check("row count matches the catalog", m.AuthRowCount() >= 25, fmt.Sprint(m.AuthRowCount()))

	// --- add a custom OpenAI-compatible provider ---
	m = typeStr(m, "add")
	m = key(m, "enter")
	check("\"add\" enters the custom flow", m.AuthStageName() == "add-name", m.AuthStageName())
	check("prompt asks for a name", strings.Contains(m.View(), "Name for this provider"))

	m = typeStr(m, "My Gateway")
	m = key(m, "enter")
	check("name advances to the URL prompt", m.AuthStageName() == "add-url", m.AuthStageName())

	// Reject a malformed URL.
	m = typeStr(m, "not-a-url")
	m = key(m, "enter")
	check("malformed URL is rejected", m.AuthStageName() == "add-url", m.AuthStageName())
	check("URL error is explained", strings.Contains(m.AuthStatus(), "not a valid host"), m.AuthStatus())
	m.AuthClearInput()

	m = typeStr(m, oai.URL)
	m = key(m, "enter")
	check("valid URL advances to the key prompt", m.AuthStageName() == "add-key", m.AuthStageName())

	m = typeStr(m, "sk-live")
	m = key(m, "enter")
	// The probe runs as a tea.Cmd; send() already fed the result back.
	check("PROBE SUCCEEDED AND PICKED THE MODEL STAGE",
		m.AuthStageName() == "pick-model", m.AuthStageName()+" | "+m.AuthStatus())
	check("models were fetched", m.AuthModelCount() == 3, fmt.Sprint(m.AuthModelCount()))
	check("openai protocol was detected", strings.Contains(m.AuthStatus(), "openai"), m.AuthStatus())

	// Choose a model.
	m = key(m, "down")
	m = key(m, "enter")
	check("model selection returns to the list", m.AuthStageName() == "list", m.AuthStageName())
	check("ACTIVE MODEL WAS SET", strings.HasPrefix(m.ModelID(), "my-gateway/"), m.ModelID())
	check("chosen model is the highlighted one", strings.HasSuffix(m.ModelID(), "gpt-5-mini"), m.ModelID())

	v2 := m.View()
	check("connected provider shows a green check", strings.Contains(v2, "✓"), "no checkmark")
	check("connected provider is listed by name",
		strings.Contains(v2, "My Gateway") || strings.Contains(v2, "my-gateway"), v2[:0])
	check("PROVIDER IS NOW LIVE", m.ProviderCount() == 1, fmt.Sprint(m.ProviderCount()))

	// Persisted to disk?
	saved, _ := config.LoadCredentials()
	cred, ok := saved.Providers["my-gateway"]
	check("credential written to auth.json", ok)
	if ok {
		check("saved with the detected protocol", cred.Type == "openai", cred.Type)
		check("saved with the key", cred.APIKey == "sk-live", cred.APIKey)
		check("saved with the model list", len(cred.Models) == 3, fmt.Sprint(len(cred.Models)))
		check("saved with the chosen default", cred.Default == "gpt-5-mini", cred.Default)
		check("marked as custom", cred.Custom)
	}

	// --- editing an existing provider by number ---
	m.InputSetValue("")
	m = typeStr(m, "1")
	m = key(m, "enter")
	check("selecting a connected provider opens the edit menu",
		m.AuthStageName() == "edit", m.AuthStageName())
	ev := m.View()
	check("edit view shows the endpoint", strings.Contains(ev, "endpoint"))
	check("edit view masks the key", strings.Contains(ev, "•"), "key not masked")
	check("edit view offers all five actions",
		strings.Contains(ev, "change API key") && strings.Contains(ev, "remove this provider"))

	// Change the key (option 1) to a bad one and confirm it fails loudly.
	m = key(m, "1")
	check("option 1 opens the key prompt", m.AuthStageName() == "key", m.AuthStageName())
	m = typeStr(m, "sk-wrong")
	m = key(m, "enter")
	check("a bad key is rejected, not saved", m.AuthStageName() == "key", m.AuthStageName())
	check("failure is explained", strings.Contains(m.AuthStatus(), "401"), m.AuthStatus())
	still, _ := config.LoadCredentials()
	check("bad key did NOT overwrite the good one",
		still.Providers["my-gateway"].APIKey == "sk-live", still.Providers["my-gateway"].APIKey)

	m = key(m, "esc")
	m = key(m, "esc")

	// --- add an Anthropic-flavored provider; protocol must autodetect ---
	m = typeStr(m, "add")
	m = key(m, "enter")
	m = typeStr(m, "claude-proxy")
	m = key(m, "enter")
	m = typeStr(m, ant.URL)
	m = key(m, "enter")
	m = typeStr(m, "sk-ant-live")
	m = key(m, "enter")
	check("anthropic-flavored endpoint connects", m.AuthStageName() == "pick-model",
		m.AuthStageName()+" | "+m.AuthStatus())
	check("ANTHROPIC PROTOCOL AUTODETECTED IN THE UI",
		strings.Contains(m.AuthStatus(), "anthropic"), m.AuthStatus())
	check("anthropic models fetched", m.AuthModelCount() == 2, fmt.Sprint(m.AuthModelCount()))
	m = key(m, "enter")

	check("two providers are now live", m.ProviderCount() == 2, fmt.Sprint(m.ProviderCount()))
	saved2, _ := config.LoadCredentials()
	check("both credentials persisted", len(saved2.Providers) == 2, fmt.Sprint(len(saved2.Providers)))
	check("anthropic flavor stored", saved2.Providers["claude-proxy"].Type == "anthropic",
		saved2.Providers["claude-proxy"].Type)

	// --- /models now lists the fetched models instead of redirecting ---
	m = key(m, "esc")
	check("auth closed", !m.AuthActive())
	m.InputSetValue("/models")
	m = key(m, "enter")
	// /models now prints a provider list into the conversation.
	check("/models lists providers inline", !m.AuthActive() && m.PendingKind() != 0,
		fmt.Sprint(m.PendingKind()))
	check("both connected providers are offered", m.PendingCount() >= 2,
		fmt.Sprint(m.PendingCount()))
	m.InputSetValue("")
	m = key(m, "enter")

	// --- removal ---
	m.InputSetValue("/auth")
	m = key(m, "enter")
	m = typeStr(m, "claude-proxy")
	m = key(m, "enter")
	check("selecting by name works", m.AuthStageName() == "edit", m.AuthStageName())
	m = key(m, "5")
	check("removal returns to the list", m.AuthStageName() == "list", m.AuthStageName())
	saved3, _ := config.LoadCredentials()
	check("PROVIDER REMOVED FROM DISK", len(saved3.Providers) == 1, fmt.Sprint(len(saved3.Providers)))
	check("removed provider is no longer live", m.ProviderCount() == 1, fmt.Sprint(m.ProviderCount()))

	// --- bad input handling ---
	m = typeStr(m, "999")
	m = key(m, "enter")
	check("out-of-range number is rejected", strings.Contains(m.AuthStatus(), "no provider matches"),
		m.AuthStatus())
	m = typeStr(m, "nonsense-provider")
	m = key(m, "enter")
	check("unknown name is rejected", strings.Contains(m.AuthStatus(), "no provider matches"),
		m.AuthStatus())

	// --- an oauth provider explains itself instead of silently failing ---
	m.AuthReset()
	m = typeStr(m, "nous")
	m = key(m, "enter")
	check("oauth provider shows the sign-in explainer",
		m.AuthStageName() == "device-code", m.AuthStageName())
	check("explainer is honest about the limitation",
		strings.Contains(m.View(), "not wired up yet"), "no honest note")

	// --- env-var-backed provider is shown as connected ---
	os.Setenv("DEEPSEEK_API_KEY", "sk-env-deepseek")
	m.AuthReset()
	dv := m.View()
	check("env-var provider is marked connected", strings.Contains(dv, "from $DEEPSEEK_API_KEY"), "not shown")
	os.Unsetenv("DEEPSEEK_API_KEY")

	_ = json.Marshal
}

// testRealWorldEndpoints covers the endpoint shapes that broke detection in
// practice: gateways with no /models, non-standard payload envelopes, and
// users pasting a full endpoint URL instead of a base.
func testRealWorldEndpoints() {
	section("real-world endpoint shapes")

	ctx := context.Background()

	// 1. Anthropic-compatible gateway that only implements /messages.
	antNoModels := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/messages") {
			if r.Header.Get("x-api-key") == "" {
				w.WriteHeader(401)
				return
			}
			w.WriteHeader(400) // our probe body names a fake model
			fmt.Fprint(w, `{"type":"error","error":{"message":"model not found"}}`)
			return
		}
		w.WriteHeader(404)
	}))
	defer antNoModels.Close()

	r1 := catalog.Probe(ctx, antNoModels.URL+"/anthropic", "k")
	check("anthropic gateway without /models still connects", r1.Err == nil, fmt.Sprint(r1.Err))
	check("  protocol identified from the auth challenge", r1.Flavor == catalog.FlavorAnthropic, r1.Flavor)
	check("  result is marked partial", r1.Partial)

	// 2. OpenAI gateway that only implements /chat/completions.
	oaiNoModels := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/chat/completions") {
			w.WriteHeader(400)
			fmt.Fprint(w, `{"error":{"message":"unknown model"}}`)
			return
		}
		w.WriteHeader(404)
	}))
	defer oaiNoModels.Close()

	r2 := catalog.Probe(ctx, oaiNoModels.URL+"/v1", "k")
	check("openai gateway without /models still connects", r2.Err == nil, fmt.Sprint(r2.Err))
	check("  protocol identified as openai", r2.Flavor == catalog.FlavorOpenAI, r2.Flavor)
	check("  result is marked partial", r2.Partial)

	// 3. Model list under a "models" key rather than "data".
	altEnvelope := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"models":[{"id":"m1"},{"id":"m2"},{"name":"m3"}]}`)
	}))
	defer altEnvelope.Close()
	r3 := catalog.Probe(ctx, altEnvelope.URL+"/v1", "k")
	check("models under a \"models\" key are parsed", r3.Err == nil, fmt.Sprint(r3.Err))
	check("  all three entries parsed", len(r3.Models) == 3, fmt.Sprint(len(r3.Models)))
	check("  name is used when id is absent",
		len(r3.Models) == 3 && r3.Models[2].ID == "m3", fmt.Sprint(r3.Models))

	// 4. Bare JSON array.
	bareArray := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `[{"id":"solo","object":"model"}]`)
	}))
	defer bareArray.Close()
	r4 := catalog.Probe(ctx, bareArray.URL+"/v1", "k")
	check("bare array model list is parsed", r4.Err == nil && len(r4.Models) == 1, fmt.Sprint(r4.Err))

	// 5. Anthropic payload without display_name.
	noDisplay := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("x-api-key") == "" {
			w.WriteHeader(401)
			fmt.Fprint(w, `{"error":"need x-api-key"}`)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"data":[{"type":"model","id":"claude-3-5-sonnet"}]}`)
	}))
	defer noDisplay.Close()
	r5 := catalog.Probe(ctx, noDisplay.URL+"/anthropic", "k")
	check("anthropic without display_name is detected", r5.Err == nil, fmt.Sprint(r5.Err))
	check("  flavor is anthropic", r5.Flavor == catalog.FlavorAnthropic, r5.Flavor)
	check("  id falls back to the model name", len(r5.Models) == 1 && r5.Models[0].Name == "claude-3-5-sonnet",
		fmt.Sprint(r5.Models))

	// 6. Anthropic endpoint whose URL gives no hint at all.
	hidden := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("x-api-key") == "" {
			w.WriteHeader(401)
			fmt.Fprint(w, `{"type":"error","error":{"type":"authentication_error"}}`)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"data":[{"type":"model","id":"c1","display_name":"C1"}]}`)
	}))
	defer hidden.Close()
	r6 := catalog.Probe(ctx, hidden.URL+"/gateway", "k")
	check("anthropic detected with no URL hint", r6.Err == nil && r6.Flavor == catalog.FlavorAnthropic,
		r6.Flavor+" "+fmt.Sprint(r6.Err))

	// 7. URL normalisation: users paste all sorts of things.
	strict := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			w.WriteHeader(404)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"object":"list","data":[{"id":"m1","object":"model"}]}`)
	}))
	defer strict.Close()

	for _, variant := range []struct{ label, url string }{
		{"trailing slash", strict.URL + "/v1/"},
		{"surrounding whitespace", "  " + strict.URL + "/v1  "},
		{"full /models URL", strict.URL + "/v1/models"},
		{"full /chat/completions URL", strict.URL + "/v1/chat/completions"},
		{"missing /v1", strict.URL},
		{"query string", strict.URL + "/v1?key=abc"},
	} {
		res := catalog.Probe(ctx, variant.url, "k")
		check("normalises: "+variant.label,
			res.Err == nil && res.BaseURL == strict.URL+"/v1",
			res.BaseURL+" "+fmt.Sprint(res.Err))
	}

	// 8. A wrong-flavor guess must not be saved. An OpenAI server probed as
	//    anthropic must report openai, never anthropic.
	oai := openAIServer("k")
	defer oai.Close()
	r8 := catalog.Probe(ctx, oai.URL+"/anthropic-ish", "k")
	if r8.Err == nil {
		check("openai server is never mislabelled anthropic", r8.Flavor == catalog.FlavorOpenAI, r8.Flavor)
	} else {
		check("openai server is never mislabelled anthropic", true)
	}

	// 9. Genuinely dead endpoint still fails.
	r9 := catalog.Probe(ctx, "http://127.0.0.1:9/void", "k")
	check("dead endpoint still reports an error", r9.Err != nil)

	// 10. Error quality: an auth failure must surface as 401, not a 404 from
	//     a wrong-protocol guess.
	authOnly := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(401)
		fmt.Fprint(w, `{"error":{"message":"Incorrect API key provided"}}`)
	}))
	defer authOnly.Close()
	r10 := catalog.Probe(ctx, authOnly.URL+"/v1", "sk-bad")
	check("bad key surfaces a 401, not a confusing 404",
		r10.Err != nil && strings.Contains(r10.Err.Error(), "401"), fmt.Sprint(r10.Err))
}

// testPartialUI checks the UI path for endpoints with no model list.
func testPartialUI(tmp string) {
	section("/auth with a model-list-less endpoint")

	gw := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/chat/completions") {
			w.WriteHeader(400)
			fmt.Fprint(w, `{"error":{"message":"unknown model"}}`)
			return
		}
		w.WriteHeader(404)
	}))
	defer gw.Close()

	m := newModel(filepath.Join(tmp, "partial"))
	m.InputSetValue("/auth")
	m = key(m, "enter")
	m = typeStr(m, "add")
	m = key(m, "enter")
	m = typeStr(m, "bare gw")
	m = key(m, "enter")
	m = typeStr(m, gw.URL+"/v1")
	m = key(m, "enter")
	m = typeStr(m, "k")
	m = key(m, "enter")

	check("connects despite having no model list",
		m.AuthStageName() == "enter-model", m.AuthStageName()+" | "+m.AuthStatus())
	check("status explains the situation",
		strings.Contains(m.AuthStatus(), "no model list"), m.AuthStatus())
	check("view prompts for a model id",
		strings.Contains(m.View(), "does not publish a model list"), "no prompt")

	m = typeStr(m, "my-custom-model")
	m = key(m, "enter")
	check("typed model id is accepted", m.AuthStageName() == "list", m.AuthStageName())
	check("typed model becomes active",
		m.ModelID() == "bare-gw/my-custom-model", m.ModelID())
	check("provider is live", m.ProviderCount() >= 1, fmt.Sprint(m.ProviderCount()))
}

// testURLEntry covers the URL prompt: the exact string a user reported being
// rejected, forgiving normalisation, and the stale-error bug where a previous
// complaint followed the user onto the next screen.
func testURLEntry(tmp string) {
	section("/auth URL entry & stale errors")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/models") {
			w.WriteHeader(404)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"object":"list","data":[{"id":"m1","object":"model"}]}`)
	}))
	defer srv.Close()

	// Walk to the URL prompt.
	start := func(dir string) *tui.Model {
		m := newModel(filepath.Join(tmp, dir))
		m.InputSetValue("/auth")
		m = key(m, "enter")
		m = typeStr(m, "add")
		m = key(m, "enter")
		m = typeStr(m, "longcat")
		m = key(m, "enter")
		return m
	}

	// 1. The reported URL must be accepted verbatim.
	m := start("u1")
	const reported = "https://api.longcat.chat/openai/v1"
	m = typeStr(m, reported)
	check("typed URL reaches the buffer intact", m.AuthInputBuf() == reported,
		fmt.Sprintf("%q", m.AuthInputBuf()))
	m = key(m, "enter")
	check("REPORTED URL IS ACCEPTED", m.AuthStageName() == "add-key",
		m.AuthStageName()+" | "+m.AuthStatus())
	check("no error is shown for a valid URL",
		!strings.Contains(m.AuthStatus(), "must start with"), m.AuthStatus())

	// 2. THE BUG: a rejected URL's error must not follow the user forward.
	m = start("u2")
	m = typeStr(m, "not a url at all")
	m = key(m, "enter")
	check("invalid URL is rejected", m.AuthStageName() == "add-url", m.AuthStageName())
	check("rejection explains itself", m.AuthStatus() != "", m.AuthStatus())
	check("rejected text is kept for editing", m.AuthInputBuf() != "", m.AuthInputBuf())

	m.AuthClearInput()
	m = typeStr(m, srv.URL)
	check("STALE ERROR CLEARS AS SOON AS YOU TYPE",
		!strings.Contains(m.AuthStatus(), "must not contain spaces"), m.AuthStatus())
	m = key(m, "enter")
	check("good URL now advances", m.AuthStageName() == "add-key", m.AuthStageName())
	check("STALE ERROR IS GONE FROM THE VIEW",
		!strings.Contains(m.View(), "must not contain spaces"), "stale error still rendered")

	// 3. Forgiving normalisation.
	for _, c := range []struct{ label, in, want string }{
		{"bare host gets https", "api.example.com/v1", "https://api.example.com/v1"},
		{"single-slash scheme repaired", "https:/api.example.com/v1", "https://api.example.com/v1"},
		{"trailing slash trimmed", "https://api.example.com/v1/", "https://api.example.com/v1"},
		{"surrounding quotes stripped", "\"https://api.example.com/v1\"", "https://api.example.com/v1"},
		{"localhost assumes http", "localhost:1234/v1", "http://localhost:1234/v1"},
	} {
		mm := start("n-" + sanitizeDir(c.label))
		mm = typeStr(mm, c.in)
		mm = key(mm, "enter")
		ok := mm.AuthStageName() == "add-key" && mm.AuthDraftURL() == c.want
		check("normalises: "+c.label, ok,
			fmt.Sprintf("stage=%s url=%q want=%q", mm.AuthStageName(), mm.AuthDraftURL(), c.want))
	}

	// 4. Genuinely bad input is still rejected, with a specific reason.
	for _, c := range []struct{ label, in, reason string }{
		{"empty", "", "required"},
		{"spaces", "http://a b.com", "spaces"},
		{"bad scheme", "ftp://files.example.com", "scheme"},
		{"no host", "https://", "host"},
		{"not a hostname", "not-a-url", "not a valid host"},
	} {
		mm := start("b-" + sanitizeDir(c.label))
		mm = typeStr(mm, c.in)
		mm = key(mm, "enter")
		stayed := mm.AuthStageName() == "add-url"
		explained := strings.Contains(strings.ToLower(mm.AuthStatus()), c.reason)
		check("rejects: "+c.label, stayed && explained,
			fmt.Sprintf("stage=%s status=%q", mm.AuthStageName(), mm.AuthStatus()))
	}
}

func sanitizeDir(s string) string {
	return strings.ReplaceAll(strings.ReplaceAll(s, " ", "-"), "/", "")
}

// testDirtyKeys covers keys carrying paste artefacts. A newline in an API key
// makes net/http refuse to send the Authorization header, producing
// "invalid header field value" — an error that names the header but not the
// cause. Every entry point must scrub the value first.
func testDirtyKeys(tmp string) {
	section("pasted-key sanitising")

	// CleanSecret itself.
	for _, c := range []struct{ label, in, want string }{
		{"trailing newline", "sk-abc\n", "sk-abc"},
		{"trailing CRLF", "sk-abc\r\n", "sk-abc"},
		{"leading newline", "\nsk-abc", "sk-abc"},
		{"surrounding spaces", "  sk-abc  ", "sk-abc"},
		{"embedded tab", "sk-\tabc", "sk-abc"},
		{"embedded NUL", "sk-\x00abc", "sk-abc"},
		{"bracketed paste", "\x1b[200~sk-abc\x1b[201~", "sk-abc"},
		{"non-breaking space", "sk-\u00a0abc", "sk-abc"},
		{"zero-width space", "sk-\u200babc", "sk-abc"},
		{"BOM", "\ufeffsk-abc", "sk-abc"},
		{"straight quotes", `"sk-abc"`, "sk-abc"},
		{"smart quotes", "\u201csk-abc\u201d", "sk-abc"},
		{"Bearer prefix", "Bearer sk-abc", "sk-abc"},
		{"already clean", "sk-abc", "sk-abc"},
	} {
		got := catalog.CleanSecret(c.in)
		check("CleanSecret: "+c.label, got == c.want, fmt.Sprintf("got %q want %q", got, c.want))
	}
	check("CleanSecret leaves a clean key untouched", catalog.SecretIsClean("sk-abc123"))
	check("CleanSecret flags a dirty key", !catalog.SecretIsClean("sk-abc\n"))

	// A dirty key must not break the request. Without scrubbing this fails
	// with "invalid header field value" before the server is even contacted.
	var sawAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"object":"list","data":[{"id":"m1","object":"model"}]}`)
	}))
	defer srv.Close()

	ctx := context.Background()
	res := catalog.Probe(ctx, srv.URL+"/v1", "sk-live\n")
	check("KEY WITH A NEWLINE NO LONGER BREAKS THE REQUEST", res.Err == nil, fmt.Sprint(res.Err))
	check("  server received the cleaned key", sawAuth == "Bearer sk-live", sawAuth)

	res2 := catalog.Probe(ctx, srv.URL+"/v1", "\x1b[200~sk-live\x1b[201~")
	check("bracketed-paste key works", res2.Err == nil, fmt.Sprint(res2.Err))
	check("  wrapper stripped from the header", sawAuth == "Bearer sk-live", sawAuth)

	// The transport error, if one ever occurs, must explain itself.
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	bad.Close() // closed: connection refused
	res3 := catalog.Probe(ctx, bad.URL+"/v1", "k")
	check("connection refused is explained plainly",
		res3.Err != nil && strings.Contains(res3.Err.Error(), "refused"), fmt.Sprint(res3.Err))

	res4 := catalog.Probe(ctx, "https://nonexistent-host-xyz-rick.invalid/v1", "k")
	check("unknown host is explained plainly",
		res4.Err != nil && strings.Contains(res4.Err.Error(), "host not found"), fmt.Sprint(res4.Err))

	// Through the UI: typing a key whose burst contains a newline.
	m := newModel(filepath.Join(tmp, "dirty"))
	m.InputSetValue("/auth")
	m = key(m, "enter")
	m = typeStr(m, "add")
	m = key(m, "enter")
	m = typeStr(m, "dirtygw")
	m = key(m, "enter")
	m = typeStr(m, srv.URL+"/v1")
	m = key(m, "enter")
	// Simulate a paste burst carrying a trailing newline.
	m = send(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("sk-live\n")})
	check("newline never enters the input buffer",
		!strings.ContainsAny(m.AuthInputBuf(), "\r\n"), fmt.Sprintf("%q", m.AuthInputBuf()))
	m = key(m, "enter")
	check("DIRTY PASTE CONNECTS SUCCESSFULLY",
		m.AuthStageName() == "pick-model", m.AuthStageName()+" | "+m.AuthStatus())

	saved, _ := config.LoadCredentials()
	if cred, ok := saved.Providers["dirtygw"]; ok {
		check("stored key is clean", cred.APIKey == "sk-live", fmt.Sprintf("%q", cred.APIKey))
	} else {
		check("stored key is clean", false, "credential missing")
	}

	// A key already saved dirty (by an older build) must self-heal on load.
	dirty, _ := config.LoadCredentials()
	dirty.Set("legacy", config.Credential{Type: "openai", APIKey: "sk-old\n", BaseURL: "https://x/v1"})
	_ = dirty.Save()
	healed, _ := config.LoadCredentials()
	check("a previously saved dirty key self-heals on load",
		healed.Providers["legacy"].APIKey == "sk-old",
		fmt.Sprintf("%q", healed.Providers["legacy"].APIKey))
	healed.Remove("legacy")
	_ = healed.Save()
}

// testGatewayPrefix covers multi-protocol gateways that host their APIs under
// a prefix (api.example.com/openai/v1). The bare host and /v1 both 404 there,
// so the probe must discover the prefixed path instead of giving up.
func testGatewayPrefix(tmp string) {
	section("multi-protocol gateway prefixes")

	// Mirrors api.longcat.chat: only /openai/v1/* and /anthropic/v1/* exist;
	// everything else is an nginx-style HTML 404.
	const html404 = "<html>\n<head><title>404 Not Found</title></head>\n<body>\n<center><h1>404 Not Found</h1></center>\n</body>\n</html>"
	gw := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/openai/v1/models":
			if r.Header.Get("Authorization") != "Bearer good" {
				w.WriteHeader(401)
				fmt.Fprint(w, `{"error":{"code":"invalid_api_key","message":"incorrect api key"}}`)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"object":"list","data":[{"id":"LongCat-Flash","object":"model"}]}`)
		case "/anthropic/v1/models":
			if r.Header.Get("x-api-key") != "good" {
				w.WriteHeader(401)
				fmt.Fprint(w, `{"error":{"code":"invalid_api_key"}}`)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"data":[{"type":"model","id":"claude-x","display_name":"Claude X"}]}`)
		default:
			w.Header().Set("Content-Type", "text/html")
			w.WriteHeader(404)
			fmt.Fprint(w, html404)
		}
	}))
	defer gw.Close()

	ctx := context.Background()

	// The core fix: a bare host must find /openai/v1.
	r1 := catalog.Probe(ctx, gw.URL, "good")
	check("BARE HOST FINDS THE /openai/v1 PREFIX", r1.Err == nil, fmt.Sprint(r1.Err))
	check("  resolved to the prefixed path",
		strings.HasSuffix(r1.BaseURL, "/openai/v1"), r1.BaseURL)
	check("  model list fetched", len(r1.Models) == 1, fmt.Sprint(len(r1.Models)))

	// A user who typed the plausible-but-wrong /v1 must also recover.
	r2 := catalog.Probe(ctx, gw.URL+"/v1", "good")
	check("WRONG /v1 PATH RECOVERS TO /openai/v1", r2.Err == nil, fmt.Sprint(r2.Err))
	check("  corrected path is returned",
		strings.HasSuffix(r2.BaseURL, "/openai/v1"), r2.BaseURL)

	// The explicit correct path still works, unchanged.
	r3 := catalog.Probe(ctx, gw.URL+"/openai/v1", "good")
	check("explicit /openai/v1 works", r3.Err == nil && r3.Flavor == catalog.FlavorOpenAI,
		r3.Flavor+" "+fmt.Sprint(r3.Err))

	// The anthropic side of the same gateway.
	r4 := catalog.Probe(ctx, gw.URL+"/anthropic/v1", "good")
	check("explicit /anthropic/v1 works", r4.Err == nil, fmt.Sprint(r4.Err))
	check("  detected as anthropic", r4.Flavor == catalog.FlavorAnthropic, r4.Flavor)

	// A bad key must report the auth failure, NOT a 404 from a wrong guess.
	r5 := catalog.Probe(ctx, gw.URL, "wrong")
	check("bad key reports 401, not 404", r5.Err != nil &&
		strings.Contains(r5.Err.Error(), "401"), fmt.Sprint(r5.Err))

	// A host with no API anywhere gets an actionable message, not raw HTML.
	dead := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(404)
		fmt.Fprint(w, html404)
	}))
	defer dead.Close()
	r6 := catalog.Probe(ctx, dead.URL, "k")
	check("host with no API is explained clearly", r6.Err != nil &&
		strings.Contains(r6.Err.Error(), "no API found at that URL"), fmt.Sprint(r6.Err))
	check("  HTML markup is never echoed at the user",
		r6.Err != nil && !strings.Contains(r6.Err.Error(), "<html"), fmt.Sprint(r6.Err))

	// End to end through the UI, entering only the bare host.
	m := newModel(filepath.Join(tmp, "gwprefix"))
	m.InputSetValue("/auth")
	m = key(m, "enter")
	m = typeStr(m, "add")
	m = key(m, "enter")
	m = typeStr(m, "longcat")
	m = key(m, "enter")
	m = typeStr(m, gw.URL) // bare host, as a user would paste it
	m = key(m, "enter")
	m = typeStr(m, "good")
	m = key(m, "enter")
	check("UI CONNECTS FROM A BARE GATEWAY HOST",
		m.AuthStageName() == "pick-model", m.AuthStageName()+" | "+m.AuthStatus())

	saved, _ := config.LoadCredentials()
	if cred, ok := saved.Providers["longcat"]; ok {
		check("saved base URL includes the prefix",
			strings.HasSuffix(cred.BaseURL, "/openai/v1"), cred.BaseURL)
	} else {
		check("saved base URL includes the prefix", false, "credential missing")
	}
}
