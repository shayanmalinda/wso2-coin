// Copyright (c) 2026 WSO2 LLC. (https://www.wso2.com).
//
// WSO2 LLC. licenses this file to you under the Apache License,
// Version 2.0 (the "License"); you may not use this file except
// in compliance with the License.
// You may obtain a copy of the License at
//
// http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing,
// software distributed under the License is distributed on an
// "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY
// KIND, either express or implied.  See the License for the
// specific language governing permissions and limitations
// under the License.

package jobs

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/wso2/wso2-coin/apps/swag-store/backend/internal/assets"
	"github.com/wso2/wso2-coin/apps/swag-store/backend/internal/catalog"
)

// PhotoGrace is how long a photo no product uses is kept: the editor uploads
// a photo before the product is saved, and may sit unsaved for a while.
const PhotoGrace = 7 * 24 * time.Hour

// PhotoStorage lists and deletes stored photos (assets.Client).
type PhotoStorage interface {
	ListPhotos(ctx context.Context) ([]assets.StoredPhoto, error)
	Delete(ctx context.Context, path string) error
}

// PhotoPathStore says which photo paths products use (repository.Store).
type PhotoPathStore interface {
	PhotoPathsInUse(ctx context.Context) (map[string]bool, error)
}

// PhotoCleanup is the nightly job that deletes photos no product uses (one a
// save replaced or removed, one never saved, one whose upload record failed)
// once they are older than PhotoGrace. It lists the photos before it reads the
// paths in use, so a photo saved in between counts as in use. Names that are
// not photo names are never touched. One failed delete doesn't stop the rest.
func PhotoCleanup(store PhotoPathStore, photos PhotoStorage, now func() time.Time) func(context.Context) error {
	return func(ctx context.Context) error {
		stored, err := photos.ListPhotos(ctx)
		if err != nil {
			return err
		}
		inUse, err := store.PhotoPathsInUse(ctx)
		if err != nil {
			return err
		}
		cutoff := now().Add(-PhotoGrace)
		deleted := 0
		var errs []error
		for _, p := range stored {
			if inUse[p.Path] || !p.Modified.Before(cutoff) {
				continue
			}
			if _, _, ok := catalog.ParsePhotoPath(p.Path); !ok {
				continue
			}
			if err := photos.Delete(ctx, p.Path); err != nil {
				errs = append(errs, err)
				continue
			}
			deleted++
		}
		slog.InfoContext(ctx, "photo_cleanup", "stored", len(stored), "deleted", deleted, "failed", len(errs))
		return errors.Join(errs...)
	}
}
