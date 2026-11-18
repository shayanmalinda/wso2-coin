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
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// ErrUnavailable means the employee directory could not be read.
var ErrUnavailable = errors.New("employee directory unavailable")

const (
	pageSize = 500
	// maxPages bounds the paging loop if the HR entity keeps returning rows.
	maxPages     = 200
	maxBodyBytes = 16 << 20
	// tokenLeeway renews the access token a little before it expires.
	tokenLeeway = time.Minute
)

const employeesQuery = `query employeesQuery ($filter: EmployeeFilter, $limit: Int, $offset: Int) {
  employees(filter: $filter, limit: $limit, offset: $offset) {
    workEmail
    firstName
    lastName
    employeeThumbnail
  }
}`

// ClientConfig is how to reach the HR entity GraphQL service.
type ClientConfig struct {
	Endpoint     string
	TokenURL     string
	ClientID     string
	ClientSecret string
	Scopes       string
	// Filter is the EmployeeFilter sent with the query; it selects active employees.
	Filter map[string]any
}

// Client reads employees from the HR entity GraphQL service, authenticating with
// OAuth2 client credentials.
type Client struct {
	cfg  ClientConfig
	http *http.Client

	mu          sync.Mutex
	token       string
	tokenExpiry time.Time
}

// NewClient returns a Client for cfg.
func NewClient(cfg ClientConfig) *Client {
	return &Client{cfg: cfg, http: &http.Client{
		Timeout: 20 * time.Second,
		// The requests carry the client secret and the bearer token, and the
		// configured URLs were checked for HTTPS. A redirect could send them
		// elsewhere, such as over plain HTTP, so redirects are not followed and
		// the 3xx response is reported as an unexpected status.
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}}
}

// Employees returns every active employee, reading the list page by page.
func (c *Client) Employees(ctx context.Context) ([]Employee, error) {
	var all []Employee
	for page := 0; page < maxPages; page++ {
		batch, rows, err := c.page(ctx, page*pageSize)
		if err != nil {
			return nil, err
		}
		// The end is an empty page from HR, not an empty list of usable people:
		// a full page of rows without a work email must not stop the paging.
		if rows == 0 {
			return all, nil
		}
		all = append(all, batch...)
	}
	return nil, fmt.Errorf("employee list did not end after %d pages", maxPages)
}

// page reads one page. It returns the usable employees and how many rows HR sent.
func (c *Client) page(ctx context.Context, offset int) ([]Employee, int, error) {
	token, err := c.accessToken(ctx)
	if err != nil {
		return nil, 0, err
	}
	body, err := json.Marshal(map[string]any{
		"query": employeesQuery,
		"variables": map[string]any{
			"filter": c.cfg.Filter,
			"limit":  pageSize,
			"offset": offset,
		},
	})
	if err != nil {
		return nil, 0, fmt.Errorf("encode employees query: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.cfg.Endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, 0, fmt.Errorf("build employees request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)

	var out struct {
		Data struct {
			Employees *[]struct {
				WorkEmail string  `json:"workEmail"`
				FirstName string  `json:"firstName"`
				LastName  string  `json:"lastName"`
				Thumbnail *string `json:"employeeThumbnail"`
			} `json:"employees"`
		} `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := c.do(req, &out); err != nil {
		return nil, 0, fmt.Errorf("query employees: %w", err)
	}
	if len(out.Errors) > 0 {
		return nil, 0, fmt.Errorf("query employees: %s", out.Errors[0].Message)
	}
	if out.Data.Employees == nil {
		// A page without the field is a broken response, not the end of the list.
		return nil, 0, errors.New("query employees: response had no data.employees")
	}
	rows := *out.Data.Employees
	employees := make([]Employee, 0, len(rows))
	for _, e := range rows {
		if strings.TrimSpace(e.WorkEmail) == "" {
			continue
		}
		emp := Employee{FirstName: e.FirstName, LastName: e.LastName, Email: strings.TrimSpace(e.WorkEmail)}
		if e.Thumbnail != nil {
			emp.Thumbnail = *e.Thumbnail
		}
		employees = append(employees, emp)
	}
	return employees, len(rows), nil
}

// accessToken returns a cached client-credentials token, fetching a new one when
// the cached one is missing or about to expire.
func (c *Client) accessToken(ctx context.Context) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.token != "" && time.Now().Before(c.tokenExpiry) {
		return c.token, nil
	}

	form := url.Values{"grant_type": {"client_credentials"}}
	if c.cfg.Scopes != "" {
		form.Set("scope", c.cfg.Scopes)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.cfg.TokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return "", fmt.Errorf("build token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.SetBasicAuth(c.cfg.ClientID, c.cfg.ClientSecret)

	var out struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if err := c.do(req, &out); err != nil {
		return "", fmt.Errorf("fetch token: %w", err)
	}
	if out.AccessToken == "" {
		return "", errors.New("fetch token: response had no access_token")
	}
	lifetime := time.Duration(out.ExpiresIn) * time.Second
	if lifetime <= tokenLeeway {
		// Unknown or very short lifetime: use the token once and fetch again next time.
		lifetime = tokenLeeway
	}
	c.token, c.tokenExpiry = out.AccessToken, time.Now().Add(lifetime-tokenLeeway)
	return c.token, nil
}

// do sends req and decodes a JSON response into dst. The response body is never
// included in the error, since it may carry tokens or personal data.
func (c *Client) do(req *http.Request, dst any) error {
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxBodyBytes))
		return fmt.Errorf("unexpected status %d", resp.StatusCode)
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxBodyBytes)).Decode(dst); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	return nil
}
