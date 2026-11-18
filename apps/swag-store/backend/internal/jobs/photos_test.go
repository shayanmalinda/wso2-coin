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
	"slices"
	"testing"
	"time"

	"github.com/wso2/wso2-coin/apps/swag-store/backend/internal/assets"
)

type fakePhotoStorage struct {
	stored  []assets.StoredPhoto
	deleted []string
	failOn  string
}

func (f *fakePhotoStorage) ListPhotos(context.Context) ([]assets.StoredPhoto, error) {
	return f.stored, nil
}
func (f *fakePhotoStorage) Delete(_ context.Context, path string) error {
	if path == f.failOn {
		return errors.New("storage down")
	}
	f.deleted = append(f.deleted, path)
	return nil
}

type fakePathStore map[string]bool

func (f fakePathStore) PhotoPathsInUse(context.Context) (map[string]bool, error) { return f, nil }

func TestPhotoCleanupDeletesOnlyOldUnusedPhotos(t *testing.T) {
	now := time.Date(2026, 11, 20, 4, 0, 0, 0, time.UTC)
	old, fresh := now.Add(-8*24*time.Hour), now.Add(-6*24*time.Hour)
	const (
		used     = "products/0192f3c4-7d1e-7a40-9b2c-5e8f1a2b3c4d/front-00000001.jpg"
		replaced = "products/0192f3c4-7d1e-7a40-9b2c-5e8f1a2b3c4d/front-00000002.jpg"
		unsaved  = "products/0192f3c4-7d1e-7a40-9b2c-5e8f1a2b3c4d/back-00000003.png"
		broken   = "products/0192f3c4-7d1e-7a40-9b2c-5e8f1a2b3c4d/top-00000004.webp"
		foreign  = "products/readme.txt"
	)
	storage := &fakePhotoStorage{failOn: broken, stored: []assets.StoredPhoto{
		{Path: used, Modified: old},
		{Path: replaced, Modified: old},
		{Path: unsaved, Modified: fresh}, // an editor may still save it
		{Path: broken, Modified: old},
		{Path: foreign, Modified: old}, // not a photo name: never ours to delete
	}}
	err := PhotoCleanup(fakePathStore{used: true}, storage, func() time.Time { return now })(context.Background())
	if err == nil {
		t.Fatal("a failed delete was not reported")
	}
	if !slices.Equal(storage.deleted, []string{replaced}) {
		t.Fatalf("deleted = %v, want only %s", storage.deleted, replaced)
	}
}
