package session_get

import (
	"context"
	"encoding/base64"
	"fmt"

	"github.com/google/uuid"

	"gh.tarampamp.am/webhook-tester/v2/internal/http/openapi"
	"gh.tarampamp.am/webhook-tester/v2/internal/storage"
)

type (
	sID = openapi.SessionUUIDInPath

	Handler struct{ db storage.Storage }
)

func New(db storage.Storage) *Handler { return &Handler{db: db} }

func (h *Handler) Handle(ctx context.Context, sID sID) (*openapi.SessionOptionsResponse, error) {
	sess, sErr := h.db.GetSession(ctx, sID)
	if sErr != nil {
		return nil, fmt.Errorf("failed to get session: %w", sErr)
	}

	var sHeaders = make([]openapi.HttpHeader, len(sess.Headers))
	for i, header := range sess.Headers {
		sHeaders[i].Name, sHeaders[i].Value = header.Name, header.Value
	}

	// Parse the session reference as UUID; for slug-based lookups the UUID lookup will be
	// handled properly in a later task — until then the UUID field carries the parsed value.
	sUUID, _ := uuid.Parse(sID)

	return &openapi.SessionOptionsResponse{
		CreatedAtUnixMilli: sess.CreatedAtUnixMilli,
		Response: openapi.SessionResponseOptions{
			Delay:              uint16(sess.Delay.Seconds()),
			Headers:            sHeaders,
			ResponseBodyBase64: base64.StdEncoding.EncodeToString(sess.ResponseBody),
			StatusCode:         openapi.StatusCode(sess.Code),
		},
		Uuid: sUUID,
	}, nil
}
