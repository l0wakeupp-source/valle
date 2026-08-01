package tui

import "io"

// Graphical terminal protocols are intentionally unsupported. Rick's mascot
// is rendered as pixel art only, which keeps startup and redraws safe in
// multiplexers, wrappers, remote shells, and terminals with ambiguous
// capability reporting.
func DetectImageSupport() string { return "none" }
func DetectImageProto() string   { return "none" }
func QueryImageProto() string    { return "none" }
func ProbeSequence() string      { return "" }

// These no-op compatibility functions keep the standalone UI diagnostic
// command source-compatible without retaining a graphical rendering path.
func RickPNG() []byte                     { return nil }
func KittyImage([]byte, int, int) string  { return "" }
func ItermImage([]byte, int, int) string  { return "" }
func (m *Model) ForceImageProto(_ string) {}
func (m *Model) PhotoBoxSize() string     { return "0x0" }
func (m *Model) DrawPhotoTo(_ io.Writer)  {}
