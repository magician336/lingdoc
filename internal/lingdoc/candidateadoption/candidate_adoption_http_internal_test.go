package candidateadoption

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestDecodeCandidateRequestRejectsUnknownAndTrailingJSON(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, body := range []string{
		`{"candidate_id":"candidate-1","unknown":true}`,
		`{"candidate_id":"candidate-1"}{"candidate_id":"candidate-2"}`,
	} {
		t.Run(body, func(t *testing.T) {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest("POST", "/", strings.NewReader(body))
			var req acceptCandidateRequest
			require.Error(t, decodeCandidateRequest(c, &req))
		})
	}
}
