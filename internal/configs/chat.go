package configs

import (
	"encoding/json"
	"errors"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
)

const (
	ChatMaxStepsKey     = "chat.max_steps"
	DefaultChatMaxSteps = 80
	minChatMaxSteps     = 2
	maxChatMaxSteps     = 500
)

// ChatMaxSteps is the most steps one Chat response may take, the last of
// which answers without tools. A missing record means the default.
func ChatMaxSteps(app core.App) int {
	record, err := app.FindFirstRecordByFilter(CollectionName, "key = {:key}", dbx.Params{"key": ChatMaxStepsKey})
	if err != nil {
		return DefaultChatMaxSteps
	}
	steps, err := parseChatMaxSteps([]byte(record.GetString("value")))
	if err != nil {
		return DefaultChatMaxSteps
	}
	return steps
}

func parseChatMaxSteps(raw []byte) (int, error) {
	var value float64
	if err := json.Unmarshal(raw, &value); err != nil || value != float64(int(value)) || value < minChatMaxSteps || value > maxChatMaxSteps {
		return 0, errors.New("must be a whole number from 2 to 500")
	}
	return int(value), nil
}
