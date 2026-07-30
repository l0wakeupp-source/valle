package tui

import (
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

var (
	photoCache   = map[photoKey]string{}
	photoCacheMu sync.Mutex
	photoWriteMu sync.Mutex
)

type photoTickMsg struct{}
type photoDrawnMsg struct {
	box        photoKey
	generation uint64
}
type photoClearedMsg struct {
	box        photoKey
	generation uint64
}

func photoTick() tea.Cmd {
	return tea.Tick(120*time.Millisecond, func(time.Time) tea.Msg { return photoTickMsg{} })
}

func (m *Model) syncPhoto() tea.Cmd {
	box := m.photoBox
	drawn := m.photoDrawn
	if box == drawn && m.photoPending == (photoKey{}) {
		return nil
	}
	if box == m.photoPending {
		return nil
	}
	m.photoPending = box
	m.photoGeneration++
	generation := m.photoGeneration
	if box == (photoKey{}) {
		return func() tea.Msg {
			clearPhoto(os.Stdout, drawn)
			return photoClearedMsg{box: drawn, generation: generation}
		}
	}
	width := m.width
	row := m.photoRow
	return func() tea.Msg {
		drawPhoto(os.Stdout, box, width, row)
		return photoDrawnMsg{box: box, generation: generation}
	}
}

func (m *Model) DrawPhotoTo(w io.Writer) { m.drawPhoto(w) }

func (m *Model) PhotoBoxSize() string {
	return fmt.Sprintf("%dx%d", m.photoBox.cols, m.photoBox.rows)
}

func (m *Model) drawPhoto(w io.Writer) {
	box := m.photoBox
	if box == (photoKey{}) || box == m.photoDrawn {
		return
	}
	drawPhoto(w, box, m.width, m.photoRow)
	m.photoDrawn = box
}

func drawPhoto(w io.Writer, box photoKey, width, photoRow int) {
	if box == (photoKey{}) {
		return
	}

	col := width - box.cols + 1
	row := photoRow + 1
	if col < 1 {
		col = 1
	}
	if row < 1 {
		row = 1
	}

	photoCacheMu.Lock()
	esc, ok := photoCache[box]
	if !ok {
		switch box.proto {
		case imageKitty:
			esc = kittyImage(rickPNG, box.cols, box.rows)
		case imageITerm:
			esc = itermImage(rickPNG, box.cols, box.rows)
		default:
			photoCacheMu.Unlock()
			return
		}
		photoCache[box] = esc
	}
	photoCacheMu.Unlock()

	var b strings.Builder
	b.WriteString("\x1b7")
	fmt.Fprintf(&b, "\x1b[%d;%dH", row, col)
	b.WriteString(esc)
	b.WriteString("\x1b8")

	photoWriteMu.Lock()
	defer photoWriteMu.Unlock()
	_, _ = io.WriteString(w, b.String())
}

func clearPhoto(w io.Writer, drawn photoKey) {
	if drawn == (photoKey{}) {
		return
	}
	if drawn.proto == imageKitty {
		photoWriteMu.Lock()
		defer photoWriteMu.Unlock()
		_, _ = io.WriteString(w, "\x1b_Ga=d\x1b\\")
	}
}
