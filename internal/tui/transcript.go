package tui

import (
	"strings"

	"github.com/charmbracelet/bubbles/viewport"
)

// transcript owns the rendered chat buffer and the scroll policy.
//
// Two problems it solves:
//
//  1. Re-rendering every message on every stream chunk. Rendering is the
//     expensive part (wrapping, markdown, diffs), so each entry is rendered
//     once and cached; only entries that actually changed are re-rendered.
//     During streaming that is a single line, not the whole history.
//
//  2. Stick-to-bottom done wrong. Following the tail must be a piece of
//     remembered state, not a guess made from "are we streaming?". Once the
//     user scrolls up they stay put until they come back to the bottom.
type transcript struct {
	blocks []string // rendered form of each msgs[i]; "" means hidden
	dirty  []bool   // blocks[i] needs re-rendering
	width  int      // width the cache was built at

	// live is the streaming tail (thinking + assistant text). It changes on
	// every chunk and is therefore kept out of the cache.
	live string

	content string // blocks + live, joined
	stick   bool   // follow the tail as content grows
	unseen  int    // entries appended while scrolled away

	// settled is blocks already joined, cached across refreshes so a stream
	// chunk appends the live tail instead of re-concatenating the history.
	settled      string
	settledStale bool
}

func newTranscript() *transcript { return &transcript{stick: true} }

// reset clears everything (new session).
func (t *transcript) reset() {
	t.blocks, t.dirty = nil, nil
	t.live, t.content = "", ""
	t.settled, t.settledStale = "", false
	t.stick, t.unseen = true, 0
	t.width = 0 // force a full re-render on the next pass
}

// sync grows or shrinks the cache to match n messages.
func (t *transcript) sync(n int) {
	for len(t.blocks) < n {
		t.blocks = append(t.blocks, "")
		t.dirty = append(t.dirty, true)
	}
	if len(t.blocks) > n {
		// Truncation removes content without dirtying any surviving entry,
		// so the cached join must be rebuilt explicitly.
		t.blocks = t.blocks[:n]
		t.dirty = t.dirty[:n]
		t.settledStale = true
	}
}

// invalidate marks one entry for re-rendering.
func (t *transcript) invalidate(i int) {
	if i >= 0 && i < len(t.dirty) {
		t.dirty[i] = true
	}
}

// invalidateAll forces a full re-render (theme change, width change).
func (t *transcript) invalidateAll(width int) {
	t.width = width
	for i := range t.dirty {
		t.dirty[i] = true
	}
}

// render rebuilds only the dirty entries, then joins everything.
func (t *transcript) render(n, width int, renderOne func(i int) string) {
	if width != t.width {
		t.invalidateAll(width)
	}
	t.sync(n)

	dirty := t.settledStale
	t.settledStale = false
	for i := 0; i < n; i++ {
		if t.dirty[i] {
			t.blocks[i] = renderOne(i)
			t.dirty[i] = false
			dirty = true
		}
	}

	// Re-joining every settled block on each refresh is O(transcript) work
	// per frame, and streaming refreshes 25 times a second. The settled
	// prefix only changes when a block is re-rendered, so cache it and
	// append the live tail on its own.
	if dirty {
		var b strings.Builder
		b.Grow(len(t.settled))
		for _, blk := range t.blocks {
			if blk == "" {
				continue
			}
			if b.Len() > 0 {
				b.WriteString("\n")
			}
			b.WriteString(blk)
			b.WriteString("\n")
		}
		t.settled = b.String()
	}

	if t.live == "" {
		t.content = t.settled
		return
	}
	var b strings.Builder
	b.Grow(len(t.settled) + len(t.live) + 2)
	b.WriteString(t.settled)
	if b.Len() > 0 {
		b.WriteString("\n")
	}
	b.WriteString(t.live)
	b.WriteString("\n")
	t.content = b.String()
}

// apply pushes the content into a viewport, honouring the scroll policy.
func (t *transcript) apply(vp *viewport.Model) {
	before := vp.YOffset
	wasAtBottom := vp.AtBottom()

	vp.SetContent(t.content)

	switch {
	case t.stick:
		vp.GotoBottom()
		t.unseen = 0
	case wasAtBottom:
		// The user was at the bottom but stick was off (they had just
		// scrolled back down): resume following.
		t.stick = true
		vp.GotoBottom()
		t.unseen = 0
	default:
		// Hold the user's position. SetContent can clamp the offset when the
		// content grows, so restore it explicitly.
		vp.SetYOffset(before)
	}
}

// userScrolled is called after any manual scroll to update the policy.
func (t *transcript) userScrolled(vp *viewport.Model) {
	if vp.AtBottom() {
		t.stick = true
		t.unseen = 0
		return
	}
	t.stick = false
}

// noteAppend records new content arriving while the user is scrolled away.
func (t *transcript) noteAppend() {
	if !t.stick {
		t.unseen++
	}
}

// following reports whether the view is pinned to the tail.
func (t *transcript) following() bool { return t.stick }

// pending is the number of entries added while scrolled away.
func (t *transcript) pending() int { return t.unseen }

// jumpToBottom re-engages following.
func (t *transcript) jumpToBottom(vp *viewport.Model) {
	t.stick = true
	t.unseen = 0
	vp.GotoBottom()
}

// relativePos captures scroll position as a fraction, for resize.
func relativePos(vp *viewport.Model) float64 {
	max := vp.TotalLineCount() - vp.Height
	if max <= 0 {
		return 1
	}
	return float64(vp.YOffset) / float64(max)
}

// restorePos re-applies a fractional position after a resize.
func restorePos(vp *viewport.Model, frac float64) {
	max := vp.TotalLineCount() - vp.Height
	if max <= 0 {
		vp.GotoTop()
		return
	}
	vp.SetYOffset(int(frac*float64(max) + 0.5))
}
