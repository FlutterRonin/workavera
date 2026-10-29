// Package sandbox gives each user one long-lived Docker container in which
// the Chat assistant runs commands and edits files. Every Docker call runs
// the docker CLI with an argument list, never through a host shell, and every
// container and volume name derives from the authenticated user's ID only.
package sandbox

import (
	"errors"
	"net/http"
	"regexp"

	"github.com/pocketbase/pocketbase/core"
)

const (
	networkName    = "workavera-sandbox"
	labelSandbox   = "workavera.sandbox"
	labelUser      = "workavera.user"
	toolsLabel     = "org.workavera.sandbox.tools"
	workspaceDir   = "/workspace"
	chatsDir       = workspaceDir + "/chats"
	jobsDir        = "/tmp/.workavera/jobs"
	managerKey     = "sandbox.manager"
	maxErrorLength = 500
)

// Stable error codes returned by the HTTP API and the Assistant tools.
const (
	CodeUnavailable         = "sandbox_unavailable"
	CodeDisabled            = "sandbox_disabled"
	CodePreparing           = "sandbox_preparing"
	CodeStartFailed         = "sandbox_start_failed"
	CodeFileNotFound        = "sandbox_file_not_found"
	CodeFileTooLarge        = "sandbox_file_too_large"
	CodeFileNotText         = "sandbox_file_not_text"
	CodeEditNoMatch         = "sandbox_edit_no_match"
	CodeEditAmbiguous       = "sandbox_edit_ambiguous"
	CodeExportTypeUnsupport = "export_type_unsupported"
	CodeNetworkDisabled     = "sandbox_network_disabled"
	CodeInvalidInput        = "invalid_input"
)

// Error is a failure with a stable code and a message that is safe to show
// to users and models. The wrapped cause, which may carry Docker output, is
// only ever logged.
type Error struct {
	Code    string
	Message string
	Status  int
	cause   error
}

func (e *Error) Error() string { return e.Code + ": " + e.Message }

func (e *Error) Unwrap() error { return e.cause }

func newError(code string, status int, message string) *Error {
	return &Error{Code: code, Status: status, Message: message}
}

func wrapError(code string, status int, message string, cause error) *Error {
	return &Error{Code: code, Status: status, Message: message, cause: cause}
}

var errDisabled = newError(CodeDisabled, http.StatusConflict, "The sandbox is turned off. The user can turn it on in Settings.")

// idPattern is what a PocketBase record ID looks like. Container names
// and conversation folders are only ever built from such IDs, so a malformed
// one can never become a docker argument or a path outside /workspace.
var idPattern = regexp.MustCompile(`^[a-z0-9]{1,64}$`)

func containerName(userID string) string { return "workavera-" + userID }

func volumeName(userID string) string { return "workavera-" + userID + "-workspace" }

// ConversationDir is the folder a conversation works in by default: commands
// run there, relative paths resolve there, and imports land there. The rest
// of /workspace stays reachable through absolute paths.
func ConversationDir(conversationID string) string {
	if !idPattern.MatchString(conversationID) {
		return workspaceDir
	}
	return chatsDir + "/" + conversationID
}

func validUserID(userID string) error {
	if !idPattern.MatchString(userID) {
		return errors.New("invalid sandbox user ID")
	}
	return nil
}

// Register creates the app's sandbox manager and wires its routes, hooks,
// and idle check.
func Register(app core.App) {
	m := newManager(app, newDocker())
	app.Store().Set(managerKey, m)
	m.register()
}

// For returns the app's sandbox manager, or nil when the feature is not
// registered, which callers treat like an unavailable sandbox.
func For(app core.App) *Manager {
	if app == nil {
		return nil
	}
	m, _ := app.Store().Get(managerKey).(*Manager)
	return m
}

func truncate(text string, limit int) string {
	runes := []rune(text)
	if len(runes) <= limit {
		return text
	}
	return string(runes[:limit]) + "…"
}
