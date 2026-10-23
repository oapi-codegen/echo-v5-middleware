# OpenAPI Validation Middleware for `labstack/echo` v5 servers

Middleware for the [Echo v5 web server](https://github.com/labstack/echo) to perform validation of incoming requests via an OpenAPI specification.

This project is a lightweight wrapper over the excellent [kin-openapi](https://github.com/getkin/kin-openapi) library's [`openapi3filter` package](https://pkg.go.dev/github.com/getkin/kin-openapi/openapi3filter).

This is _intended_ to be used with code that's generated through [`oapi-codegen`](https://github.com/oapi-codegen/oapi-codegen), but should work otherwise.

> [!NOTE]
> Using Echo v4? You want [`oapi-codegen/echo-middleware`](https://github.com/oapi-codegen/echo-middleware) instead.

> [!WARNING]

⚠️ This README may be for the latest development version, which may contain unreleased changes. Please ensure you're looking at the README for the latest release version.

## Usage

You can add the middleware to your project with:

```sh
go get github.com/oapi-codegen/echo-v5-middleware
```

There is a full example of usage in [the Go doc for this project](https://pkg.go.dev/github.com/oapi-codegen/echo-v5-middleware#pkg-examples).

A simplified version of this code is as follows:

```go
rawSpec := `
openapi: "3.0.0"
# ...
`
spec, _ := openapi3.NewLoader().LoadFromData([]byte(rawSpec))

// NOTE that we need to make sure that the `Servers` aren't set, otherwise the OpenAPI validation middleware will validate that the `Host` header (of incoming requests) are targeting known `Servers` in the OpenAPI spec
// See also: Options#SilenceServersWarning
spec.Servers = nil

e := echo.New()
e.POST("/resource", func(c echo.Context) error {
    fmt.Printf("%s /resource was called\n", c.Request().Method)

    return c.NoContent(http.StatusNoContent)
})

// create middleware
mw := middleware.OapiRequestValidatorWithOptions(spec, &middleware.Options{
    Options: openapi3filter.Options{
        AuthenticationFunc: authenticationFunc,
    },
})

e.Use(mw)

// now all HTTP routes will be handled by the middleware, and any requests that are invalid will be rejected
```

### Validated request body

The middleware enables kin-openapi's [`openapi3filter.Options.KeepValidatedValue`](https://pkg.go.dev/github.com/getkin/kin-openapi/openapi3filter#Options) by default. After a request body passes validation, the body that handlers see (`c.Request().Body`) also has a `ValidatedValue() any` method returning the decoded value that was validated, and it can still be read as usual. This lets a handler, such as an `oapi-codegen` strict server, bind exactly the value that was validated rather than decoding the body a second time, which could interpret it differently (for example, `encoding/json` matches object member names case-insensitively, so `{"role":"user","ROLE":"admin"}` would validate as `role: user` but decode as `Role: "admin"`). To drop the decoded value after validation instead, for instance to limit memory retention for very large request bodies, set `Options.DiscardValidatedValue`; it overrides `Options.Options.KeepValidatedValue`.

Any middleware that runs after the validator and replaces the request body (such as decompression, request size limiting or logging wrappers) hides the validated value from handlers, so register such middleware before the validator.

## FAQs

### What versions of Echo does this support?

| Version | Supported? |
| -- | -- |
| `github.com/labstack/echo/v5` | ✅ |
| `github.com/labstack/echo/v4` | ❌ Use [`oapi-codegen/echo-middleware`](https://github.com/oapi-codegen/echo-middleware) |


### "This doesn't support ..." / "I think it's a bug that ..."

As this project is a lightweight wrapper over [kin-openapi](https://github.com/getkin/kin-openapi)'s [`openapi3filter` package](https://pkg.go.dev/github.com/getkin/kin-openapi/openapi3filter), it's _likely_ that any bugs/features are better sent upstream.

However, it's worth raising an issue here instead, as it'll allow us to triage it before it goes to the kin-openapi maintainers.

Additionally, as `oapi-codegen` contains [a number of middleware modules](https://github.com/search?q=org%3Aoapi-codegen+middleware&type=repositories), we'll very likely want to implement the same functionality across all the middlewares, so it may take a bit more coordination to get the changes in across our middlewares.

### I've just updated my version of `kin-openapi`, and now I can't build my code 😠

The [kin-openapi](https://github.com/getkin/kin-openapi) project - which we 💜 for providing a great library and set of tooling for interacting with OpenAPI - is a pre-v1 release, which means that they're within their rights to push breaking changes.

This may lead to breakage in your consuming code, and if so, sorry that's happened!

We'll be aware of the issue, and will work to update both the core `oapi-codegen` and the middlewares accordingly.
