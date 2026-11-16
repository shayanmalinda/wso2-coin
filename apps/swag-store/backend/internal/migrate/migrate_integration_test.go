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

package migrate_test

import (
	"context"
	"sync"
	"testing"

	"github.com/wso2/wso2-coin/apps/swag-store/backend/internal/migrate"
	"github.com/wso2/wso2-coin/apps/swag-store/backend/internal/testdb"
	"github.com/wso2/wso2-coin/apps/swag-store/backend/migrations"
)

func TestMigrationsApplyOnEmptyDB(t *testing.T) {
	db := testdb.OpenEmpty(t)
	ctx := context.Background()

	if err := migrate.CheckCurrent(ctx, db, migrations.FS); err == nil {
		t.Fatal("empty schema passed the startup check")
	}
	applied, err := migrate.Apply(ctx, db, migrations.FS)
	if err != nil {
		t.Fatal(err)
	}
	if len(applied) == 0 {
		t.Fatal("nothing applied")
	}
	again, err := migrate.Apply(ctx, db, migrations.FS)
	if err != nil || len(again) != 0 {
		t.Fatalf("re-run applied %v, %v", again, err)
	}
	if err := migrate.CheckCurrent(ctx, db, migrations.FS); err != nil {
		t.Fatalf("current schema failed the check: %v", err)
	}
}

func TestConcurrentApplyRunsEachFileOnce(t *testing.T) {
	db := testdb.OpenEmpty(t)
	ctx := context.Background()
	var wg sync.WaitGroup
	errs := make([]error, 2)
	applied := make([][]string, 2)
	for i := range errs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			applied[i], errs[i] = migrate.Apply(ctx, db, migrations.FS)
		}()
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("run %d: %v", i, err)
		}
	}
	if len(applied[0])+len(applied[1]) != len(mustFiles(t)) {
		t.Fatalf("applied %v and %v; want each file once", applied[0], applied[1])
	}
}

// MySQL commits each CREATE TABLE on its own, so a run stopped halfway leaves
// some tables and no record. The next run must finish the job.
func TestInterruptedBootstrapReplays(t *testing.T) {
	db := testdb.OpenEmpty(t)
	ctx := context.Background()
	if _, err := migrate.Apply(ctx, db, migrations.FS); err != nil {
		t.Fatal(err)
	}
	first := mustFiles(t)[0]
	for _, q := range []string{
		"DELETE FROM swag_applied_migrations WHERE filename = '" + first + "'",
		"DROP TABLE swag_admin_actions",
	} {
		if _, err := db.ExecContext(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	again, err := migrate.Apply(ctx, db, migrations.FS)
	if err != nil {
		t.Fatalf("replay after an interrupted run: %v", err)
	}
	if len(again) != 1 || again[0] != first {
		t.Fatalf("replayed %v, want %s", again, first)
	}
	if _, err := db.ExecContext(ctx, "SELECT 1 FROM swag_admin_actions LIMIT 1"); err != nil {
		t.Fatalf("missing table not created: %v", err)
	}
}

func mustFiles(t *testing.T) []string {
	t.Helper()
	files, err := migrate.Files(migrations.FS)
	if err != nil {
		t.Fatal(err)
	}
	return files
}
