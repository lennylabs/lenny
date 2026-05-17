// SPDX-License-Identifier: MIT

// Package ofrep is a minimal OpenFeature Remote Evaluation Protocol
// (OFREP) client. The §10.7 ExperimentRouter uses it to resolve a
// `mode: external` experiment when the deployer configures an OFREP
// endpoint rather than an in-process OpenFeature SDK provider.
//
// The client performs the single-flag evaluation POST and classifies
// the outcome. Any failure — a transport error, a non-2xx status, or
// an OFREP errorCode — is the §10.7 targeting_failed condition: no
// experiment assignment is made and the session runs the base runtime.
package ofrep

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// DefaultTimeout bounds a single flag evaluation.
const DefaultTimeout = 5 * time.Second

// Options configures a Client.
type Options struct {
	// BaseURL is the OFREP service root, e.g. https://flags.example.com.
	// Required.
	BaseURL string
	// Timeout bounds a single evaluation when HTTPClient is not set.
	// Zero selects DefaultTimeout.
	Timeout time.Duration
	// HTTPClient overrides the default client. When set, Timeout is
	// ignored — the caller owns the client's transport and timeout.
	HTTPClient *http.Client
}

// Client evaluates flags against an OFREP endpoint. It is safe for
// concurrent use.
type Client struct {
	baseURL string
	http    *http.Client
}

// New returns a Client for the OFREP service at opts.BaseURL.
func New(opts Options) (*Client, error) {
	base := strings.TrimRight(strings.TrimSpace(opts.BaseURL), "/")
	if base == "" {
		return nil, fmt.Errorf("ofrep: BaseURL is required")
	}
	hc := opts.HTTPClient
	if hc == nil {
		timeout := opts.Timeout
		if timeout <= 0 {
			timeout = DefaultTimeout
		}
		hc = &http.Client{Timeout: timeout}
	}
	return &Client{baseURL: base, http: hc}, nil
}

// Result is a successful OFREP flag evaluation. The §10.7 caller feeds
// Variant and Value to experiment.ResolveExternalVariant.
type Result struct {
	Key     string
	Value   any
	Variant string
	Reason  string
}

// EvalError is an OFREP flag-evaluation failure. Code carries the OFREP
// errorCode when the provider returned one (FLAG_NOT_FOUND,
// PARSE_ERROR, TARGETING_KEY_MISSING, GENERAL, etc.); it is empty for a
// non-2xx response that carried no errorCode. Status is the HTTP
// status. Any EvalError is the §10.7 targeting_failed condition.
type EvalError struct {
	FlagKey string
	Status  int
	Code    string
	Detail  string
}

func (e *EvalError) Error() string {
	if e.Code != "" {
		return fmt.Sprintf("ofrep: flag %q evaluation failed (%s): %s", e.FlagKey, e.Code, e.Detail)
	}
	return fmt.Sprintf("ofrep: flag %q evaluation failed with HTTP %d", e.FlagKey, e.Status)
}

// Evaluate resolves flagKey against the OFREP endpoint with evalContext
// as the targeting context. A transport failure returns a wrapped
// error; an OFREP-level failure returns a *EvalError. Both are the
// §10.7 targeting_failed condition.
func (c *Client) Evaluate(ctx context.Context, flagKey string, evalContext map[string]any) (Result, error) {
	reqBody, err := json.Marshal(map[string]any{"context": evalContext})
	if err != nil {
		return Result{}, fmt.Errorf("ofrep: encode request: %w", err)
	}
	endpoint := c.baseURL + "/ofrep/v1/evaluate/flags/" + url.PathEscape(flagKey)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(reqBody))
	if err != nil {
		return Result{}, fmt.Errorf("ofrep: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return Result{}, fmt.Errorf("ofrep: evaluate flag %q: %w", flagKey, err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))

	var decoded struct {
		Key          string `json:"key"`
		Value        any    `json:"value"`
		Variant      string `json:"variant"`
		Reason       string `json:"reason"`
		ErrorCode    string `json:"errorCode"`
		ErrorDetails string `json:"errorDetails"`
	}
	// A decode failure on a 2xx response is itself an evaluation failure;
	// on a non-2xx response the status alone classifies the error.
	decodeErr := json.Unmarshal(body, &decoded)
	if resp.StatusCode != http.StatusOK || decoded.ErrorCode != "" || decodeErr != nil {
		ee := &EvalError{FlagKey: flagKey, Status: resp.StatusCode, Code: decoded.ErrorCode, Detail: decoded.ErrorDetails}
		if decodeErr != nil && resp.StatusCode == http.StatusOK {
			ee.Code = "PARSE_ERROR"
			ee.Detail = decodeErr.Error()
		}
		return Result{}, ee
	}
	return Result{Key: decoded.Key, Value: decoded.Value, Variant: decoded.Variant, Reason: decoded.Reason}, nil
}
