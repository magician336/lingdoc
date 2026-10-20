package candidateadoption

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/clienterror"
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

// TestCandidateAdoptionFormsSeparateInvalidStateFromInvalidRequest keeps the
// distinction the old status table made: a state that does not allow the
// action is not a malformed request. The status now travels with the form the
// error declares.
func TestCandidateAdoptionFormsSeparateInvalidStateFromInvalidRequest(t *testing.T) {
	state, ok := clienterror.FormOf(ErrAdoptionInvalidState)
	require.True(t, ok)
	require.Equal(t, http.StatusUnprocessableEntity, state.Status)
	require.Equal(t, "invalid_state", state.Code)

	request, ok := clienterror.FormOf(ErrInvalidRequest)
	require.True(t, ok)
	require.Equal(t, http.StatusBadRequest, request.Status)
	require.Equal(t, "invalid_request", request.Code)
}
