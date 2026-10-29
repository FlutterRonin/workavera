package configs

import (
	"encoding/json"
	"errors"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
)

const (
	ResourcesMaxFileSizeKey = "resources.max_file_size_mb"
	// DefaultResourcesMaxFileSizeMB applies while the record is missing.
	DefaultResourcesMaxFileSizeMB = 10
	// MaxResourcesMaxFileSizeMB is the highest value an administrator may
	// set; the file field and request limits are sized for it.
	MaxResourcesMaxFileSizeMB = 100
)

// ResourcesMaxFileSize is the largest resource file accepted, in bytes.
func ResourcesMaxFileSize(app core.App) int64 {
	megabytes := DefaultResourcesMaxFileSizeMB
	if record, err := app.FindFirstRecordByFilter(CollectionName, "key = {:key}", dbx.Params{"key": ResourcesMaxFileSizeKey}); err == nil {
		if value, err := parseResourcesMaxFileSize([]byte(record.GetString("value"))); err == nil {
			megabytes = value
		}
	}
	return int64(megabytes) * 1024 * 1024
}

func parseResourcesMaxFileSize(raw []byte) (int, error) {
	var value float64
	if err := json.Unmarshal(raw, &value); err != nil || value != float64(int(value)) || value < 1 || value > MaxResourcesMaxFileSizeMB {
		return 0, errors.New("must be a whole number of megabytes from 1 to 100")
	}
	return int(value), nil
}
