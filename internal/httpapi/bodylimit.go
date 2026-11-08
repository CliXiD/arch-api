package httpapi

import (
	"bytes"
	"errors"
	"io"
	"net/http"

	"github.com/gin-gonic/gin"
)

// MaxBodyBytes is the largest request body any route accepts (1 MB).
const MaxBodyBytes = 1 << 20

// bodyLimit answers 413 /problems/too-large to a request body over MaxBodyBytes, before
// authentication. A declared Content-Length over the limit is rejected without reading the body.
// A body of unknown length (chunked) is read into memory up to the limit, so the 413 holds even on
// routes that never read it. A body of known length within the limit is wrapped in
// http.MaxBytesReader.
//
// A route with a larger documented limit (the YAML import, 5 MB, [SPEC-yaml-as-code-03]) will
// exempt itself here when it is added.
func bodyLimit() gin.HandlerFunc {
	return func(c *gin.Context) {
		r := c.Request
		switch {
		case r.ContentLength > MaxBodyBytes:
			tooLarge(c)
			return
		case r.ContentLength < 0:
			body, err := io.ReadAll(http.MaxBytesReader(c.Writer, r.Body, MaxBodyBytes))
			if err != nil {
				if maxErr := (*http.MaxBytesError)(nil); errors.As(err, &maxErr) {
					tooLarge(c)
					return
				}
				c.AbortWithStatus(http.StatusBadRequest)
				return
			}
			r.Body = io.NopCloser(bytes.NewReader(body))
		case r.Body != nil && r.Body != http.NoBody:
			r.Body = http.MaxBytesReader(c.Writer, r.Body, MaxBodyBytes)
		}
		c.Next()
	}
}

func tooLarge(c *gin.Context) {
	writeHTTPProblem(c.Writer, http.StatusRequestEntityTooLarge, ProblemTooLarge, "request body exceeds 1 MB")
	c.Abort()
}
