package http

import (
	"errors"
	"fmt"
	"net/http"
	"testing"

	"gh.tarampamp.am/webhook-tester/v2/internal/http/handlers/shared"
	"gh.tarampamp.am/webhook-tester/v2/internal/storage"
)

func TestStatusForError(t *testing.T) {
	t.Parallel()

	for name, tc := range map[string]struct {
		err  error
		want int
	}{
		"bad request":            {shared.ErrBadRequest, http.StatusBadRequest},
		"handler conflict":       {shared.ErrConflict, http.StatusConflict},
		"storage slug conflict":  {storage.ErrSlugConflict, http.StatusConflict},
		"wrapped slug conflict":  {fmt.Errorf("create: %w", storage.ErrSlugConflict), http.StatusConflict},
		"not found":              {storage.ErrNotFound, http.StatusNotFound},
		"session not found":      {storage.ErrSessionNotFound, http.StatusNotFound},
		"unknown ⇒ server error": {errors.New("boom"), http.StatusInternalServerError},
		"nil ⇒ server error":     {nil, http.StatusInternalServerError},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if got := statusForError(tc.err); got != tc.want {
				t.Fatalf("statusForError(%v) = %d, want %d", tc.err, got, tc.want)
			}
		})
	}
}
