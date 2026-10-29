package tools

import (
	"context"
	"errors"
	"strings"

	"charm.land/fantasy"
	"github.com/pocketbase/pocketbase/core"

	workdocs "github.com/xusenlin/workavera/internal/docs"
	"github.com/xusenlin/workavera/internal/resources"
)

const maxSentFiles = 6

type chatSendFileItem struct {
	Kind string `json:"kind" enum:"resource,doc" description:"resource for a file in Resources, doc for a document"`
	ID   string `json:"id" description:"Resource ID or document ID"`
}

type chatSendFilesInput struct {
	Files []chatSendFileItem `json:"files" description:"One to 6 resources or documents to show the user, in the order to show them"`
}

// SentFile is one file shown to the user. Names, types, and sizes come from
// the stored records, never from the model.
type SentFile struct {
	Kind       string `json:"kind"`
	ResourceID string `json:"resourceId,omitempty"`
	DocID      string `json:"docId,omitempty"`
	Name       string `json:"name"`
	MediaType  string `json:"mediaType,omitempty"`
	Size       int64  `json:"size,omitempty"`
	DocKind    string `json:"docKind,omitempty"`
}

// newChatSendFilesTool shows resources and documents to the user as file
// cards in the reply. It needs no approval: the files stay inside Workavera
// and are only shown to their own user.
func newChatSendFilesTool(app core.App, actorID string) fantasy.AgentTool {
	return fantasy.NewAgentTool(
		"chat_send_files",
		"Show up to 6 of the user's resources or documents in your reply as file cards they can preview, open, and download; images get a thumbnail that enlarges on click. Use it to deliver files you produced, such as exports from the sandbox, or files the user asks to see. Only the final deliverables, not intermediate files. Then reply briefly without repeating file paths, IDs, or links.",
		func(ctx context.Context, input chatSendFilesInput, _ fantasy.ToolCall) (fantasy.ToolResponse, error) {
			files, err := sendFiles(ctx, app, actorID, input.Files)
			if err != nil {
				var resourceErr *resources.Error
				var inputErr sendFilesError
				switch {
				case errors.As(err, &resourceErr):
					return fantasy.NewTextErrorResponse(resourceErr.Code + ": " + resourceErr.Message), nil
				case errors.As(err, &inputErr):
					return fantasy.NewTextErrorResponse(string(inputErr)), nil
				}
				app.Logger().Error("assistant chat_send_files failed", "actorId", actorID, "error", err)
				return fantasy.NewTextErrorResponse("The files could not be shown."), nil
			}
			return resourcesToolResponse(app, actorID, "send files", map[string]any{"files": files}, nil)
		},
	)
}

// sendFilesError is a failure whose message is safe to return to the model.
type sendFilesError string

func (e sendFilesError) Error() string { return string(e) }

const errInvalidSentFiles = sendFilesError(`invalid_input: files must name 1 to 6 distinct items, each with kind "resource" or "doc" and an id`)

func sendFiles(ctx context.Context, app core.App, actorID string, items []chatSendFileItem) ([]SentFile, error) {
	if len(items) == 0 || len(items) > maxSentFiles {
		return nil, errInvalidSentFiles
	}
	seen := map[string]bool{}
	files := make([]SentFile, 0, len(items))
	for _, item := range items {
		id := strings.TrimSpace(item.ID)
		key := item.Kind + ":" + id
		if id == "" || seen[key] {
			return nil, errInvalidSentFiles
		}
		seen[key] = true
		switch item.Kind {
		case "resource":
			snapshots, err := resources.Snapshots(app, actorID, []string{id})
			if err != nil {
				return nil, err
			}
			snapshot := snapshots[0]
			files = append(files, SentFile{Kind: "resource", ResourceID: snapshot.ResourceID, Name: snapshot.Name, MediaType: snapshot.MediaType, Size: snapshot.Size})
		case "doc":
			doc, err := workdocs.Get(ctx, app, actorID, id)
			if err != nil {
				if errors.Is(err, workdocs.ErrNotFound) {
					return nil, sendFilesError("document_not_found: document " + id + " does not exist or is not visible to the user")
				}
				return nil, err
			}
			files = append(files, SentFile{Kind: "doc", DocID: doc.ID, Name: doc.Title, DocKind: doc.Kind})
		default:
			return nil, errInvalidSentFiles
		}
	}
	return files, nil
}
