package echomiddleware

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/openapi3filter"
	"github.com/labstack/echo/v5"
	"github.com/stretchr/testify/require"
)

func TestPrefixKeepsTheRequestBody(t *testing.T) {
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

	var got []byte
	e.POST("/api/thing", func(c *echo.Context) error {
		got, _ = io.ReadAll(c.Request().Body)
		return c.NoContent(http.StatusNoContent)
	})

	r := httptest.NewRequest(http.MethodPost, "/api/thing", strings.NewReader(`{"name":"x"}`))
	r.Header.Set("content-type", "application/json")
	r.Header.Set("authorization", "Bearer token")
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, r)

	require.Equal(t, http.StatusNoContent, rec.Code)
	require.NotEmpty(t, got, "the handler received a drained body")
}
