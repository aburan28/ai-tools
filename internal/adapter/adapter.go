package adapter

import (
	"context"
	"errors"

	"agent-hub/internal/model"
)

var ErrUnsupported = errors.New("action not supported")

type SessionAdapter interface {
	Name() string
	List(context.Context) ([]model.AgentSession, error)
	Send(context.Context, string, string) error
	OpenCommand(model.AgentSession) (string, error)
}
