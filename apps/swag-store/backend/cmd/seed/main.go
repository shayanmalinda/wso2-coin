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

// Command seed loads the launch catalog (the 7 Phase 1 rewards, design §2.1)
// and the pickup offices. It is idempotent: a category, product or office that
// already exists (by slug or name) is left alone, except a product an earlier
// run left as a DRAFT, which it finishes. Products are created ACTIVE with no
// stock; Catalog Managers receive stock in the admin app.
//
// The sizes, restock policies and office list are placeholders until People
// Ops confirms them (design Q11).
package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"time"

	_ "github.com/go-sql-driver/mysql" // registers the "mysql" driver

	"github.com/wso2/wso2-coin/apps/swag-store/backend/internal/assets"
	"github.com/wso2/wso2-coin/apps/swag-store/backend/internal/dbconn"
	"github.com/wso2/wso2-coin/apps/swag-store/backend/internal/httpsec"
	"github.com/wso2/wso2-coin/apps/swag-store/backend/internal/repository"
)

type seedProduct struct {
	name, slug, category string
	price                int64
	policy               string
	sizes                []string
}

var categories = []repository.Category{
	{Slug: "apparel", Name: "Apparel", Sort: 1},
	{Slug: "drinkware", Name: "Drinkware", Sort: 2},
	{Slug: "tech", Name: "Tech", Sort: 3},
	{Slug: "stationery", Name: "Stationery", Sort: 4},
	{Slug: "everyday", Name: "Everyday", Sort: 5},
}

var products = []seedProduct{
	{"WSO2 Umbrella", "wso2-umbrella", "everyday", 40, "RESTOCKABLE", nil},
	{"WSO2 Notebook", "wso2-notebook", "stationery", 40, "RESTOCKABLE", nil},
	{"WSO2 Laptop Pouch", "wso2-laptop-pouch", "tech", 50, "RESTOCKABLE", nil},
	{"WSO2 Tumbler", "wso2-tumbler", "drinkware", 50, "RESTOCKABLE", nil},
	{"WSO2 Powerbank", "wso2-powerbank", "tech", 70, "RESTOCKABLE", nil},
	{"WSO2 Hoodie", "wso2-hoodie", "apparel", 90, "RESTOCKABLE", []string{"S", "M", "L", "XL", "XXL"}},
	{"WSO2 Bluetooth Speaker", "wso2-bluetooth-speaker", "tech", 150, "LIMITED", nil},
}

var offices = []repository.Office{
	{Name: "Colombo office", Country: "Sri Lanka", CollectionPoint: "Reception", Hours: "Mon–Fri 9:00–17:00",
		LeadTimeDays: 2, HRLocations: []string{"Colombo"}, Active: true, Sort: 1},
}

var actor = repository.Actor{Email: "seed", UserID: "seed"}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "seed:", err)
		os.Exit(1)
	}
}

func dsn() (string, error) {
	mc, err := dbconn.ToolConfig(os.Getenv)
	if err != nil {
		return "", err
	}
	mc.ParseTime, mc.Loc = true, time.UTC
	return mc.FormatDSN(), nil
}

func run() error {
	d, err := dsn()
	if err != nil {
		return err
	}
	db, err := sql.Open("mysql", d)
	if err != nil {
		return err
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	if err := seed(ctx, repository.New(db)); err != nil {
		return err
	}
	return photoContainer(ctx)
}

// seed adds whatever of the launch catalog and offices is missing.
func seed(ctx context.Context, s *repository.Store) error {
	existingCats, err := s.Categories(ctx)
	if err != nil {
		return err
	}
	catID := map[string]string{}
	for _, c := range existingCats {
		catID[c.Slug] = c.ID
	}
	for _, c := range categories {
		if catID[c.Slug] != "" {
			continue
		}
		saved, err := s.SaveCategory(ctx, actor, c)
		if err != nil {
			return fmt.Errorf("category %s: %w", c.Slug, err)
		}
		catID[c.Slug] = saved.ID
		fmt.Println("category", c.Slug)
	}

	for _, p := range products {
		existing, err := s.ProductBySlug(ctx, p.slug)
		if err != nil {
			return err
		}
		var id string
		switch {
		case existing == nil:
			d, err := s.CreateProduct(ctx, actor, repository.ProductInput{Name: p.name, Slug: p.slug, CategoryID: catID[p.category],
				Price: p.price, RestockPolicy: p.policy})
			if err != nil {
				return fmt.Errorf("product %s: %w", p.slug, err)
			}
			id = d.Product.ID
		case existing.Product.Status == "DRAFT":
			// A seeded product is DRAFT only when an earlier run stopped before
			// publishing it: finish it. Setting the same sizes again keeps them.
			id = existing.Product.ID
		default:
			continue
		}
		if len(p.sizes) > 0 {
			opt := repository.OptionInput{Name: "Size"}
			for _, v := range p.sizes {
				opt.Values = append(opt.Values, repository.OptionValueInput{Value: v})
			}
			if _, err := s.SetOptions(ctx, actor, id, []repository.OptionInput{opt}); err != nil {
				return fmt.Errorf("options %s: %w", p.slug, err)
			}
		}
		if err := s.SetProductStatus(ctx, actor, id, "ACTIVE", ""); err != nil {
			return fmt.Errorf("publish %s: %w", p.slug, err)
		}
		fmt.Println("product", p.slug)
	}

	existingOffices, err := s.Offices(ctx, false)
	if err != nil {
		return err
	}
	have := map[string]bool{}
	for _, o := range existingOffices {
		have[o.Name] = true
	}
	for _, o := range offices {
		if have[o.Name] {
			continue
		}
		if _, err := s.SaveOffice(ctx, actor, o); err != nil {
			return fmt.Errorf("office %s: %w", o.Name, err)
		}
		fmt.Println("office", o.Name)
	}
	return nil
}

// photoContainer creates the photo container in the local emulator. It runs
// only with ASSETS_ACCOUNT_KEY, which the server accepts only on loopback; in
// Azure, ops create the container.
func photoContainer(ctx context.Context) error {
	accountURL, key := os.Getenv("ASSETS_ACCOUNT_URL"), os.Getenv("ASSETS_ACCOUNT_KEY")
	if accountURL == "" || key == "" {
		return nil
	}
	if !httpsec.IsLoopbackURL(accountURL) {
		return errors.New("ASSETS_ACCOUNT_KEY is only for the local emulator on a loopback ASSETS_ACCOUNT_URL")
	}
	name := os.Getenv("ASSETS_CONTAINER")
	if name == "" {
		name = "swag-store-assets"
	}
	c, err := assets.New(assets.Config{AccountURL: accountURL, Container: name, AccountKey: key})
	if err != nil {
		return err
	}
	if err := c.EnsureContainer(ctx); err != nil {
		return err
	}
	fmt.Println("photo container", name)
	return nil
}
