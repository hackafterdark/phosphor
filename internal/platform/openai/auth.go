package openai

import (
	"net/http"
	"strings"

	"github.com/labstack/echo/v5"
)

// AuthMiddleware returns an Echo middleware that enforces bearer token
// authentication. If apiKey is empty, all requests are allowed (dev mode).
// Otherwise, every request must include an Authorization header with a
// Bearer token matching apiKey.
func AuthMiddleware(apiKey string) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c *echo.Context) error {
			if apiKey == "" {
				// No auth configured — allow all (local/dev use).
				return next(c)
			}

			authHeader := c.Request().Header.Get("Authorization")
			if !strings.HasPrefix(authHeader, "Bearer ") {
				return echo.NewHTTPError(http.StatusUnauthorized, "missing bearer token")
			}

			token := strings.TrimPrefix(authHeader, "Bearer ")
			if token != apiKey {
				return echo.NewHTTPError(http.StatusUnauthorized, "invalid bearer token")
			}

			return next(c)
		}
	}
}
