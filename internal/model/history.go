package model

import (
	"context"
	"strconv"
	"time"

	"github.com/CliXiD/arch-api/internal/db/dbq"
)

// History returns one page of a node's change_log rows and its edges' rows, newest first by
// (at, id). Deleted nodes and edges keep their rows; an unknown node has none.
func (s *Service) History(ctx context.Context, environment, id string, limit int, cursor string) (HistoryPage, error) {
	p := dbq.NodeHistoryParams{
		Environment: environment,
		NodeID:      id,
		RowLimit:    int32(limit + 1), //nolint:gosec // the API bounds limit to 1..500
	}
	if cursor != "" {
		at, rowID, err := decodeHistoryCursor(cursor)
		if err != nil {
			return HistoryPage{}, err
		}
		p.Paged, p.CursorAt, p.CursorID = true, at, rowID
	}
	rows, err := s.q.NodeHistory(ctx, p)
	if err != nil {
		return HistoryPage{}, err
	}
	out := HistoryPage{Items: make([]Change, 0, min(len(rows), limit))}
	for i, r := range rows {
		if i == limit {
			last := out.Items[len(out.Items)-1]
			next := encodeCursor(last.At.Format(time.RFC3339Nano), strconv.FormatInt(last.ID, 10))
			out.NextCursor = &next
			break
		}
		c, err := changeFrom(r)
		if err != nil {
			return HistoryPage{}, err
		}
		out.Items = append(out.Items, c)
	}
	return out, nil
}

func decodeHistoryCursor(cursor string) (time.Time, int64, error) {
	invalid := &ValidationError{Location: "query.cursor", Message: "invalid cursor"}
	key, err := decodeCursor(cursor, 2)
	if err != nil {
		return time.Time{}, 0, err
	}
	at, err := time.Parse(time.RFC3339Nano, key[0])
	if err != nil {
		return time.Time{}, 0, invalid
	}
	id, err := strconv.ParseInt(key[1], 10, 64)
	if err != nil {
		return time.Time{}, 0, invalid
	}
	return at, id, nil
}

func changeFrom(r dbq.ChangeLog) (Change, error) {
	c := Change{
		ID: r.ID, At: r.At.UTC(), Actor: r.Actor, ActorName: r.ActorName, ActorKind: r.ActorKind, Source: r.Source,
		Entity: r.Entity, EntityKey: r.EntityKey, Action: r.Action,
	}
	var err error
	if r.Before != nil {
		if c.Before, err = object(r.Before); err != nil {
			return Change{}, err
		}
	}
	if r.After != nil {
		if c.After, err = object(r.After); err != nil {
			return Change{}, err
		}
	}
	return c, nil
}
