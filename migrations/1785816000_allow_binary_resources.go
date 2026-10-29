package migrations

import (
	"github.com/pocketbase/pocketbase/core"
	m "github.com/pocketbase/pocketbase/migrations"
	"github.com/pocketbase/pocketbase/tools/types"
)

func init() {
	m.Register(allowBinaryResources, disallowBinaryResources)
}

// allowBinaryResources lets a resource have no Markdown: images and ZIP
// archives are stored and downloaded without any conversion, so their
// converted_markdown is empty and converted_size is 0.
func allowBinaryResources(app core.App) error {
	resources, err := app.FindCollectionByNameOrId(userResourcesCollection)
	if err != nil {
		return err
	}
	markdown, ok := resources.Fields.GetByName("converted_markdown").(*core.TextField)
	if ok {
		markdown.Required = false
	}
	size, ok := resources.Fields.GetByName("converted_size").(*core.NumberField)
	if ok {
		size.Required = false
		size.Min = types.Pointer(0.0)
	}
	return app.Save(resources)
}

// disallowBinaryResources deletes the resources without Markdown before
// requiring it again, since they could not be stored under the old rules.
func disallowBinaryResources(app core.App) error {
	records, err := app.FindRecordsByFilter(userResourcesCollection, "converted_size = 0", "", 0, 0)
	if err != nil {
		return err
	}
	for _, record := range records {
		if err := app.Delete(record); err != nil {
			return err
		}
	}
	resources, err := app.FindCollectionByNameOrId(userResourcesCollection)
	if err != nil {
		return err
	}
	if markdown, ok := resources.Fields.GetByName("converted_markdown").(*core.TextField); ok {
		markdown.Required = true
	}
	if size, ok := resources.Fields.GetByName("converted_size").(*core.NumberField); ok {
		size.Required = true
		size.Min = types.Pointer(1.0)
	}
	return app.Save(resources)
}
