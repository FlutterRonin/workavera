package migrations

import (
	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
	m "github.com/pocketbase/pocketbase/migrations"
	"github.com/pocketbase/pocketbase/tools/types"
)

const (
	resourcesMaxFileSizeKey = "resources.max_file_size_mb"
	// resourceFileSizeCeiling is the largest value resources.max_file_size_mb
	// may take. The file field allows it, and the server enforces the
	// configured limit on every upload.
	resourceFileSizeCeiling = 100 * 1024 * 1024
)

func init() {
	m.Register(widenResources, narrowResources)
}

// widenResources makes the resource size limit a setting, 10 MB by default,
// and records why a resource has no Markdown: binary files, and documents
// whose text cannot be extracted, such as scanned PDFs, which are now kept.
func widenResources(app core.App) error {
	resources, err := app.FindCollectionByNameOrId(userResourcesCollection)
	if err != nil {
		return err
	}
	if file, ok := resources.Fields.GetByName("file").(*core.FileField); ok {
		file.MaxSize = resourceFileSizeCeiling
	}
	if size, ok := resources.Fields.GetByName("size").(*core.NumberField); ok {
		size.Max = types.Pointer(float64(resourceFileSizeCeiling))
	}
	resources.Fields.Add(&core.SelectField{
		Name:      "no_text_reason",
		MaxSelect: 1,
		Values:    []string{"binary", "needs_ocr", "encrypted", "empty", "too_complex"},
	})
	if err := app.Save(resources); err != nil {
		return err
	}
	if _, err := app.DB().NewQuery("UPDATE {{" + userResourcesCollection + "}} SET no_text_reason = 'binary' WHERE converted_size = 0").Execute(); err != nil {
		return err
	}

	if existing, _ := app.FindFirstRecordByFilter(configsCollection, "key = {:key}", dbx.Params{"key": resourcesMaxFileSizeKey}); existing != nil {
		return nil
	}
	configs, err := app.FindCollectionByNameOrId(configsCollection)
	if err != nil {
		return err
	}
	record := core.NewRecord(configs)
	record.Set("key", resourcesMaxFileSizeKey)
	record.Set("value", types.JSONRaw("10"))
	record.Set("description", "Largest file accepted in Resources, in megabytes, from 1 to 100. Applies to uploads and to files exported from the sandbox.")
	return app.Save(record)
}

func narrowResources(app core.App) error {
	if record, err := app.FindFirstRecordByFilter(configsCollection, "key = {:key}", dbx.Params{"key": resourcesMaxFileSizeKey}); err == nil {
		if err := app.Delete(record); err != nil {
			return err
		}
	}
	// Documents kept without text could not be stored under the old rules.
	records, err := app.FindRecordsByFilter(userResourcesCollection, "no_text_reason != '' && no_text_reason != 'binary'", "", 0, 0)
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
	resources.Fields.RemoveByName("no_text_reason")
	if file, ok := resources.Fields.GetByName("file").(*core.FileField); ok {
		file.MaxSize = userResourceMaxFileSize
	}
	if size, ok := resources.Fields.GetByName("size").(*core.NumberField); ok {
		size.Max = types.Pointer(float64(userResourceMaxFileSize))
	}
	return app.Save(resources)
}
