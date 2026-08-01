package tui

import (
	"context"
	"testing"

	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"

	"rick/internal/config"
	"rick/internal/provider/catalog"
)

func keyRunes(r rune) tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}} }

func newScratchAuthModel(t *testing.T) *Model {
	t.Helper()
	creds := &config.Credentials{Providers: map[string]config.Credential{
		"alpha": {Type: "openai", APIKey: "sk-alpha", BaseURL: "https://a.example.com"},
		"beta":  {Type: "openai", APIKey: "sk-beta", BaseURL: "https://b.example.com"},
	}}
	m := &Model{
		ready:    true,
		width:    100,
		height:   30,
		styles:   NewStyles(nil),
		tx:       newTranscript(),
		viewport: viewport.New(100, 20),
		creds:    creds,
	}
	m.input = textarea.New()
	return m
}

// A: confirmRemove is never reset when leaving the edit menu.
func TestScratchConfirmRemoveLeaksAcrossProviders(t *testing.T) {
	m := newScratchAuthModel(t)
	m.auth = authState{active: true, stage: authEditMenu, draftID: "alpha"}

	m.authEditKey("6") // arms the confirmation for alpha
	if !m.auth.confirmRemove {
		t.Fatal("expected confirmRemove to be armed")
	}
	m.authBack() // back to the list; alpha untouched
	if _, ok := m.creds.Providers["alpha"]; !ok {
		t.Fatal("alpha was removed without confirmation")
	}
	m.authSelectRow(authRow{id: "beta", connected: true})
	if m.auth.stage != authEditMenu {
		t.Fatalf("stage = %v", m.auth.stage)
	}
	t.Logf("confirmRemove still armed after switching providers: %v", m.auth.confirmRemove)
	m.authEditKey("6")
	if _, ok := m.creds.Providers["beta"]; !ok {
		t.Log("BUG CONFIRMED: beta removed on the FIRST press of 6, no confirmation")
	} else {
		t.Log("beta survived, confirmation was required")
	}
}

// B: esc during a probe does not cancel it; the late result hijacks the UI.
func TestScratchProbeCancelIsIgnored(t *testing.T) {
	m := newScratchAuthModel(t)
	m.auth = authState{active: true, stage: authEnterKey, draftID: "alpha", draftURL: "https://a.example.com", draftKey: "sk-alpha"}
	m.authStartProbe()
	if !m.auth.busy {
		t.Fatal("probe did not set busy")
	}
	m.authBack() // user presses esc
	t.Logf("after esc: stage=%v busy=%v", m.auth.stage, m.auth.busy)

	// keys are now dead because busy is still true
	before := m.auth.stage
	m.handleAuthKey(keyRunes('1'), "1")
	t.Logf("input while busy changed stage %v -> %v, inputBuf=%q", before, m.auth.stage, m.auth.inputBuf)

	// the late probe result still lands
	m.applyAuthProbe(authProbeMsg{id: "alpha", key: "sk-alpha", res: catalog.ProbeResult{
		Flavor: "openai", BaseURL: "https://a.example.com",
		Models: []catalog.Model{{ID: "gpt-x"}},
	}})
	t.Logf("after late probe result: stage=%v statusLine=%q", m.auth.stage, stripANSI(m.auth.statusLine))
}

// C: esc while waiting for OAuth leaves the generation counter untouched, so
// the cancelled poll's error is displayed as a sign-in failure.
func TestScratchOAuthCancelShowsSpuriousError(t *testing.T) {
	m := newScratchAuthModel(t)
	_, cancel := context.WithCancel(context.Background())
	m.auth = authState{active: true, stage: authOAuthWaiting, oauthGen: 1, oauthCancel: cancel,
		oauthUserCode: "ABCD-1234", oauthVerifURI: "https://example.com/device"}
	m.authBack()
	t.Logf("after esc: stage=%v gen=%d", m.auth.stage, m.auth.oauthGen)
	m.applyOAuthDone(oauthDoneMsg{gen: 1, err: context.Canceled})
	t.Logf("after cancelled poll returns: stage=%v status=%q", m.auth.stage, stripANSI(m.auth.statusLine))
}

// D: a custom provider whose sanitised name collides with a saved provider
// silently targets that provider.
func TestScratchCustomNameCollision(t *testing.T) {
	m := newScratchAuthModel(t)
	m.auth = authState{active: true, stage: authAddName, custom: true}
	m.auth.inputBuf = "Alpha"
	m.authInputKey(keyRunes('\n'), "enter")
	t.Logf("draftID=%q stage=%v collidesWithSaved=%v", m.auth.draftID, m.auth.stage,
		func() bool { _, ok := m.creds.Providers[m.auth.draftID]; return ok }())
}
