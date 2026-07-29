package tui

import (
	"testing"

	"rick/internal/config"
	"rick/internal/permission"
)

func TestSlashYoloEnablesAndExplicitlyDisablesYoloMode(t *testing.T) {
	perms := permission.New(&config.Permission{Default: config.PermAsk}, t.TempDir())
	model := &Model{deps: Deps{Perms: perms}, tx: newTranscript()}

	model.runSlash("/yolo")
	if !perms.Yolo() {
		t.Fatal("/yolo did not enable yolo mode")
	}

	model.runSlash("/yolo")
	if !perms.Yolo() {
		t.Fatal("repeating /yolo unexpectedly toggled yolo mode off")
	}

	model.runSlash("/yolo off")
	if perms.Yolo() {
		t.Fatal("/yolo off did not disable yolo mode")
	}

	model.runSlash("/yolo toggle")
	if !perms.Yolo() {
		t.Fatal("/yolo toggle did not toggle yolo mode on")
	}
}
