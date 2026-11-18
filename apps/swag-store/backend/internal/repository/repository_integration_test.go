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

package repository_test

import (
	"context"
	"errors"
	"slices"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/wso2/wso2-coin/apps/swag-store/backend/internal/orders"
	"github.com/wso2/wso2-coin/apps/swag-store/backend/internal/repository"
	"github.com/wso2/wso2-coin/apps/swag-store/backend/internal/store"
	"github.com/wso2/wso2-coin/apps/swag-store/backend/internal/testdb"
)

var admin = repository.Actor{Email: "admin@wso2.com", UserID: "u-admin"}

type fixture struct {
	s         *repository.Store
	productID string
	variantID string
	officeID  string
}

// newFixture makes an ACTIVE product with one variant holding stock units, and one office.
func newFixture(t *testing.T, stock int) *fixture {
	t.Helper()
	ctx := context.Background()
	s := repository.New(testdb.Open(t))
	cat, err := s.SaveCategory(ctx, admin, repository.Category{Slug: "apparel", Name: "Apparel"})
	if err != nil {
		t.Fatal(err)
	}
	d, err := s.CreateProduct(ctx, admin, repository.ProductInput{Name: "WSO2 Hoodie", Slug: "wso2-hoodie",
		CategoryID: cat.ID, Price: 90, RestockPolicy: "RESTOCKABLE"})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetProductStatus(ctx, admin, d.Product.ID, "ACTIVE", ""); err != nil {
		t.Fatal(err)
	}
	v := d.Variants[0].ID
	if stock > 0 {
		if _, err := s.RecordStockMovement(ctx, admin, v, repository.MoveReceive, stock, "first delivery"); err != nil {
			t.Fatal(err)
		}
	}
	o, err := s.SaveOffice(ctx, admin, repository.Office{Name: "Colombo office", LeadTimeDays: 2, HRLocations: []string{"Colombo"}, Active: true})
	if err != nil {
		t.Fatal(err)
	}
	return &fixture{s: s, productID: d.Product.ID, variantID: v, officeID: o.ID}
}

func (f *fixture) pendingOrder(t *testing.T, email string, qty int) (*repository.Order, error) {
	t.Helper()
	ctx := context.Background()
	if _, err := f.s.AddToCart(ctx, email, f.variantID, qty); err != nil {
		t.Fatal(err)
	}
	return f.s.CreatePendingOrder(ctx, repository.NewOrder{
		ID: uuid.Must(uuid.NewV7()).String(), UserEmail: email, UserID: "u-" + email, UserName: "Test User",
		OfficeID: f.officeID, OfficeName: "Colombo office", WalletAddress: "0xuser", IdempotencyKey: uuid.NewString(),
		Check: func(items []repository.CartItem) (int64, error) {
			var total int64
			for _, it := range items {
				total += it.UnitPrice * int64(it.Quantity)
			}
			return total, nil
		},
	})
}

func (f *fixture) stock(t *testing.T) (onHand, reserved int) {
	t.Helper()
	d, err := f.s.ProductByID(context.Background(), f.productID)
	if err != nil {
		t.Fatal(err)
	}
	return d.Variants[0].OnHand, d.Variants[0].Reserved
}

func TestRemovedProductOrdersStillFulfil(t *testing.T) {
	f := newFixture(t, 3)
	ctx := context.Background()
	o, err := f.pendingOrder(t, "you@wso2.com", 1)
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := f.s.ConfirmOrder(ctx, o.ID); !ok || err != nil {
		t.Fatalf("confirm = %v %v", ok, err)
	}
	if err := f.s.SetProductStatus(ctx, admin, f.productID, "REMOVED", "supplier stopped"); err != nil {
		t.Fatal(err)
	}
	for _, to := range []orders.Status{orders.ReadyForPickup, orders.Collected} {
		if _, err := f.s.TransitionOrder(ctx, o.OrderNo, to, admin, ""); err != nil {
			t.Fatalf("%s after removal: %v", to, err)
		}
	}
	if onHand, reserved := f.stock(t); onHand != 2 || reserved != 0 {
		t.Fatalf("stock = %d/%d", onHand, reserved)
	}
}

func TestTransitionCASConflict(t *testing.T) {
	f := newFixture(t, 3)
	ctx := context.Background()
	o, _ := f.pendingOrder(t, "you@wso2.com", 1)
	_, _ = f.s.ConfirmOrder(ctx, o.ID)
	if _, err := f.s.TransitionOrder(ctx, o.OrderNo, orders.ReadyForPickup, admin, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.TransitionOrder(ctx, o.OrderNo, orders.Collected, admin, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.TransitionOrder(ctx, o.OrderNo, orders.Collected, admin, ""); !errors.Is(err, repository.ErrStateConflict) {
		t.Fatalf("second collect = %v", err)
	}
	d, err := f.s.OrderDetail(ctx, o.OrderNo, "")
	if err != nil {
		t.Fatal(err)
	}
	collected := 0
	for _, e := range d.Events {
		if e.To == string(orders.Collected) {
			collected++
		}
	}
	if collected != 1 {
		t.Fatalf("collected events = %d", collected)
	}
}

func TestFailReleasesStock(t *testing.T) {
	f := newFixture(t, 2)
	ctx := context.Background()
	o, _ := f.pendingOrder(t, "you@wso2.com", 2)
	if _, reserved := f.stock(t); reserved != 2 {
		t.Fatalf("reserved = %d", reserved)
	}
	if ok, err := f.s.FailOrder(ctx, o.ID, repository.Failure{Code: "insufficient_funds", Notify: true}); !ok || err != nil {
		t.Fatalf("fail = %v %v", ok, err)
	}
	if onHand, reserved := f.stock(t); onHand != 2 || reserved != 0 {
		t.Fatalf("stock = %d/%d", onHand, reserved)
	}
	if ok, _ := f.s.ConfirmOrder(ctx, o.ID); ok {
		t.Fatal("a failed order confirmed through the pending path")
	}
}

func TestReconcilerConfirmsLatePayment(t *testing.T) {
	f := newFixture(t, 2)
	ctx := context.Background()
	o, _ := f.pendingOrder(t, "you@wso2.com", 1)
	_, _ = f.s.FailOrder(ctx, o.ID, repository.Failure{Code: "payment_not_found"})
	if err := f.s.ConfirmLatePayment(ctx, o.ID); err != nil {
		t.Fatal(err)
	}
	got, _ := f.s.OrderByID(ctx, o.ID)
	if got.Status != orders.Confirmed {
		t.Fatalf("status = %s", got.Status)
	}
	if onHand, reserved := f.stock(t); onHand != 1 || reserved != 0 {
		t.Fatalf("stock = %d/%d", onHand, reserved)
	}
}

func TestReconcilerConfirmsLatePaymentFlagsNoStock(t *testing.T) {
	f := newFixture(t, 1)
	ctx := context.Background()
	o, _ := f.pendingOrder(t, "you@wso2.com", 1)
	_, _ = f.s.FailOrder(ctx, o.ID, repository.Failure{Code: "payment_not_found"})
	other, _ := f.pendingOrder(t, "other@wso2.com", 1)
	_, _ = f.s.ConfirmOrder(ctx, other.ID)
	if err := f.s.ConfirmLatePayment(ctx, o.ID); !errors.Is(err, repository.ErrNoStockForLatePayment) {
		t.Fatalf("late payment without stock = %v", err)
	}
	got, _ := f.s.OrderByID(ctx, o.ID)
	if got.Status != orders.Failed || got.AttentionReason != "late_payment_no_stock" {
		t.Fatalf("order = %+v", got)
	}
}

func TestLateConfirmationClearsOrderedCartLines(t *testing.T) {
	f := newFixture(t, 2)
	ctx := context.Background()
	o, _ := f.pendingOrder(t, "you@wso2.com", 1)
	_, _ = f.s.FailOrder(ctx, o.ID, repository.Failure{Code: "payment_not_found"})
	if err := f.s.ConfirmLatePayment(ctx, o.ID); err != nil {
		t.Fatal(err)
	}
	if n, _ := f.s.CartItemCount(ctx, "you@wso2.com"); n != 0 {
		t.Fatalf("cart still holds %d paid units", n)
	}
}

func TestLateConfirmationKeepsLinesAddedAfterOrder(t *testing.T) {
	f := newFixture(t, 3)
	ctx := context.Background()
	o, _ := f.pendingOrder(t, "you@wso2.com", 1)
	_, _ = f.s.FailOrder(ctx, o.ID, repository.Failure{Code: "payment_not_found"})
	items, err := f.s.CartForQuote(ctx, "you@wso2.com")
	if err != nil || len(items) != 1 {
		t.Fatalf("cart = %v %v", items, err)
	}
	if err := f.s.RemoveCartLine(ctx, "you@wso2.com", items[0].LineID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.AddToCart(ctx, "you@wso2.com", f.variantID, 2); err != nil {
		t.Fatal(err)
	}
	if err := f.s.ConfirmLatePayment(ctx, o.ID); err != nil {
		t.Fatal(err)
	}
	if n, _ := f.s.CartItemCount(ctx, "you@wso2.com"); n != 2 {
		t.Fatalf("cart = %d units, want the 2 added after the order", n)
	}
}

func TestReminderClaimSkipsCollectedOrder(t *testing.T) {
	f := newFixture(t, 2)
	ctx := context.Background()
	o, _ := f.pendingOrder(t, "you@wso2.com", 1)
	_, _ = f.s.ConfirmOrder(ctx, o.ID)
	for _, to := range []orders.Status{orders.ReadyForPickup, orders.Collected} {
		if _, err := f.s.TransitionOrder(ctx, o.OrderNo, to, admin, ""); err != nil {
			t.Fatal(err)
		}
	}
	// The order was listed as READY_FOR_PICKUP, then collected before the claim.
	n, err := store.New(f.s.DB()).MarkReminderSent(ctx, o.ID)
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatal("claimed a reminder for a collected order")
	}
}

func TestPatchOfficeAppliesToCurrentRow(t *testing.T) {
	f := newFixture(t, 0)
	ctx := context.Background()
	holding, release := make(chan struct{}), make(chan struct{})
	first := make(chan error, 1)
	go func() {
		_, err := f.s.PatchOffice(ctx, admin, f.officeID, func(o *repository.Office) error {
			close(holding)
			<-release
			o.Active = false
			return nil
		})
		first <- err
	}()
	t.Cleanup(func() {
		select {
		case <-release:
		default:
			close(release)
		}
	})
	select {
	case <-holding:
	case err := <-first:
		t.Fatalf("first patch ended before its callback: %v", err)
	case <-time.After(10 * time.Second):
		t.Fatal("first patch never started")
	}
	second := make(chan error, 1)
	go func() {
		_, err := f.s.PatchOffice(ctx, admin, f.officeID, func(o *repository.Office) error {
			o.Hours = "9:00-17:00"
			return nil
		})
		second <- err
	}()
	time.Sleep(200 * time.Millisecond) // the second patch now waits on the row lock
	close(release)
	for _, ch := range []chan error{first, second} {
		select {
		case err := <-ch:
			if err != nil {
				t.Fatal(err)
			}
		case <-time.After(10 * time.Second):
			t.Fatal("patch did not finish")
		}
	}
	got, err := f.s.Office(ctx, f.officeID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Active || got.Hours != "9:00-17:00" {
		t.Fatalf("office = active %v, hours %q; want both patches kept", got.Active, got.Hours)
	}
}

func TestNotifyOnReceiveAndRemovalClosesAlerts(t *testing.T) {
	f := newFixture(t, 0)
	ctx := context.Background()
	if _, err := f.s.CreateAlert(ctx, "a@wso2.com", f.variantID); err != nil {
		t.Fatal(err)
	}
	id2, _ := f.s.CreateAlert(ctx, "a@wso2.com", f.variantID)
	if alerts, _ := f.s.Alerts(ctx, "a@wso2.com"); len(alerts) != 1 || alerts[0].ID != id2 {
		t.Fatalf("duplicate pending alert: %+v", alerts)
	}
	res, err := f.s.RecordStockMovement(ctx, admin, f.variantID, repository.MoveReceive, 5, "restock")
	if err != nil || res.Notified != 1 {
		t.Fatalf("receive = %+v %v", res, err)
	}
	if alerts, _ := f.s.Alerts(ctx, "a@wso2.com"); alerts[0].Status != "NOTIFIED" {
		t.Fatalf("alert = %+v", alerts[0])
	}

	g := newFixture(t, 0)
	if _, err := g.s.CreateAlert(ctx, "b@wso2.com", g.variantID); err != nil {
		t.Fatal(err)
	}
	if err := g.s.SetProductStatus(ctx, admin, g.productID, "REMOVED", ""); err != nil {
		t.Fatal(err)
	}
	if alerts, _ := g.s.Alerts(ctx, "b@wso2.com"); alerts[0].Status != "CLOSED" {
		t.Fatalf("alert after removal = %+v", alerts[0])
	}
	emails, err := g.s.ClaimDueEmails(ctx)
	if err != nil {
		t.Fatal(err)
	}
	removed := 0
	for _, e := range emails {
		if e.Template == "product_removed" {
			removed++
		}
	}
	if removed != 1 {
		t.Fatalf("product_removed emails = %d", removed)
	}
}

func TestDeleteDraftOnlyWhenNeverOrdered(t *testing.T) {
	f := newFixture(t, 1)
	ctx := context.Background()
	if err := f.s.DeleteDraft(ctx, admin, f.productID); !errors.Is(err, repository.ErrStateConflict) {
		t.Fatalf("delete ACTIVE = %v", err)
	}
	d, err := f.s.CreateProduct(ctx, admin, repository.ProductInput{Name: "Draft", Slug: "draft", Price: 10, RestockPolicy: "LIMITED"})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.s.DeleteDraft(ctx, admin, d.Product.ID); err != nil {
		t.Fatal(err)
	}
}

func TestSetOptionsKeepsSKUs(t *testing.T) {
	f := newFixture(t, 0)
	ctx := context.Background()
	size := func(vals ...string) []repository.OptionInput {
		in := repository.OptionInput{Name: "Size"}
		for _, v := range vals {
			in.Values = append(in.Values, repository.OptionValueInput{Value: v})
		}
		return []repository.OptionInput{in}
	}
	d, err := f.s.SetOptions(ctx, admin, f.productID, size("S", "M"))
	if err != nil {
		t.Fatal(err)
	}
	skus := map[string]string{}
	for _, v := range d.Variants {
		if v.Active {
			skus[v.Options] = v.SKU
		}
	}
	if len(skus) != 2 {
		t.Fatalf("variants = %+v", d.Variants)
	}
	d, err = f.s.SetOptions(ctx, admin, f.productID, size("M", "L"))
	if err != nil {
		t.Fatal(err)
	}
	active := map[string]string{}
	for _, v := range d.Variants {
		if v.Active {
			active[v.Options] = v.SKU
		}
	}
	if active["M"] != skus["M"] || active["L"] == "" || active["S"] != "" {
		t.Fatalf("after change: %+v (before %+v)", active, skus)
	}
}

// A replacement shows only the options and values it names; the variants of
// dropped values stay readable, inactive, with their labels.
func TestSetOptionsShowsOnlyTheCurrentDefinition(t *testing.T) {
	f := newFixture(t, 0)
	ctx := context.Background()
	opt := func(name string, vals ...string) repository.OptionInput {
		in := repository.OptionInput{Name: name}
		for _, v := range vals {
			in.Values = append(in.Values, repository.OptionValueInput{Value: v})
		}
		return in
	}
	definition := func(d *repository.ProductDetail) string {
		var parts []string
		for _, o := range d.Options {
			var vals []string
			for _, v := range o.Values {
				vals = append(vals, v.Value)
			}
			parts = append(parts, o.Name+"="+strings.Join(vals, ","))
		}
		return strings.Join(parts, " ")
	}
	labels := func(d *repository.ProductDetail) (active, inactive []string) {
		for _, v := range d.Variants {
			if v.Active {
				active = append(active, v.Options)
			} else {
				inactive = append(inactive, v.Options)
			}
		}
		sort.Strings(active)
		sort.Strings(inactive)
		return active, inactive
	}

	if _, err := f.s.SetOptions(ctx, admin, f.productID, []repository.OptionInput{opt("Size", "M", "L")}); err != nil {
		t.Fatal(err)
	}
	d, err := f.s.SetOptions(ctx, admin, f.productID, []repository.OptionInput{opt("Size", "M")})
	if err != nil {
		t.Fatal(err)
	}
	if got := definition(d); got != "Size=M" {
		t.Errorf("after dropping L, options = %q, want Size=M", got)
	}
	if active, inactive := labels(d); strings.Join(active, ";") != "M" || !slices.Contains(inactive, "L") {
		t.Errorf("variants active %v inactive %v, want M active and L kept inactive", active, inactive)
	}
	read, err := f.s.ProductByID(ctx, f.productID)
	if err != nil {
		t.Fatal(err)
	}
	if got := definition(read); got != "Size=M" {
		t.Errorf("read back, options = %q, want Size=M", got)
	}

	d, err = f.s.SetOptions(ctx, admin, f.productID, []repository.OptionInput{opt("Colour", "Black"), opt("Size", "M")})
	if err != nil {
		t.Fatal(err)
	}
	if got := definition(d); got != "Colour=Black Size=M" {
		t.Errorf("after adding Colour, options = %q", got)
	}
	d, err = f.s.SetOptions(ctx, admin, f.productID, []repository.OptionInput{opt("Size", "M", "L")})
	if err != nil {
		t.Fatal(err)
	}
	if got := definition(d); got != "Size=M,L" {
		t.Errorf("after dropping Colour and adding L back, options = %q, want Size=M,L", got)
	}
	if active, _ := labels(d); strings.Join(active, ";") != "L;M" {
		t.Errorf("active variants = %v, want L and M", active)
	}
}

// An audit row without detail (category_deleted, order_note_added) must read
// back as detail null, not fail the whole audit page.
func TestAuditEventWithoutDetailReads(t *testing.T) {
	s := repository.New(testdb.Open(t))
	ctx := context.Background()
	c, err := s.SaveCategory(ctx, admin, repository.Category{Slug: "drinkware", Name: "Drinkware"})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteCategory(ctx, admin, c.ID); err != nil {
		t.Fatal(err)
	}
	events, _, err := s.AuditEvents(ctx, 50, 0)
	if err != nil {
		t.Fatalf("AuditEvents: %v", err)
	}
	for _, e := range events {
		if e.Action == "category_deleted" {
			if string(e.Detail) != "null" {
				t.Fatalf("detail = %s, want null", e.Detail)
			}
			return
		}
	}
	t.Fatal("category_deleted event missing")
}

// Two edits of different fields must not undo each other: the second patch
// waits for the first one's row lock and sees its change.
func TestUpdateProductPatchesTheLockedRow(t *testing.T) {
	f := newFixture(t, 0)
	ctx := context.Background()
	holding, release := make(chan struct{}), newGate(t)
	var once sync.Once
	first := make(chan error, 1)
	go func() {
		_, err := f.s.UpdateProduct(ctx, admin, f.productID, func(in *repository.ProductInput) error {
			in.Price = 95
			once.Do(func() { close(holding) })
			<-release.c
			return nil
		})
		first <- err
	}()
	waitForCallback(t, holding, first)
	second := make(chan error, 1)
	var seenPrice int64
	go func() {
		_, err := f.s.UpdateProduct(ctx, admin, f.productID, func(in *repository.ProductInput) error {
			seenPrice = in.Price
			in.Name = "WSO2 Hoodie v2"
			return nil
		})
		second <- err
	}()
	time.Sleep(200 * time.Millisecond) // the second edit is now waiting for the row lock
	release.open()
	if err := <-first; err != nil {
		t.Fatal(err)
	}
	if err := <-second; err != nil {
		t.Fatal(err)
	}
	d, err := f.s.ProductByID(ctx, f.productID)
	if err != nil {
		t.Fatal(err)
	}
	if seenPrice != 95 || d.Product.Price != 95 || d.Product.Name != "WSO2 Hoodie v2" {
		t.Fatalf("second patch saw price %d; product = %q at %d, want both edits", seenPrice, d.Product.Name, d.Product.Price)
	}
}

// gate holds a patch callback until open is called. The test's cleanup opens
// it too, so a failed test never leaves the callback blocked.
type gate struct {
	c    chan struct{}
	once sync.Once
}

func newGate(t *testing.T) *gate {
	g := &gate{c: make(chan struct{})}
	t.Cleanup(g.open)
	return g
}

func (g *gate) open() { g.once.Do(func() { close(g.c) }) }

// waitForCallback waits until the first edit's patch callback holds the row.
// It fails the test when the edit ends before its callback runs (a lock or
// read error) or nothing happens within 10 seconds.
func waitForCallback(t *testing.T, holding <-chan struct{}, first <-chan error) {
	t.Helper()
	select {
	case <-holding:
	case err := <-first:
		t.Fatalf("the first edit ended before its patch ran: %v", err)
	case <-time.After(10 * time.Second):
		t.Fatal("the first edit's patch never ran")
	}
}

// Two edits of different category fields must not undo each other.
func TestUpdateCategoryPatchesTheLockedRow(t *testing.T) {
	s := repository.New(testdb.Open(t))
	ctx := context.Background()
	c, err := s.SaveCategory(ctx, admin, repository.Category{Slug: "drinkware", Name: "Drinkware", Sort: 1})
	if err != nil {
		t.Fatal(err)
	}
	holding, release := make(chan struct{}), newGate(t)
	var once sync.Once
	first := make(chan error, 1)
	go func() {
		_, err := s.UpdateCategory(ctx, admin, c.ID, func(cat *repository.Category) error {
			cat.Name = "Cups and bottles"
			once.Do(func() { close(holding) })
			<-release.c
			return nil
		})
		first <- err
	}()
	waitForCallback(t, holding, first)
	second := make(chan error, 1)
	var seenName string
	go func() {
		_, err := s.UpdateCategory(ctx, admin, c.ID, func(cat *repository.Category) error {
			seenName = cat.Name
			cat.Sort = 7
			return nil
		})
		second <- err
	}()
	time.Sleep(200 * time.Millisecond) // the second edit is now waiting for the row lock
	release.open()
	if err := <-first; err != nil {
		t.Fatal(err)
	}
	if err := <-second; err != nil {
		t.Fatal(err)
	}
	got, err := s.Category(ctx, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	if seenName != "Cups and bottles" || got.Name != "Cups and bottles" || got.Sort != 7 {
		t.Fatalf("second patch saw %q; category = %+v, want both edits", seenName, got)
	}
}

func TestUpdateCategoryErrors(t *testing.T) {
	s := repository.New(testdb.Open(t))
	ctx := context.Background()
	invalid := errors.New("invalid")
	a, err := s.SaveCategory(ctx, admin, repository.Category{Slug: "apparel", Name: "Apparel"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.SaveCategory(ctx, admin, repository.Category{Slug: "tech", Name: "Tech"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.UpdateCategory(ctx, admin, a.ID, func(c *repository.Category) error { c.Name = "X"; return invalid }); !errors.Is(err, invalid) {
		t.Errorf("patch error = %v, want it returned", err)
	}
	if _, err := s.UpdateCategory(ctx, admin, a.ID, func(c *repository.Category) error { c.Slug = "tech"; return nil }); !errors.Is(err, repository.ErrConflict) {
		t.Errorf("duplicate slug = %v, want ErrConflict", err)
	}
	if _, err := s.UpdateCategory(ctx, admin, uuid.NewString(), func(*repository.Category) error { return nil }); !errors.Is(err, repository.ErrNotFound) {
		t.Errorf("missing category = %v, want ErrNotFound", err)
	}
	if got, _ := s.Category(ctx, a.ID); got.Name != "Apparel" || got.Slug != "apparel" {
		t.Errorf("a failed patch wrote %+v", got)
	}
}

func TestUpdateProductPatchErrorWritesNothing(t *testing.T) {
	f := newFixture(t, 0)
	ctx := context.Background()
	invalid := errors.New("invalid")
	_, err := f.s.UpdateProduct(ctx, admin, f.productID, func(in *repository.ProductInput) error {
		in.Price = 1
		return invalid
	})
	if !errors.Is(err, invalid) {
		t.Fatalf("err = %v, want the patch error", err)
	}
	if d, _ := f.s.ProductByID(ctx, f.productID); d.Product.Price != 90 {
		t.Fatalf("price = %d after a failed patch", d.Product.Price)
	}
	if _, err := f.s.UpdateProduct(ctx, admin, uuid.NewString(), func(*repository.ProductInput) error { return nil }); !errors.Is(err, repository.ErrNotFound) {
		t.Fatalf("unknown product: err = %v", err)
	}
}

// A variant always costs at least 1 coin: price + priceDelta >= 1, whether the
// variant or the product price changes.
func TestVariantPriceStaysPositive(t *testing.T) {
	f := newFixture(t, 0)
	ctx := context.Background()
	delta := func(d int64) repository.VariantInput { return repository.VariantInput{PriceDelta: &d} }
	if err := f.s.UpdateVariant(ctx, admin, f.productID, f.variantID, delta(-90)); !errors.Is(err, repository.ErrPriceTooLow) {
		t.Fatalf("delta -90 on a 90-coin product: err = %v, want ErrPriceTooLow", err)
	}
	if err := f.s.UpdateVariant(ctx, admin, f.productID, f.variantID, delta(-89)); err != nil {
		t.Fatalf("delta -89: %v", err)
	}
	_, err := f.s.UpdateProduct(ctx, admin, f.productID, func(in *repository.ProductInput) error { in.Price = 89; return nil })
	if !errors.Is(err, repository.ErrPriceTooLow) {
		t.Fatalf("price 89 with delta -89: err = %v, want ErrPriceTooLow", err)
	}
	if d, _ := f.s.ProductByID(ctx, f.productID); d.Product.Price != 90 || d.Variants[0].PriceDelta != -89 {
		t.Fatalf("product = %d, delta = %d after the refused edits", d.Product.Price, d.Variants[0].PriceDelta)
	}
}

// A job holds its lock connection while it runs and does its work on the
// main pool, so a one-connection main pool must not stall it.
func TestJobLockWorksWithAOneConnectionPool(t *testing.T) {
	db := testdb.Open(t)
	s := repository.New(db)
	db.SetMaxOpenConns(1)
	s.UseLockPool(testdb.SecondPool(t, db))
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ran, err := s.WithJobLock(ctx, "stats", func(ctx context.Context) error {
		if _, err := s.GetJobState(ctx, "stats"); err != nil {
			return err
		}
		return s.SaveJobState(ctx, "stats", repository.JobState{Status: "ok"})
	})
	if !ran || err != nil {
		t.Fatalf("ran = %v, err = %v", ran, err)
	}
}

// The photo cleanup keeps every path a product uses, a removed product's too.
func TestPhotoPathsInUse(t *testing.T) {
	f := newFixture(t, 0)
	ctx := context.Background()
	front := "products/" + f.productID + "/front-3f9a2c1d.jpg"
	if _, err := f.s.UpdateProduct(ctx, admin, f.productID, func(in *repository.ProductInput) error {
		in.Images = []repository.ImageInput{{View: "front", Path: front}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := f.s.SetProductStatus(ctx, admin, f.productID, "REMOVED", "gone"); err != nil {
		t.Fatal(err)
	}
	inUse, err := f.s.PhotoPathsInUse(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(inUse) != 1 || !inUse[front] {
		t.Fatalf("in use = %v, want only %s", inUse, front)
	}
}

// Photos are stored one per view and read back in gallery order, and an
// upload is audited without touching the product.
func TestPhotosStoredInViewOrder(t *testing.T) {
	f := newFixture(t, 0)
	ctx := context.Background()
	back := "products/" + f.productID + "/back-0a1b2c3d.png"
	front := "products/" + f.productID + "/front-3f9a2c1d.jpg"
	if _, err := f.s.UpdateProduct(ctx, admin, f.productID, func(in *repository.ProductInput) error {
		in.Images = []repository.ImageInput{{View: "back", Path: back, Alt: "Logo"}, {View: "front", Path: front}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	d, err := f.s.ProductByID(ctx, f.productID)
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Images) != 2 || d.Images[0].View != "front" || d.Images[1].Path != back || d.Images[1].Alt != "Logo" {
		t.Fatalf("images = %+v", d.Images)
	}
	cards, _, err := f.s.SearchProducts(ctx, repository.ProductQuery{Status: "ACTIVE", Sort: "name", Limit: 10})
	if err != nil || len(cards) != 1 || cards[0].ImagePath != front {
		t.Fatalf("card = %+v, %v", cards, err)
	}

	if err := f.s.RecordPhotoUpload(ctx, admin, f.productID, repository.PhotoUploadDetail{View: "top", Path: "p", Bytes: 9, ContentType: "image/png"}); err != nil {
		t.Fatal(err)
	}
	events, _, err := f.s.AuditEvents(ctx, 50, 0)
	if err != nil {
		t.Fatal(err)
	}
	found := 0
	for _, e := range events {
		if e.Action == "product_image_uploaded" {
			found++
		}
	}
	if found != 1 {
		t.Fatalf("product_image_uploaded events = %d", found)
	}
}
