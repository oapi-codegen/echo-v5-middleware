package echomiddleware

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/openapi3filter"
	"github.com/labstack/echo/v5"
	echomiddleware "github.com/labstack/echo/v5/middleware"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

//go:embed test_spec.yaml
var testSchema []byte

func doGet(t *testing.T, e *echo.Echo, rawURL string) *httptest.ResponseRecorder {
	u, err := url.Parse(rawURL)
	if err != nil {
		t.Fatalf("Invalid url: %s", rawURL)
	}

	r, err := http.NewRequest(http.MethodGet, u.String(), nil)
	if err != nil {
		t.Fatalf("Could not construct a request: %s", rawURL)
	}
	r.Header.Set("accept", "application/json")
	r.Header.Set("host", u.Host)

	tt := httptest.NewRecorder()

	e.ServeHTTP(tt, r)

	return tt
}

func doPost(t *testing.T, e *echo.Echo, rawURL string, jsonBody any) *httptest.ResponseRecorder {
	u, err := url.Parse(rawURL)
	if err != nil {
		t.Fatalf("Invalid url: %s", rawURL)
	}

	body, err := json.Marshal(jsonBody)
	if err != nil {
		t.Fatalf("Could not marshal request body: %v", err)
	}

	r, err := http.NewRequest(http.MethodPost, u.String(), bytes.NewReader(body))
	if err != nil {
		t.Fatalf("Could not construct a request for URL %s: %v", rawURL, err)
	}
	r.Header.Set("accept", "application/json")
	r.Header.Set("content-type", "application/json")
	r.Header.Set("host", u.Host)

	tt := httptest.NewRecorder()

	e.ServeHTTP(tt, r)

	return tt
}

func TestOapiRequestValidator(t *testing.T) {
	spec, err := openapi3.NewLoader().LoadFromData(testSchema)
	require.NoError(t, err, "Error initializing OpenAPI spec")

	// Create a new echo router
	e := echo.New()

	// Set up an authenticator to check authenticated function. It will allow
	// access to "someScope", but disallow others.
	options := Options{
		ErrorHandler: func(c *echo.Context, err *echo.HTTPError) error {
			return c.String(err.Code, "test: "+err.Error())
		},
		Options: openapi3filter.Options{
			AuthenticationFunc: func(c context.Context, input *openapi3filter.AuthenticationInput) error {
				// The echo context should be propagated into here.
				eCtx := GetEchoContext(c)
				assert.NotNil(t, eCtx)
				// As should user data
				assert.EqualValues(t, "hi!", GetUserData(c))

				for _, s := range input.Scopes {
					if s == "someScope" {
						return nil
					}
					if s == "unauthorized" {
						return echo.ErrUnauthorized
					}
				}
				return errors.New("forbidden")
			},
		},
		UserData: "hi!",
	}

	// Install our OpenApi based request validator
	e.Use(OapiRequestValidatorWithOptions(spec, &options))

	called := false

	// Install a request handler for /resource. We want to make sure it doesn't
	// get called.
	e.GET("/resource", func(c *echo.Context) error {
		called = true
		return nil
	})
	// Let's send the request to the wrong server, this should return 404
	{
		rec := doGet(t, e, "http://not.test.hostname/resource")
		assert.Equal(t, http.StatusNotFound, rec.Code)
		assert.False(t, called, "Handler should not have been called")
	}

	// Let's send a good request, it should pass
	{
		rec := doGet(t, e, "http://test.hostname/resource")
		assert.Equal(t, http.StatusOK, rec.Code)
		assert.True(t, called, "Handler should have been called")
		called = false
	}

	// Send an out-of-spec parameter
	{
		rec := doGet(t, e, "http://test.hostname/resource?id=500")
		assert.Equal(t, http.StatusBadRequest, rec.Code)
		assert.False(t, called, "Handler should not have been called")
		called = false
	}

	// Send a bad parameter type
	{
		rec := doGet(t, e, "http://test.hostname/resource?id=foo")
		assert.Equal(t, http.StatusBadRequest, rec.Code)
		assert.False(t, called, "Handler should not have been called")
		called = false
	}

	// Send a request with the wrong HTTP method
	{
		rec := doPost(t, e, "http://test.hostname/multiparamresource", nil)
		assert.Equal(t, http.StatusMethodNotAllowed, rec.Code)
		assert.False(t, called, "Handler should not have been called")
		called = false
	}

	// Add a handler for the POST message
	e.POST("/resource", func(c *echo.Context) error {
		called = true
		return c.NoContent(http.StatusNoContent)
	})

	called = false
	// Send a good request body
	{
		body := struct {
			Name string `json:"name"`
		}{
			Name: "Marcin",
		}
		rec := doPost(t, e, "http://test.hostname/resource", body)
		assert.Equal(t, http.StatusNoContent, rec.Code)
		assert.True(t, called, "Handler should have been called")
		called = false
	}

	// Send a malformed body
	{
		body := struct {
			Name int `json:"name"`
		}{
			Name: 7,
		}
		rec := doPost(t, e, "http://test.hostname/resource", body)
		assert.Equal(t, http.StatusBadRequest, rec.Code)
		assert.False(t, called, "Handler should not have been called")
		called = false
	}

	e.GET("/protected_resource", func(c *echo.Context) error {
		called = true
		return c.NoContent(http.StatusNoContent)

	})

	// Call a protected function to which we have access
	{
		rec := doGet(t, e, "http://test.hostname/protected_resource")
		assert.Equal(t, http.StatusNoContent, rec.Code)
		assert.True(t, called, "Handler should have been called")
		called = false
	}

	e.GET("/protected_resource2", func(c *echo.Context) error {
		called = true
		return c.NoContent(http.StatusNoContent)
	})
	// Call a protected function to which we dont have access
	{
		rec := doGet(t, e, "http://test.hostname/protected_resource2")
		assert.Equal(t, http.StatusForbidden, rec.Code)
		assert.False(t, called, "Handler should not have been called")
		called = false
	}

	e.GET("/protected_resource_401", func(c *echo.Context) error {
		called = true
		return c.NoContent(http.StatusNoContent)
	})
	// Call a protected function without credentials
	{
		rec := doGet(t, e, "http://test.hostname/protected_resource_401")
		assert.Equal(t, http.StatusUnauthorized, rec.Code)
		assert.Equal(t, "test: code=401, message=Unauthorized", rec.Body.String())
		assert.False(t, called, "Handler should not have been called")
		called = false
	}
}

func TestOapiRequestValidatorWithOptionsMultiError(t *testing.T) {
	spec, err := openapi3.NewLoader().LoadFromData(testSchema)
	require.NoError(t, err, "Error initializing OpenAPI spec")

	// Create a new echo router
	e := echo.New()

	// Set up an authenticator to check authenticated function. It will allow
	// access to "someScope", but disallow others.
	options := Options{
		Options: openapi3filter.Options{
			ExcludeRequestBody:    false,
			ExcludeResponseBody:   false,
			IncludeResponseStatus: true,
			MultiError:            true,
		},
	}

	// register middleware
	e.Use(OapiRequestValidatorWithOptions(spec, &options))

	called := false

	// Install a request handler for /resource. We want to make sure it doesn't
	// get called.
	e.GET("/multiparamresource", func(c *echo.Context) error {
		called = true
		return nil
	})

	// Let's send a good request, it should pass
	{
		rec := doGet(t, e, "http://test.hostname/multiparamresource?id=50&id2=50")
		assert.Equal(t, http.StatusOK, rec.Code)
		assert.True(t, called, "Handler should have been called")
		called = false
	}

	// Let's send a request with a missing parameter, it should return
	// a bad status
	{
		rec := doGet(t, e, "http://test.hostname/multiparamresource?id=50")
		assert.Equal(t, http.StatusBadRequest, rec.Code)
		body, err := io.ReadAll(rec.Body)
		if assert.NoError(t, err) {
			assert.Contains(t, string(body), "parameter \\\"id2\\\"")
			assert.Contains(t, string(body), "value is required but missing")
		}
		assert.False(t, called, "Handler should not have been called")
		called = false
	}

	// Let's send a request with a 2 missing parameters, it should return
	// a bad status
	{
		rec := doGet(t, e, "http://test.hostname/multiparamresource")
		assert.Equal(t, http.StatusBadRequest, rec.Code)
		body, err := io.ReadAll(rec.Body)
		if assert.NoError(t, err) {
			assert.Contains(t, string(body), "parameter \\\"id\\\"")
			assert.Contains(t, string(body), "value is required but missing")
			assert.Contains(t, string(body), "parameter \\\"id2\\\"")
			assert.Contains(t, string(body), "value is required but missing")
		}
		assert.False(t, called, "Handler should not have been called")
		called = false
	}

	// Let's send a request with a 1 missing parameter, and another outside
	// or the parameters. It should return a bad status
	{
		rec := doGet(t, e, "http://test.hostname/multiparamresource?id=500")
		assert.Equal(t, http.StatusBadRequest, rec.Code)
		body, err := io.ReadAll(rec.Body)
		if assert.NoError(t, err) {
			assert.Contains(t, string(body), "parameter \\\"id\\\"")
			assert.Contains(t, string(body), "number must be at most 100")
			assert.Contains(t, string(body), "parameter \\\"id2\\\"")
			assert.Contains(t, string(body), "value is required but missing")
		}
		assert.False(t, called, "Handler should not have been called")
		called = false
	}

	// Let's send a request with a parameters that do not meet spec. It should
	// return a bad status
	{
		rec := doGet(t, e, "http://test.hostname/multiparamresource?id=abc&id2=1")
		assert.Equal(t, http.StatusBadRequest, rec.Code)
		body, err := io.ReadAll(rec.Body)
		if assert.NoError(t, err) {
			assert.Contains(t, string(body), "parameter \\\"id\\\"")
			assert.Contains(t, string(body), "value abc: an invalid integer: invalid syntax")
			assert.Contains(t, string(body), "parameter \\\"id2\\\"")
			assert.Contains(t, string(body), "number must be at least 10")
		}
		assert.False(t, called, "Handler should not have been called")
		called = false
	}
}

func TestOapiRequestValidatorWithOptionsMultiErrorAndCustomHandler(t *testing.T) {
	spec, err := openapi3.NewLoader().LoadFromData(testSchema)
	require.NoError(t, err, "Error initializing OpenAPI spec")

	// Create a new echo router
	e := echo.New()

	// Set up an authenticator to check authenticated function. It will allow
	// access to "someScope", but disallow others.
	options := Options{
		Options: openapi3filter.Options{
			ExcludeRequestBody:    false,
			ExcludeResponseBody:   false,
			IncludeResponseStatus: true,
			MultiError:            true,
		},
		MultiErrorHandler: func(me openapi3.MultiError) *echo.HTTPError {
			return &echo.HTTPError{
				Code:    http.StatusTeapot,
				Message: me.Error(),
			}
		},
	}

	// register middleware
	e.Use(OapiRequestValidatorWithOptions(spec, &options))

	called := false

	// Install a request handler for /resource. We want to make sure it doesn't
	// get called.
	e.GET("/multiparamresource", func(c *echo.Context) error {
		called = true
		return nil
	})

	// Let's send a good request, it should pass
	{
		rec := doGet(t, e, "http://test.hostname/multiparamresource?id=50&id2=50")
		assert.Equal(t, http.StatusOK, rec.Code)
		assert.True(t, called, "Handler should have been called")
		called = false
	}

	// Let's send a request with a missing parameter, it should return
	// a bad status
	{
		rec := doGet(t, e, "http://test.hostname/multiparamresource?id=50")
		assert.Equal(t, http.StatusTeapot, rec.Code)
		body, err := io.ReadAll(rec.Body)
		if assert.NoError(t, err) {
			assert.Contains(t, string(body), "parameter \\\"id2\\\"")
			assert.Contains(t, string(body), "value is required but missing")
		}
		assert.False(t, called, "Handler should not have been called")
		called = false
	}

	// Let's send a request with a 2 missing parameters, it should return
	// a bad status
	{
		rec := doGet(t, e, "http://test.hostname/multiparamresource")
		assert.Equal(t, http.StatusTeapot, rec.Code)
		body, err := io.ReadAll(rec.Body)
		if assert.NoError(t, err) {
			assert.Contains(t, string(body), "parameter \\\"id\\\"")
			assert.Contains(t, string(body), "value is required but missing")
			assert.Contains(t, string(body), "parameter \\\"id2\\\"")
			assert.Contains(t, string(body), "value is required but missing")
		}
		assert.False(t, called, "Handler should not have been called")
		called = false
	}

	// Let's send a request with a 1 missing parameter, and another outside
	// or the parameters. It should return a bad status
	{
		rec := doGet(t, e, "http://test.hostname/multiparamresource?id=500")
		assert.Equal(t, http.StatusTeapot, rec.Code)
		body, err := io.ReadAll(rec.Body)
		if assert.NoError(t, err) {
			assert.Contains(t, string(body), "parameter \\\"id\\\"")
			assert.Contains(t, string(body), "number must be at most 100")
			assert.Contains(t, string(body), "parameter \\\"id2\\\"")
			assert.Contains(t, string(body), "value is required but missing")
		}
		assert.False(t, called, "Handler should not have been called")
		called = false
	}

	// Let's send a request with a parameters that do not meet spec. It should
	// return a bad status
	{
		rec := doGet(t, e, "http://test.hostname/multiparamresource?id=abc&id2=1")
		assert.Equal(t, http.StatusTeapot, rec.Code)
		body, err := io.ReadAll(rec.Body)
		if assert.NoError(t, err) {
			assert.Contains(t, string(body), "parameter \\\"id\\\"")
			assert.Contains(t, string(body), "value abc: an invalid integer: invalid syntax")
			assert.Contains(t, string(body), "parameter \\\"id2\\\"")
			assert.Contains(t, string(body), "number must be at least 10")
		}
		assert.False(t, called, "Handler should not have been called")
		called = false
	}
}

func TestOapiRequestValidatorWithPrefix(t *testing.T) {
	spec, err := openapi3.NewLoader().LoadFromData(testSchema)
	require.NoError(t, err, "Error initializing OpenAPI spec")

	// Create a new echo router
	e := echo.New()

	options := Options{
		SilenceServersWarning: true,
		Prefix:                "/api",
	}

	// Install our OpenApi based request validator
	e.Use(OapiRequestValidatorWithOptions(spec, &options))

	called := false

	// Register handler under the prefixed path (as echo sees it)
	e.GET("/api/resource", func(c *echo.Context) error {
		called = true
		// The original request path should be preserved for the handler
		assert.Equal(t, "/api/resource", c.Request().URL.Path)
		return c.NoContent(http.StatusOK)
	})

	// A request to /api/resource should validate against /resource in the spec
	{
		rec := doGet(t, e, "http://test.hostname/api/resource")
		assert.Equal(t, http.StatusOK, rec.Code)
		assert.True(t, called, "Handler should have been called")
		called = false
	}

	// A request with query params should also validate correctly
	{
		rec := doGet(t, e, "http://test.hostname/api/resource?id=50")
		assert.Equal(t, http.StatusOK, rec.Code)
		assert.True(t, called, "Handler should have been called")
		called = false
	}

	// An out-of-spec parameter should still be rejected
	{
		rec := doGet(t, e, "http://test.hostname/api/resource?id=500")
		assert.Equal(t, http.StatusBadRequest, rec.Code)
		assert.False(t, called, "Handler should not have been called")
	}
}

// newRawJSONPost builds a POST whose body is sent verbatim, so a test can
// compare the exact bytes that reach the handler.
func newRawJSONPost(target, body string) *http.Request {
	r := httptest.NewRequest(http.MethodPost, target, strings.NewReader(body))
	r.Header.Set("content-type", "application/json")
	return r
}

// prefixedHandlerResult records what a handler mounted under a prefix saw.
type prefixedHandlerResult struct {
	called  bool
	path    string
	rawPath string
	body    []byte
	readErr error
	// validated is the body's ValidatedValue(), when it has one.
	validated    any
	hasValidated bool
}

func (r *prefixedHandlerResult) handler(status int) echo.HandlerFunc {
	return func(c *echo.Context) error {
		req := c.Request()
		r.called = true
		r.path, r.rawPath = req.URL.Path, req.URL.RawPath
		if vv, ok := req.Body.(interface{ ValidatedValue() any }); ok {
			r.validated, r.hasValidated = vv.ValidatedValue(), true
		}
		if req.Body != nil {
			r.body, r.readErr = io.ReadAll(req.Body)
		}
		return c.NoContent(status)
	}
}

// TestOapiRequestValidatorWithPrefixRequestBody checks that, with
// Options.Prefix set, the request body openapi3filter reads during validation
// is restored on the request the handler receives, rather than on a copy of it.
// See https://github.com/oapi-codegen/echo-v5-middleware/issues/16.
func TestOapiRequestValidatorWithPrefixRequestBody(t *testing.T) {
	// Not JSON-canonical on purpose: the handler must see these exact bytes,
	// not a re-encoding of the decoded value.
	const body = `{ "name" : "x" }`

	t.Run("body validation", func(t *testing.T) {
		spec, err := openapi3.NewLoader().LoadFromData(testSchema)
		require.NoError(t, err)

		e := echo.New()
		e.Use(OapiRequestValidatorWithOptions(spec, &Options{
			SilenceServersWarning: true,
			Prefix:                "/api",
		}))
		var got prefixedHandlerResult
		e.POST("/api/resource", got.handler(http.StatusNoContent))

		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, newRawJSONPost("http://test.hostname/api/resource", body))

		require.Equal(t, http.StatusNoContent, rec.Code, rec.Body.String())
		require.True(t, got.called, "handler should have been called")
		require.NoError(t, got.readErr)
		assert.Equal(t, body, string(got.body))
		assert.Equal(t, "/api/resource", got.path)
		// KeepValidatedValue is on by default.
		require.True(t, got.hasValidated, "handler's body should expose ValidatedValue()")
		assert.Equal(t, map[string]any{"name": "x"}, got.validated)

		// A body that doesn't match the schema is still rejected.
		got = prefixedHandlerResult{}
		rec = httptest.NewRecorder()
		e.ServeHTTP(rec, newRawJSONPost("http://test.hostname/api/resource", `{"name": 5}`))
		assert.Equal(t, http.StatusBadRequest, rec.Code)
		assert.False(t, got.called, "handler should not have been called")
	})

	t.Run("security validation with ExcludeRequestBody", func(t *testing.T) {
		// openapi3filter's security validation reads the body even when body
		// validation is excluded, which is how issue #16 was found.
		const specYAML = `
openapi: "3.0.0"
info:
  title: t
  version: 1.0.0
paths:
  /thing:
    post:
      operationId: createThing
      security:
        - BearerAuth: []
      requestBody:
        required: true
        content:
          application/json:
            schema:
              type: object
      responses:
        '204':
          description: ok
components:
  securitySchemes:
    BearerAuth:
      type: http
      scheme: bearer
`
		spec, err := openapi3.NewLoader().LoadFromData([]byte(specYAML))
		require.NoError(t, err)

		e := echo.New()
		e.Use(OapiRequestValidatorWithOptions(spec, &Options{
			Prefix:                "/api",
			SilenceServersWarning: true,
			Options: openapi3filter.Options{
				ExcludeRequestBody: true,
				AuthenticationFunc: func(context.Context, *openapi3filter.AuthenticationInput) error {
					return nil
				},
			},
		}))
		var got prefixedHandlerResult
		e.POST("/api/thing", got.handler(http.StatusNoContent))

		r := newRawJSONPost("/api/thing", body)
		r.Header.Set("authorization", "Bearer token")
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, r)

		require.Equal(t, http.StatusNoContent, rec.Code, rec.Body.String())
		require.True(t, got.called, "handler should have been called")
		require.NoError(t, got.readErr)
		assert.Equal(t, body, string(got.body))
		assert.Equal(t, "/api/thing", got.path)
	})

	t.Run("KeepValidatedValue", func(t *testing.T) {
		spec, err := openapi3.NewLoader().LoadFromData(testSchema)
		require.NoError(t, err)

		e := echo.New()
		e.Use(OapiRequestValidatorWithOptions(spec, &Options{
			SilenceServersWarning: true,
			Prefix:                "/api",
			Options: openapi3filter.Options{
				KeepValidatedValue: true,
			},
		}))
		var got prefixedHandlerResult
		e.POST("/api/resource", got.handler(http.StatusNoContent))

		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, newRawJSONPost("http://test.hostname/api/resource", body))

		require.Equal(t, http.StatusNoContent, rec.Code, rec.Body.String())
		require.True(t, got.called, "handler should have been called")
		require.True(t, got.hasValidated, "handler's body should expose ValidatedValue()")
		assert.Equal(t, map[string]any{"name": "x"}, got.validated)
		require.NoError(t, got.readErr)
		assert.Equal(t, body, string(got.body))
		assert.Equal(t, "/api/resource", got.path)
	})
}

// TestOapiRequestValidatorKeepValidatedValue checks that the middleware enables
// openapi3filter.Options.KeepValidatedValue unless Options.DiscardValidatedValue
// is set, so the handler's request body exposes the validated value through
// ValidatedValue() while remaining readable.
func TestOapiRequestValidatorKeepValidatedValue(t *testing.T) {
	// Not JSON-canonical on purpose: the handler must see these exact bytes,
	// not a re-encoding of the decoded value.
	const body = `{ "name" : "x" }`

	spec, err := openapi3.NewLoader().LoadFromData(testSchema)
	require.NoError(t, err)

	// serve sends body to POST /resource through mw and returns what the
	// handler saw.
	serve := func(t *testing.T, mw echo.MiddlewareFunc) prefixedHandlerResult {
		t.Helper()
		e := echo.New()
		e.Use(mw)
		var got prefixedHandlerResult
		e.POST("/resource", got.handler(http.StatusNoContent))

		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, newRawJSONPost("http://test.hostname/resource", body))

		require.Equal(t, http.StatusNoContent, rec.Code, rec.Body.String())
		require.True(t, got.called, "handler should have been called")
		require.NoError(t, got.readErr)
		assert.Equal(t, body, string(got.body), "body should still be readable with the original bytes")
		return got
	}

	t.Run("default without options", func(t *testing.T) {
		got := serve(t, OapiRequestValidator(spec))
		require.True(t, got.hasValidated, "handler's body should expose ValidatedValue()")
		assert.Equal(t, map[string]any{"name": "x"}, got.validated)
	})

	t.Run("default with empty options", func(t *testing.T) {
		options := &Options{}
		got := serve(t, OapiRequestValidatorWithOptions(spec, options))
		require.True(t, got.hasValidated, "handler's body should expose ValidatedValue()")
		assert.Equal(t, map[string]any{"name": "x"}, got.validated)
		assert.False(t, options.Options.KeepValidatedValue, "the caller's options should not be modified")
	})

	t.Run("DiscardValidatedValue", func(t *testing.T) {
		got := serve(t, OapiRequestValidatorWithOptions(spec, &Options{
			DiscardValidatedValue: true,
		}))
		assert.False(t, got.hasValidated, "handler's body should not expose ValidatedValue()")
	})

	t.Run("DiscardValidatedValue overrides openapi3filter KeepValidatedValue", func(t *testing.T) {
		got := serve(t, OapiRequestValidatorWithOptions(spec, &Options{
			DiscardValidatedValue: true,
			Options: openapi3filter.Options{
				KeepValidatedValue: true,
			},
		}))
		assert.False(t, got.hasValidated, "handler's body should not expose ValidatedValue()")
	})

	t.Run("ExcludeRequestBody", func(t *testing.T) {
		got := serve(t, OapiRequestValidatorWithOptions(spec, &Options{
			Options: openapi3filter.Options{
				ExcludeRequestBody: true,
			},
		}))
		// The body was not validated, so there is no validated value to keep.
		assert.False(t, got.hasValidated, "handler's body should not expose ValidatedValue()")
	})
}

// TestOapiRequestValidatorWithPrefixEncodedPathParams checks that the prefix
// is trimmed from URL.RawPath as well as URL.Path, and both are restored
// before the handler runs.
func TestOapiRequestValidatorWithPrefixEncodedPathParams(t *testing.T) {
	spec, err := openapi3.NewLoader().LoadFromData(testSchema)
	require.NoError(t, err)

	e := echo.New()
	e.Use(OapiRequestValidatorWithOptions(spec, &Options{
		SilenceServersWarning: true,
		Prefix:                "/api",
	}))
	var got prefixedHandlerResult
	e.GET("/api/resource/maxlength/:param", got.handler(http.StatusNoContent))

	// %2F decodes to a single "/", which satisfies maxLength: 1. The router
	// matches on URL.EscapedPath(), which only uses RawPath while it is a valid
	// encoding of Path; if the prefix were trimmed from Path alone, the router
	// would see the decoded "//" and find no operation.
	{
		rec := doGet(t, e, "http://test.hostname/api/resource/maxlength/%2F")
		require.Equal(t, http.StatusNoContent, rec.Code, rec.Body.String())
		require.True(t, got.called, "handler should have been called")
		assert.Equal(t, "/api/resource/maxlength//", got.path)
		assert.Equal(t, "/api/resource/maxlength/%2F", got.rawPath)
	}

	// %2B%2B decodes to "++", which exceeds maxLength: 1.
	{
		got = prefixedHandlerResult{}
		rec := doGet(t, e, "http://test.hostname/api/resource/maxlength/%2B%2B")
		assert.Equal(t, http.StatusBadRequest, rec.Code)
		assert.False(t, got.called, "handler should not have been called")
	}
}

func TestEncodedPathParams(t *testing.T) {
	spec, err := openapi3.NewLoader().LoadFromData(testSchema)
	require.NoError(t, err, "Error initializing OpenAPI spec")

	e := echo.New()

	options := Options{
		SilenceServersWarning: true,
	}

	e.Use(OapiRequestValidatorWithOptions(spec, &options))

	called := false

	// Register handlers for parameterized paths. These must be registered
	// before any requests are made so echo allocates enough param slots.
	e.GET("/resource/maxlength/:param", func(c *echo.Context) error {
		called = true
		return c.NoContent(http.StatusNoContent)
	})
	e.GET("/resource/pattern/:param", func(c *echo.Context) error {
		called = true
		return c.NoContent(http.StatusNoContent)
	})

	// Encoded "+" (%2B) — 3 chars encoded, but 1 char decoded.
	// maxLength: 1 should pass because validation uses the decoded value.
	{
		rec := doGet(t, e, "http://test.hostname/resource/maxlength/%2B")
		assert.Equal(t, http.StatusNoContent, rec.Code)
		assert.True(t, called, "Handler should have been called")
		called = false
	}

	// Unencoded "+" in the same path — should also pass.
	{
		rec := doGet(t, e, "http://test.hostname/resource/maxlength/+")
		assert.Equal(t, http.StatusNoContent, rec.Code)
		assert.True(t, called, "Handler should have been called")
		called = false
	}

	// Two-char unencoded value exceeds maxLength: 1 — should be rejected.
	{
		rec := doGet(t, e, "http://test.hostname/resource/maxlength/ab")
		assert.Equal(t, http.StatusBadRequest, rec.Code)
		assert.False(t, called, "Handler should not have been called")
	}

	// Encoded value that decodes to two chars (%2B%2B -> "++") — should be rejected.
	{
		rec := doGet(t, e, "http://test.hostname/resource/maxlength/%2B%2B")
		assert.Equal(t, http.StatusBadRequest, rec.Code)
		assert.False(t, called, "Handler should not have been called")
	}

	// Pattern: ^\+[0-9]+$ — encoded "+1234" as "%2B1234" should pass.
	{
		rec := doGet(t, e, "http://test.hostname/resource/pattern/%2B1234")
		assert.Equal(t, http.StatusNoContent, rec.Code)
		assert.True(t, called, "Handler should have been called")
		called = false
	}

	// Unencoded "+1234" should also pass.
	{
		rec := doGet(t, e, "http://test.hostname/resource/pattern/+1234")
		assert.Equal(t, http.StatusNoContent, rec.Code)
		assert.True(t, called, "Handler should have been called")
		called = false
	}

	// Value that doesn't match pattern — should be rejected.
	{
		rec := doGet(t, e, "http://test.hostname/resource/pattern/nope")
		assert.Equal(t, http.StatusBadRequest, rec.Code)
		assert.False(t, called, "Handler should not have been called")
	}
}

func TestGetSkipperFromOptions(t *testing.T) {

	options := new(Options)
	assert.NotNil(t, getSkipperFromOptions(options))

	options = &Options{}
	assert.NotNil(t, getSkipperFromOptions(options))

	options = &Options{
		Skipper: echomiddleware.DefaultSkipper,
	}
	assert.NotNil(t, getSkipperFromOptions(options))
}
