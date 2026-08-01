package tui

import (
	"encoding/binary"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
)

func TestScratchTruncateOSC8(t *testing.T) {
	line := linkifyLine("see https://example.com/averylongpath/here for details", true)
	t.Logf("linkified=%q width=%d", line, lipgloss.Width(line))
	cut := truncate(line, 20)
	t.Logf("truncated=%q hasTerminator=%v", cut, strings.HasSuffix(cut, "\x1b]8;;\a"))
	t.Logf("count of OSC8 openers in cut: %d", strings.Count(cut, "\x1b]8;;"))
}

func TestScratchLinkReControlChars(t *testing.T) {
	evil := "http://ok.com\x1b]0;pwned\x07rest"
	t.Logf("match=%q", linkRe.FindString(evil))
	out := linkifyLine(evil, true)
	t.Logf("out=%q", out)
	bell := "http://ok.com\x07more"
	t.Logf("bellmatch=%q", linkRe.FindString(bell))
}

func TestScratchDIBMalformed(t *testing.T) {
	mk := func(w, h int32, bits uint16, extra int) []byte {
		b := make([]byte, 40+extra)
		binary.LittleEndian.PutUint32(b[0:4], 40)
		binary.LittleEndian.PutUint32(b[4:8], uint32(w))
		binary.LittleEndian.PutUint32(b[8:12], uint32(h))
		binary.LittleEndian.PutUint16(b[12:14], 1)
		binary.LittleEndian.PutUint16(b[14:16], bits)
		binary.LittleEndian.PutUint32(b[16:20], 0)
		return b
	}
	func() {
		defer func() { t.Logf("truncated-pixels recover=%v", recover()) }()
		p, err := dibToPNG(mk(64, 64, 24, 0)) // header claims 64x64 but no pixel bytes
		t.Logf("no panic: %v %v", p, err)
	}()
	func() {
		defer func() { t.Logf("huge-header recover=%v", recover()) }()
		b := mk(4, 4, 32, 64)
		binary.LittleEndian.PutUint32(b[0:4], 0xFFFFFF00) // absurd biSize
		p, err := dibToPNG(b)
		t.Logf("no panic: %v %v", p, err)
	}()
	func() {
		defer func() { t.Logf("alpha-zero recover=%v", recover()) }()
		b := mk(1, 1, 32, 4) // one BGRA pixel, alpha byte = 0
		b[40], b[41], b[42], b[43] = 0x10, 0x20, 0x30, 0x00
		p, err := dibToPNG(b)
		t.Logf("path=%v err=%v (alpha 0 => transparent)", p, err)
	}()
}
