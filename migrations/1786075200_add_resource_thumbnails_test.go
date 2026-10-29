package migrations

import (
	"slices"
	"testing"

	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tests"
)

func TestResourceThumbnailsMigration(t *testing.T) {
	app, err := tests.NewTestApp()
	if err != nil {
		t.Fatal(err)
	}
	defer app.Cleanup()

	resources, err := app.FindCollectionByNameOrId(userResourcesCollection)
	if err != nil {
		t.Fatal(err)
	}
	file, ok := resources.Fields.GetByName("file").(*core.FileField)
	if !ok || !slices.Equal(file.Thumbs, []string{resourceThumbSize}) || !file.Protected {
		t.Fatalf("resource files need a protected %s thumbnail: %#v", resourceThumbSize, file)
	}
}
