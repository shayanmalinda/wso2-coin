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

package main

import (
	"context"
	"testing"

	"github.com/wso2/wso2-coin/apps/swag-store/backend/internal/repository"
	"github.com/wso2/wso2-coin/apps/swag-store/backend/internal/testdb"
)

// A run that stopped after creating the Hoodie left it a DRAFT with no sizes.
// The next run must finish it, not skip it.
func TestSeedFinishesADraftLeftByAnEarlierRun(t *testing.T) {
	ctx := context.Background()
	s := repository.New(testdb.Open(t))
	cat, err := s.SaveCategory(ctx, actor, repository.Category{Slug: "apparel", Name: "Apparel", Sort: 1})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateProduct(ctx, actor, repository.ProductInput{Name: "WSO2 Hoodie", Slug: "wso2-hoodie",
		CategoryID: cat.ID, Price: 90, RestockPolicy: "RESTOCKABLE"}); err != nil {
		t.Fatal(err)
	}

	for run := 1; run <= 2; run++ { // the second run must change nothing
		if err := seed(ctx, s); err != nil {
			t.Fatalf("run %d: %v", run, err)
		}
		d, err := s.ProductBySlug(ctx, "wso2-hoodie")
		if err != nil || d == nil {
			t.Fatalf("run %d: hoodie = %v, %v", run, d, err)
		}
		sizes := 0
		for _, v := range d.Variants {
			if v.Active {
				sizes++
			}
		}
		if d.Product.Status != "ACTIVE" || sizes != 5 {
			t.Fatalf("run %d: hoodie is %s with %d active variants, want ACTIVE with 5 sizes", run, d.Product.Status, sizes)
		}
	}
	for _, p := range products {
		if d, err := s.ProductBySlug(ctx, p.slug); err != nil || d == nil || d.Product.Status != "ACTIVE" {
			t.Errorf("%s not seeded ACTIVE: %v %v", p.slug, d, err)
		}
	}
}
