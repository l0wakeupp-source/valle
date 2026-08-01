package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// SaveThemeChoice persists the selected theme to the global tui.json.
func SaveThemeChoice(name string) error {
	return SaveTUIOption("theme", name)
}

// SaveTUIOption persists a single key in the global tui.json (presentation).
func SaveTUIOption(key string, value any) error {
	return patchGlobal("tui.json", key, value)
}

// SaveModelChoice persists the active model to the global rick.json, so the
// next launch resumes with whatever was last selected.
//
// It belongs in rick.json rather than tui.json: the model is runtime
// behaviour, not presentation, and the two files are deliberately not merged.
func SaveModelChoice(id string) error {
	return patchGlobal("rick.json", "model", id)
}

// SaveWebSearchConfig persists the web-search settings to the global rick.json
// while preserving all unrelated configuration keys.
func SaveWebSearchConfig(cfg WebSearchConfig) error {
	return patchGlobal("rick.json", "web_search", cfg)
}

// patchGlobal sets one key in a global config file, leaving everything else
// untouched.
//
// The file is read as a generic map rather than into a typed struct so a
// user's unknown or future keys survive the round-trip — a settings write must
// never silently drop configuration it does not understand. The write goes
// through a temp file so an interrupted save cannot truncate the original.
func patchGlobal(name, key string, value any) error {
	dir := GlobalDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	// A .jsonc variant is loaded in preference to .json, so writing the plain
	// file would leave the value permanently shadowed. We cannot rewrite the
	// .jsonc either: marshalling it back through encoding/json would strip
	// every comment the user wrote. Refuse instead of destroying their file.
	path := filepath.Join(dir, name)
	if jsonc := path + "c"; firstExisting(jsonc) != "" {
		return fmt.Errorf("%s exists and takes precedence; edit %q by hand to change %q",
			filepath.Base(jsonc), jsonc, key)
	}

	doc := map[string]any{}
	if raw, err := os.ReadFile(path); err == nil {
		if err := json.Unmarshal(StripJSONC(raw), &doc); err != nil {
			return fmt.Errorf("refusing to patch malformed %s: %w", path, err)
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	doc[key] = value

	data, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(data, '\n'), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
