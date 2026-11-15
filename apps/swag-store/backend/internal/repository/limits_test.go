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

package repository

import (
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/wso2/wso2-coin/apps/swag-store/backend/internal/store"
)

func TestLimitSinceNeverZeroDate(t *testing.T) {
	now := time.Date(2026, 11, 11, 0, 0, 0, 0, time.UTC)
	if got := limitSince(now, nil); got.Year() < 1970 {
		t.Fatalf("no window = %v; MySQL strict mode rejects a zero date", got)
	}
	days := 30
	if got := limitSince(now, &days); !got.Equal(now.AddDate(0, 0, -30)) {
		t.Fatalf("30-day window = %v", got)
	}
}

func TestTruncateKeepsWholeCharacters(t *testing.T) {
	reason := strings.Repeat("a", 255) + "é and more" // é is two bytes: bytes 255 and 256
	got := truncate(reason, 256)
	if !utf8.ValidString(got) {
		t.Fatalf("cut inside a character: %q", got[250:])
	}
	if got != strings.Repeat("a", 255) {
		t.Fatalf("got %d bytes, want the 255 before the split character", len(got))
	}
	if truncate("short", 256) != "short" {
		t.Fatal("short reason changed")
	}
}

func TestStockOrderSortsByVariant(t *testing.T) {
	lines := []store.OrderLine{
		{ID: "l1", Name: "Hoodie", VariantID: "v-b"},
		{ID: "l2", Name: "Hoodie", VariantID: "v-a"},
		{ID: "l3", Name: "Bag", VariantID: "v-c"},
	}
	got := stockOrder(lines)
	for i, want := range []string{"v-a", "v-b", "v-c"} {
		if got[i].VariantID != want {
			t.Fatalf("lock order = %v, want variant id order", got)
		}
	}
	if lines[0].ID != "l1" {
		t.Fatal("stockOrder changed the caller's slice")
	}
}
