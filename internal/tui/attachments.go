// Package tui implements rick's terminal interface.
package tui

import (
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// attachment represents a file or image attached to the pending prompt.
type attachment struct {
	Path      string `json:"path"`
	Name      string `json:"name"`
	IsImage   bool   `json:"is_image"`
	IsVideo   bool   `json:"is_video"`
	Base64    string `json:"base64,omitempty"`
	MediaType string `json:"media_type,omitempty"`
	Size      int64  `json:"size"`
}

// addAttachment loads a file and adds it as an attachment.
// Images are base64-encoded for vision models.
func addAttachment(path string) (*attachment, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("stat %s: %w", path, err)
	}

	if info.IsDir() {
		return nil, fmt.Errorf("cannot attach directory: %s", path)
	}

	att := &attachment{
		Path:    path,
		Name:    filepath.Base(path),
		Size:    info.Size(),
		IsImage: isImageFile(path),
		IsVideo: isVideoFile(path),
	}

	// Base64-encode images for vision models
	if att.IsImage {
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", path, err)
		}
		att.Base64 = base64.StdEncoding.EncodeToString(data)
		att.MediaType = mediaTypeFor(path)
	}

	return att, nil
}

func isImageFile(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".png", ".jpg", ".jpeg", ".gif", ".bmp", ".webp", ".tiff", ".tif", ".ico":
		return true
	default:
		return false
	}
}

func isVideoFile(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".mp4", ".avi", ".mkv", ".mov", ".wmv", ".flv", ".webm", ".m4v", ".3gp":
		return true
	default:
		return false
	}
}

// mediaTypeFor returns the MIME type for an image file.
func mediaTypeFor(path string) string {
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".png":
		return "image/png"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".gif":
		return "image/gif"
	case ".webp":
		return "image/webp"
	case ".bmp":
		return "image/bmp"
	case ".tiff", ".tif":
		return "image/tiff"
	}
	return "application/octet-stream"
}

// attachmentMarker matches [image #1] or [file #2] in the input.
var attachmentMarker = regexp.MustCompile(`\[(image|file) #(\d+)\]`)

// parseAttachmentMarkers extracts attachment references from text.
// Returns the cleaned text (markers removed, whitespace collapsed) and the ordered list of attachment indices referenced.
func parseAttachmentMarkers(text string) ([]int, string) {
	matches := attachmentMarker.FindAllStringSubmatch(text, -1)
	if len(matches) == 0 {
		return nil, text
	}

	var indices []int
	for _, m := range matches {
		var idx int
		fmt.Sscanf(m[2], "%d", &idx)
		indices = append(indices, idx)
	}

	cleaned := attachmentMarker.ReplaceAllString(text, "")
	cleaned = strings.Join(strings.Fields(cleaned), " ")
	return indices, cleaned
}

func stringSliceContains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

// removeLastAttachmentMarker strips the last [image #N] or [file #N] from text.
// Returns the cleaned text and the index that was removed (or -1 if none).
func removeLastAttachmentMarker(text string) (string, int) {
	matches := attachmentMarker.FindAllStringSubmatchIndex(text, -1)
	if len(matches) == 0 {
		return text, -1
	}

	last := matches[len(matches)-1]
	cleaned := text[:last[0]] + text[last[1]:]
	cleaned = strings.Join(strings.Fields(cleaned), " ")

	var idx int
	fmt.Sscanf(text[last[4]:last[5]], "%d", &idx)
	return cleaned, idx
}
