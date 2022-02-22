// Package middleware implements middleware function for go-chi or net/http,
// which validates incoming HTTP requests to make sure that they conform to the given OAPI 3.0 specification.
// When OAPI validation failes on the request, we return an HTTP/400.
package middleware

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"net/http"
	"strings"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/openapi3filter"
	"github.com/getkin/kin-openapi/routers"
	"github.com/getkin/kin-openapi/routers/gorillamux"
)

// Options to customize request validation, openapi3filter specified options will be passed through.
type Options struct {
	Options openapi3filter.Options
	ErrRespContentType
}

type ErrRespContentType string

// Consts to expose supported Error Response Content-Types
const (
	ErrRespContentTypePlain ErrRespContentType = "text/plain"
	ErrRespContentTypeJSON  ErrRespContentType = "application/json"
	ErrRespContentTypeXML   ErrRespContentType = "application/xml"
)

// OapiRequestValidator Creates middleware to validate request by swagger spec.
// This middleware is good for net/http either since go-chi is 100% compatible with net/http.
func OapiRequestValidator(swagger *openapi3.T) func(next http.Handler) http.Handler {
	return OapiRequestValidatorWithOptions(swagger, nil)
}

// OapiRequestValidatorWithOptions Creates middleware to validate request by swagger spec.
// This middleware is good for net/http either since go-chi is 100% compatible with net/http.
func OapiRequestValidatorWithOptions(swagger *openapi3.T, options *Options) func(next http.Handler) http.Handler {
	router, err := gorillamux.NewRouter(swagger)
	if err != nil {
		panic(err)
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {

			// validate request
			if statusCode, err := validateRequest(r, router, options); err != nil {
				contentType := options.ErrRespContentType
				if contentType == "" {
					contentType = ErrRespContentTypePlain
				}
				w.Header().Set("Content-Type", string(contentType)+"; charset=utf-8")
				w.Header().Set("X-Content-Type-Options", "nosniff")
				w.WriteHeader(statusCode)

				body := []byte(err.Error())
				switch contentType {
				case ErrRespContentTypeJSON:
					body, _ = json.Marshal(err.Error())
				case ErrRespContentTypeXML:
					body, _ = xml.Marshal(err.Error())
				}
				fmt.Fprintln(w, string(body))
				return
			}

			// serve
			next.ServeHTTP(w, r)
		})
	}

}

// This function is called from the middleware above and actually does the work
// of validating a request.
func validateRequest(r *http.Request, router routers.Router, options *Options) (int, error) {

	// Find route
	route, pathParams, err := router.FindRoute(r)
	if err != nil {
		return http.StatusBadRequest, err // We failed to find a matching route for the request.
	}

	// Validate request
	requestValidationInput := &openapi3filter.RequestValidationInput{
		Request:    r,
		PathParams: pathParams,
		Route:      route,
	}

	if options != nil {
		requestValidationInput.Options = &options.Options
	}

	// Validate security before any other validation, unless options.Options.MultiError is true
	if options == nil || !options.Options.MultiError {
		if err := validateSecurity(requestValidationInput); err != nil {
			return http.StatusUnauthorized, err
		}
	}

	// Validate the rest of the request
	if err := openapi3filter.ValidateRequest(context.Background(), requestValidationInput); err != nil {
		switch e := err.(type) {
		case *openapi3filter.RequestError:
			// We've got a bad request
			// Split up the verbose error by lines and return the first one
			// openapi errors seem to be multi-line with a decent message on the first
			errorLines := strings.Split(e.Error(), "\n")
			return http.StatusBadRequest, fmt.Errorf(errorLines[0])
		case *openapi3filter.SecurityRequirementsError:
			return http.StatusUnauthorized, err
		default:
			// This case occurs when options.Options.MultiError is true.
			// TODO(zlb): Find a better way to handle this.
			return http.StatusInternalServerError, fmt.Errorf("error validating route: %s", err.Error())
		}
	}

	return http.StatusOK, nil
}

func validateSecurity(input *openapi3filter.RequestValidationInput) error {

	security := input.Route.Operation.Security
	if security == nil {
		security = &input.Route.Spec.Security
		if security == nil {
			return nil
		}
	}

	return openapi3filter.ValidateSecurityRequirements(context.Background(), input, *security)
}
