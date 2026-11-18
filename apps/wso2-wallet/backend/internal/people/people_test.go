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

package people

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

type fakeSource struct {
	employees []Employee
	err       error
	calls     int
}

func (f *fakeSource) Employees(context.Context) ([]Employee, error) {
	f.calls++
	return f.employees, f.err
}

func staff() []Employee {
	return []Employee{
		{FirstName: "Anna", LastName: "Perera", Email: "anna@wso2.com"},
		{FirstName: "Joanna", LastName: "Silva", Email: "Joanna@wso2.com", Thumbnail: "https://img/j.png"},
		{FirstName: "Sam", LastName: "Annandale", Email: "sam@wso2.com"},
		{FirstName: "Anne", LastName: "Outside", Email: "anne@partner.com"},
		{FirstName: "Me", LastName: "Annable", Email: "me@wso2.com"},
	}
}

func TestSearchRanksPrefixMatchesAndFilters(t *testing.T) {
	dir := NewDirectory(&fakeSource{employees: staff()}, "wso2.com")
	got, err := dir.Search(context.Background(), "  ANN ", "ME@wso2.com")
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	// A first-name match (Anna) comes before a last-name match (Sam Annandale), and
	// both before a match in the middle of a name (Joanna). The partner-domain
	// person and the caller are left out.
	want := []string{"anna@wso2.com", "sam@wso2.com", "joanna@wso2.com"}
	if len(got) != len(want) {
		t.Fatalf("got %d results %+v, want %d", len(got), got, len(want))
	}
	for i, email := range want {
		if got[i].Email != email {
			t.Errorf("result %d = %s, want %s", i, got[i].Email, email)
		}
	}
	if got[0].Name != "Anna Perera" || got[2].Thumbnail != "https://img/j.png" {
		t.Errorf("unexpected fields: %+v", got)
	}
}

func TestSearchNeedsMinimumLength(t *testing.T) {
	src := &fakeSource{employees: staff()}
	dir := NewDirectory(src, "wso2.com")
	got, err := dir.Search(context.Background(), "an", "")
	if err != nil || len(got) != 0 {
		t.Fatalf("short query = %+v, %v; want no results", got, err)
	}
	if src.calls != 0 {
		t.Errorf("short query reached the HR entity %d times", src.calls)
	}
}

func TestSearchCapsResults(t *testing.T) {
	var many []Employee
	for i := 0; i < 30; i++ {
		many = append(many, Employee{FirstName: "Test", LastName: fmt.Sprint(i), Email: fmt.Sprintf("test%d@wso2.com", i)})
	}
	dir := NewDirectory(&fakeSource{employees: many}, "wso2.com")
	got, err := dir.Search(context.Background(), "test", "")
	if err != nil || len(got) != MaxResults {
		t.Fatalf("got %d results, %v; want %d", len(got), err, MaxResults)
	}
}

func TestDirectoryCachesAndSurvivesOutage(t *testing.T) {
	src := &fakeSource{employees: staff()}
	dir := NewDirectory(src, "wso2.com")
	now := time.Now()
	dir.now = func() time.Time { return now }
	ctx := context.Background()

	for i := 0; i < 3; i++ {
		if _, err := dir.Search(ctx, "anna", ""); err != nil {
			t.Fatalf("Search: %v", err)
		}
	}
	if src.calls != 1 {
		t.Fatalf("fetched %d times, want 1 (cached)", src.calls)
	}

	// Once the copy is old, a failing refresh still serves it, and is not retried
	// on every search.
	now = now.Add(cacheTTL + time.Second)
	src.err = errors.New("hr down")
	for i := 0; i < 3; i++ {
		got, err := dir.Search(ctx, "anna", "")
		if err != nil || len(got) == 0 {
			t.Fatalf("Search during outage = %+v, %v; want stale results", got, err)
		}
	}
	if src.calls != 2 {
		t.Errorf("fetched %d times, want 2 (one failed refresh)", src.calls)
	}
}

func TestDirectoryUnavailableWithNoCopy(t *testing.T) {
	src := &fakeSource{err: errors.New("hr down")}
	dir := NewDirectory(src, "wso2.com")
	for i := 0; i < 2; i++ {
		if _, err := dir.Search(context.Background(), "anna", ""); !errors.Is(err, ErrUnavailable) {
			t.Fatalf("Search = %v, want ErrUnavailable", err)
		}
	}
	if src.calls != 1 {
		t.Errorf("fetched %d times, want 1 (failure is not retried at once)", src.calls)
	}
}

func TestClientPagesAndReusesToken(t *testing.T) {
	var tokenCalls, queryCalls atomic.Int32
	mux := http.NewServeMux()
	mux.HandleFunc("POST /token", func(w http.ResponseWriter, r *http.Request) {
		tokenCalls.Add(1)
		id, secret, ok := r.BasicAuth()
		if !ok || id != "id" || secret != "secret" || r.FormValue("grant_type") != "client_credentials" {
			http.Error(w, "bad client", http.StatusUnauthorized)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "tok", "expires_in": 3600})
	})
	mux.HandleFunc("POST /graphql", func(w http.ResponseWriter, r *http.Request) {
		queryCalls.Add(1)
		if r.Header.Get("Authorization") != "Bearer tok" {
			http.Error(w, "no token", http.StatusUnauthorized)
			return
		}
		var body struct {
			Variables struct {
				Offset int            `json:"offset"`
				Filter map[string]any `json:"filter"`
			} `json:"variables"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		if _, ok := body.Variables.Filter["employeeStatus"]; !ok {
			http.Error(w, "filter not sent", http.StatusBadRequest)
			return
		}
		var page []map[string]any
		if body.Variables.Offset == 0 {
			page = []map[string]any{
				{"workEmail": "a@wso2.com", "firstName": "A", "lastName": "One", "employeeThumbnail": nil},
				{"workEmail": "  ", "firstName": "No", "lastName": "Email"},
				{"workEmail": "b@wso2.com", "firstName": "B", "lastName": "Two", "employeeThumbnail": "https://img/b.png"},
			}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"employees": page}})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := NewClient(ClientConfig{Endpoint: srv.URL + "/graphql", TokenURL: srv.URL + "/token", ClientID: "id", ClientSecret: "secret", Filter: map[string]any{"employeeStatus": []string{"Active"}}})
	got, err := c.Employees(context.Background())
	if err != nil {
		t.Fatalf("Employees: %v", err)
	}
	if len(got) != 2 || got[0].Email != "a@wso2.com" || got[1].Thumbnail != "https://img/b.png" {
		t.Fatalf("employees = %+v", got)
	}
	if tokenCalls.Load() != 1 || queryCalls.Load() != 2 {
		t.Errorf("token calls = %d (want 1), query calls = %d (want 2)", tokenCalls.Load(), queryCalls.Load())
	}
}

func TestClientReportsGraphQLErrors(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /token", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "tok", "expires_in": 3600})
	})
	mux.HandleFunc("POST /graphql", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"errors": []map[string]any{{"message": "not allowed"}}})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := NewClient(ClientConfig{Endpoint: srv.URL + "/graphql", TokenURL: srv.URL + "/token", ClientID: "id", ClientSecret: "secret"})
	if _, err := c.Employees(context.Background()); err == nil {
		t.Fatal("Employees succeeded, want the GraphQL error")
	}
}
