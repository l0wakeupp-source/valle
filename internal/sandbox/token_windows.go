//go:build windows

package sandbox

import (
	"fmt"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Windows mandatory integrity levels. A process at low integrity may read
// medium-integrity objects but may not write them — the kernel enforces this
// on every securable object, with no per-path ACL bookkeeping.
//
// This is the mechanism Chrome and Edge use to confine renderer processes, and
// it is the right primitive for "let the build read the toolchain but keep it
// out of the user's documents".
//
// The catch is that a low-integrity process cannot write the workspace either
// unless the workspace itself carries a low-integrity label, and MSYS-based
// shells (git-bash) additionally need a writable \BaseNamedObjects subdirectory
// that low integrity denies outright. labelWorkspaceLow handles the first
// problem; the second is why the backend probes the shell before committing to
// an integrity drop.
const (
	securityMandatoryLowRID = 0x1000

	// sddlLowIntegrityNoWriteUp is a SACL granting low-integrity processes
	// full access to the labelled object: "mandatory label, no write-up policy,
	// low integrity level".
	sddlLowIntegrityLabel = "S:(ML;OICI;NW;;;LW)"
)

// lowIntegrityToken duplicates rick's own token and drops it to low integrity.
//
// Unlike CreateRestrictedToken — whose restricting SIDs are intersected against
// every DACL, which breaks reads of anything granted to Users rather than
// Everyone, including the git-bash install — an integrity drop leaves reads
// intact and denies only writes upward.
func lowIntegrityToken() (windows.Token, error) {
	var self windows.Token
	if err := windows.OpenProcessToken(windows.CurrentProcess(),
		windows.TOKEN_DUPLICATE|windows.TOKEN_ASSIGN_PRIMARY|windows.TOKEN_QUERY|
			windows.TOKEN_ADJUST_DEFAULT|windows.TOKEN_ADJUST_SESSIONID, &self); err != nil {
		return 0, fmt.Errorf("open own token: %w", err)
	}
	defer self.Close()

	var dup windows.Token
	if err := windows.DuplicateTokenEx(self,
		windows.TOKEN_ALL_ACCESS, nil,
		windows.SecurityImpersonation, windows.TokenPrimary, &dup); err != nil {
		return 0, fmt.Errorf("duplicate token: %w", err)
	}

	if err := setIntegrityLevel(dup, securityMandatoryLowRID); err != nil {
		dup.Close()
		return 0, err
	}
	return dup, nil
}

// setIntegrityLevel stamps a mandatory integrity SID onto a token.
func setIntegrityLevel(token windows.Token, rid uint32) error {
	sid, err := integritySid(rid)
	if err != nil {
		return err
	}
	defer windows.FreeSid(sid)

	// TOKEN_MANDATORY_LABEL is a single SID_AND_ATTRIBUTES. The SID must be
	// reachable from the struct, so pass its pointer and size the call to
	// include the SID body.
	label := windows.Tokenmandatorylabel{
		Label: windows.SIDAndAttributes{
			Sid:        sid,
			Attributes: windows.SE_GROUP_INTEGRITY,
		},
	}
	if err := windows.SetTokenInformation(token,
		windows.TokenIntegrityLevel,
		(*byte)(unsafe.Pointer(&label)),
		label.Size()); err != nil {
		return fmt.Errorf("set integrity level: %w", err)
	}
	return nil
}

// integritySid constructs S-1-16-<rid>.
func integritySid(rid uint32) (*windows.SID, error) {
	var sid *windows.SID
	auth := windows.SidIdentifierAuthority{Value: [6]byte{0, 0, 0, 0, 0, 16}}
	if err := windows.AllocateAndInitializeSid(&auth, 1, rid, 0, 0, 0, 0, 0, 0, 0, &sid); err != nil {
		return nil, fmt.Errorf("build integrity sid: %w", err)
	}
	return sid, nil
}

// labelWorkspaceLow marks dir (and everything created under it) writable by
// low-integrity processes.
//
// Without this a low-integrity child cannot write the very directory it is
// meant to work in. The label inherits to new children (OICI), so files the
// build creates stay writable on the next run.
func labelWorkspaceLow(dir string) error {
	sd, err := windows.SecurityDescriptorFromString(sddlLowIntegrityLabel)
	if err != nil {
		return fmt.Errorf("parse label sddl: %w", err)
	}
	sacl, _, err := sd.SACL()
	if err != nil {
		return fmt.Errorf("read label sacl: %w", err)
	}
	if err := windows.SetNamedSecurityInfo(dir,
		windows.SE_FILE_OBJECT,
		windows.LABEL_SECURITY_INFORMATION,
		nil, nil, nil, sacl); err != nil {
		return fmt.Errorf("apply label to %s: %w", dir, err)
	}
	return nil
}
