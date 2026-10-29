package migrations

import (
	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
	m "github.com/pocketbase/pocketbase/migrations"
	"github.com/pocketbase/pocketbase/tools/types"
)

const (
	sandboxImageKey = "sandbox.image"
	sandboxImage2   = `"ghcr.io/xusenlin/workavera-sandbox:2"`
	sandboxImage3   = `"ghcr.io/xusenlin/workavera-sandbox:3"`
)

func init() {
	m.Register(
		func(app core.App) error { return moveSandboxImage(app, sandboxImage2, sandboxImage3) },
		func(app core.App) error { return moveSandboxImage(app, sandboxImage3, sandboxImage2) },
	)
}

// moveSandboxImage moves sandbox.image to the image with document, OCR, and
// data tools, but only while it still holds the previous default: an
// administrator's own image is left alone.
func moveSandboxImage(app core.App, from, to string) error {
	record, err := app.FindFirstRecordByFilter(configsCollection, "key = {:key}", dbx.Params{"key": sandboxImageKey})
	if err != nil || record.GetString("value") != from {
		return nil
	}
	record.Set("value", types.JSONRaw(to))
	return app.Save(record)
}
