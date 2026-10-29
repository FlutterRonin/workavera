package agent

import "context"

type Request struct {
	SystemPrompt   string
	Messages       []Message
	Model          ModelConfig
	ActorID        string
	ConversationID string
	UserMessageID  string
	Approval       ApprovalHandler
	// MaxSteps bounds the steps of the run, the last of which answers
	// without tools; zero means the default.
	MaxSteps int
}

type ToolScope struct {
	ActorID        string
	ConversationID string
	UserMessageID  string
}

type EmitFunc func(context.Context, StreamChunk) error

type Runner interface {
	Stream(context.Context, Request, EmitFunc) (Result, error)
}
