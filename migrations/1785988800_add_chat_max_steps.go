package migrations

import (
	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
	m "github.com/pocketbase/pocketbase/migrations"
	"github.com/pocketbase/pocketbase/tools/types"
)

const chatMaxStepsKey = "chat.max_steps"

func init() {
	m.Register(addChatMaxSteps, dropChatMaxSteps)
}

func addChatMaxSteps(app core.App) error {
	if existing, _ := app.FindFirstRecordByFilter(configsCollection, "key = {:key}", dbx.Params{"key": chatMaxStepsKey}); existing != nil {
		return nil
	}
	configs, err := app.FindCollectionByNameOrId(configsCollection)
	if err != nil {
		return err
	}
	record := core.NewRecord(configs)
	record.Set("key", chatMaxStepsKey)
	record.Set("value", types.JSONRaw("80"))
	record.Set("description", "Most steps one Chat response may take, from 2 to 500. Each model call is a step; the last step cannot call tools, so a response that reaches the limit still ends with a summary of what was done and what is left.")
	return app.Save(record)
}

func dropChatMaxSteps(app core.App) error {
	record, err := app.FindFirstRecordByFilter(configsCollection, "key = {:key}", dbx.Params{"key": chatMaxStepsKey})
	if err != nil {
		return nil
	}
	return app.Delete(record)
}
