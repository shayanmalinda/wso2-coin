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

// Package people looks up employees in the HR entity so a wallet owner can pick
// who to share a wallet with by name.
package people

import (
	"context"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/wso2/wso2-coin/apps/wso2-wallet/backend/internal/model"
)

const (
	// MinQueryLength is the shortest search text that returns results, so the
	// directory cannot be listed a letter at a time.
	MinQueryLength = 3
	// MaxResults caps how many people one search returns.
	MaxResults = 10

	cacheTTL = 24 * time.Hour
	// retryAfterFailure is how long a failed refresh is left alone before trying
	// again, so an HR outage does not turn every keystroke into an upstream call.
	retryAfterFailure = time.Minute
	// refreshTimeout bounds one refresh of the whole employee list.
	refreshTimeout = 90 * time.Second
	// firstLoadWait is how long a search waits for the first load of the list
	// before answering that the directory is unavailable. It stays under the
	// server's write deadline so the caller always gets an answer.
	firstLoadWait = 15 * time.Second
)

// Employee is one active employee as read from the HR entity.
type Employee struct {
	FirstName string
	LastName  string
	Email     string
	Thumbnail string
}

// Source returns every active employee.
type Source interface {
	Employees(ctx context.Context) ([]Employee, error)
}

// Directory answers searches from a cached copy of the employee list.
type Directory struct {
	source Source
	domain string
	now    func() time.Time

	mu        sync.Mutex
	employees []Employee
	fetchedAt time.Time
	failedAt  time.Time
	// refreshing is open while a refresh is running; it closes when it ends.
	refreshing chan struct{}
}

// NewDirectory returns a Directory over source that only returns people whose email
// is in domain.
func NewDirectory(source Source, domain string) *Directory {
	return &Directory{source: source, domain: strings.ToLower(strings.TrimSpace(domain)), now: time.Now}
}

// Search returns up to MaxResults people whose name or email contains query,
// leaving out exclude (the caller). People whose first name or email starts with the
// query come first, then those whose last name does, then any other match. A query
// shorter than MinQueryLength returns nothing.
func (d *Directory) Search(ctx context.Context, query, exclude string) ([]model.Employee, error) {
	q := strings.ToLower(strings.TrimSpace(query))
	if utf8.RuneCountInString(q) < MinQueryLength {
		return []model.Employee{}, nil
	}
	employees, err := d.list(ctx)
	if err != nil {
		return nil, err
	}
	exclude = strings.ToLower(strings.TrimSpace(exclude))

	// rank orders matches: lower is better.
	const (
		rankFirst = iota // first name, full name or email starts with the query
		rankLast         // last name starts with the query
		rankContains
	)
	type match struct {
		e    Employee
		rank int
	}
	var matches []match
	for _, e := range employees {
		email := strings.ToLower(e.Email)
		if email == exclude || !d.InCompany(email) {
			continue
		}
		last := strings.ToLower(e.LastName)
		full := strings.ToLower(e.FirstName) + " " + last
		switch {
		case strings.HasPrefix(full, q), strings.HasPrefix(email, q):
			matches = append(matches, match{e, rankFirst})
		case strings.HasPrefix(last, q):
			matches = append(matches, match{e, rankLast})
		case strings.Contains(full, q), strings.Contains(email, q):
			matches = append(matches, match{e, rankContains})
		}
	}
	sort.SliceStable(matches, func(i, j int) bool {
		if matches[i].rank != matches[j].rank {
			return matches[i].rank < matches[j].rank
		}
		return displayName(matches[i].e) < displayName(matches[j].e)
	})
	if len(matches) > MaxResults {
		matches = matches[:MaxResults]
	}
	out := make([]model.Employee, 0, len(matches))
	for _, m := range matches {
		out = append(out, model.Employee{
			Name: displayName(m.e), Email: strings.ToLower(m.e.Email), Thumbnail: m.e.Thumbnail,
		})
	}
	return out, nil
}

// InCompany reports whether email belongs to the directory's organization. With no
// domain configured nobody does, since a bare "@" suffix would match every email.
func (d *Directory) InCompany(email string) bool {
	if d.domain == "" {
		return false
	}
	return strings.HasSuffix(strings.ToLower(strings.TrimSpace(email)), "@"+d.domain)
}

// list returns the cached employees. A missing or old copy is refreshed by one
// shared background refresh that no caller's request can cancel. An old copy keeps
// being served while that refresh runs, and when a refresh fails. With no copy at
// all, a caller waits for the refresh for at most firstLoadWait, then gets
// ErrUnavailable while the refresh carries on for the next caller.
func (d *Directory) list(ctx context.Context) ([]Employee, error) {
	d.mu.Lock()
	now := d.now()
	fresh := d.employees != nil && now.Sub(d.fetchedAt) < cacheTTL
	recentlyFailed := !d.failedAt.IsZero() && now.Sub(d.failedAt) < retryAfterFailure
	if fresh || (recentlyFailed && d.employees != nil) {
		employees := d.employees
		d.mu.Unlock()
		return employees, nil
	}
	if recentlyFailed {
		d.mu.Unlock()
		return nil, ErrUnavailable
	}
	done := d.refreshing
	if done == nil {
		done = make(chan struct{})
		d.refreshing = done
		go d.refresh(done)
	}
	if d.employees != nil {
		employees := d.employees
		d.mu.Unlock()
		return employees, nil
	}
	d.mu.Unlock()

	timer := time.NewTimer(firstLoadWait)
	defer timer.Stop()
	select {
	case <-done:
	case <-timer.C:
		return nil, ErrUnavailable
	case <-ctx.Done():
		return nil, ErrUnavailable
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.employees == nil {
		return nil, ErrUnavailable
	}
	return d.employees, nil
}

// refresh reads the whole employee list and stores it, then releases everyone
// waiting on done. It runs on its own context so a caller who disconnects cannot
// fail it and mark the directory as down for everyone.
func (d *Directory) refresh(done chan struct{}) {
	ctx, cancel := context.WithTimeout(context.Background(), refreshTimeout)
	defer cancel()
	employees, err := d.source.Employees(ctx)

	d.mu.Lock()
	if err != nil {
		d.failedAt = d.now()
		slog.Error("employee directory refresh failed", "err", err)
	} else {
		// nil means "never loaded" to list, so an empty answer is stored as an
		// empty list and is cached like any other.
		if employees == nil {
			employees = []Employee{}
		}
		d.employees, d.fetchedAt, d.failedAt = employees, d.now(), time.Time{}
	}
	d.refreshing = nil
	d.mu.Unlock()
	close(done)
}

func displayName(e Employee) string {
	return strings.TrimSpace(e.FirstName + " " + e.LastName)
}
