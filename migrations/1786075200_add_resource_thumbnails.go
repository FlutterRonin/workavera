package migrations

import (
	"slices"

	"github.com/pocketbase/pocketbase/core"
	m "github.com/pocketbase/pocketbase/migrations"
)

// resourceThumbSize is the image thumbnail Chat shows for a resource: 480
// pixels wide with the original aspect ratio, so a card never loads a
// 6 MB original.
const resourceThumbSize = "480x0"

func init() {
	m.Register(addResourceThumbnails, dropResourceThumbnails)
}

func addResourceThumbnails(app core.App) error {
	return setResourceThumbs(app, []string{resourceThumbSize})
}

func dropResourceThumbnails(app core.App) error {
	return setResourceThumbs(app, nil)
}

func setResourceThumbs(app core.App, thumbs []string) error {
	resources, err := app.FindCollectionByNameOrId(userResourcesCollection)
	if err != nil {
		return err
	}
	file, ok := resources.Fields.GetByName("file").(*core.FileField)
	if !ok || slices.Equal(file.Thumbs, thumbs) {
		return nil
	}
	file.Thumbs = thumbs
	return app.Save(resources)
}
