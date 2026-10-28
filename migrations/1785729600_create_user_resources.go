package migrations

import (
	"github.com/pocketbase/pocketbase/core"
	m "github.com/pocketbase/pocketbase/migrations"
	"github.com/pocketbase/pocketbase/tools/types"
)

const (
	userResourcesCollection          = "user_resources"
	resourceDownloadLeasesCollection = "resource_download_leases"

	userResourceMaxFileSize     = 6 * 1024 * 1024
	userResourceMaxMarkdownSize = 8 * 1024 * 1024
)

func init() {
	m.Register(createUserResourcesCollections, dropUserResourcesCollections)
}

func createUserResourcesCollections(app core.App) error {
	users, err := app.FindCollectionByNameOrId(usersCollectionName)
	if err != nil {
		return err
	}

	resources := core.NewBaseCollection(userResourcesCollection)
	ownerRule := `@request.auth.id != "" && owner = @request.auth.id`
	// Owners list, view, and rename through the Records API. Uploading has to
	// detect, hash, and convert the file, and deleting has to reach the
	// download leases, so both go through internal/resources; a hook there also
	// limits updates to display_name.
	resources.ListRule = types.Pointer(ownerRule)
	resources.ViewRule = types.Pointer(ownerRule)
	resources.UpdateRule = types.Pointer(ownerRule)
	resources.Fields.Add(
		&core.RelationField{Name: "owner", CollectionId: users.Id, MaxSelect: 1, Required: true, CascadeDelete: true},
		&core.FileField{Name: "file", MaxSelect: 1, Required: true, MaxSize: userResourceMaxFileSize, Protected: true},
		&core.TextField{Name: "original_name", Required: true, Max: 255},
		&core.TextField{Name: "display_name", Required: true, Max: 255, Presentable: true},
		&core.TextField{Name: "media_type", Required: true, Max: 127},
		&core.NumberField{Name: "size", Required: true, OnlyInt: true, Min: types.Pointer(1.0), Max: types.Pointer(float64(userResourceMaxFileSize))},
		&core.TextField{Name: "sha256", Required: true, Hidden: true, Min: 64, Max: 64, Pattern: "^[a-f0-9]{64}$"},
		&core.TextField{Name: "converted_markdown", Required: true, Max: userResourceMaxMarkdownSize},
		&core.NumberField{Name: "converted_size", Required: true, OnlyInt: true, Min: types.Pointer(1.0), Max: types.Pointer(float64(userResourceMaxMarkdownSize))},
		&core.AutodateField{Name: "created", OnCreate: true},
		&core.AutodateField{Name: "updated", OnCreate: true, OnUpdate: true},
	)
	resources.AddIndex("idx_user_resources_owner_sha256_name", true, "owner, sha256, original_name", "")
	resources.AddIndex("idx_user_resources_owner_updated", false, "owner, updated", "")
	if err := app.Save(resources); err != nil {
		return err
	}

	leases := core.NewBaseCollection(resourceDownloadLeasesCollection)
	// A lease is a bearer credential: creating and revoking it go through
	// internal/resources, and its token hash is never readable.
	leases.ListRule = types.Pointer(ownerRule)
	leases.ViewRule = types.Pointer(ownerRule)
	leases.Fields.Add(
		&core.RelationField{Name: "owner", CollectionId: users.Id, MaxSelect: 1, Required: true, CascadeDelete: true},
		&core.RelationField{Name: "resource", CollectionId: resources.Id, MaxSelect: 1, Required: true, CascadeDelete: true},
		&core.TextField{Name: "token_hash", Required: true, Hidden: true, Min: 64, Max: 64, Pattern: "^[a-f0-9]{64}$"},
		&core.TextField{Name: "purpose", Required: true, Max: 200},
		&core.DateField{Name: "expires_at", Required: true},
		&core.AutodateField{Name: "created", OnCreate: true},
	)
	leases.AddIndex("idx_resource_download_leases_token_hash", true, "token_hash", "")
	leases.AddIndex("idx_resource_download_leases_resource", false, "resource, expires_at", "")
	leases.AddIndex("idx_resource_download_leases_expires_at", false, "expires_at", "")
	return app.Save(leases)
}

func dropUserResourcesCollections(app core.App) error {
	for _, name := range []string{resourceDownloadLeasesCollection, userResourcesCollection} {
		collection, err := app.FindCollectionByNameOrId(name)
		if err != nil {
			return err
		}
		if err := app.Delete(collection); err != nil {
			return err
		}
	}
	return nil
}
