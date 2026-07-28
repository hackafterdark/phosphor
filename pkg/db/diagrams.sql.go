package db

import "context"

type CreateDiagramParams struct {
	SessionID string `json:"session_id"`
	Syntax    string `json:"syntax"`
	CreatedAt int64  `json:"created_at"`
}

const createDiagram = `INSERT INTO diagrams (session_id, syntax, created_at) VALUES ($1, $2, $3) RETURNING id, session_id, syntax, created_at`
const getDiagram = `SELECT id, session_id, syntax, created_at FROM diagrams WHERE id = $1`

func (q *Queries) CreateDiagram(ctx context.Context, arg CreateDiagramParams) (Diagram, error) {
	row := q.db.QueryRowContext(ctx, createDiagram, arg.SessionID, arg.Syntax, arg.CreatedAt)
	var d Diagram
	err := row.Scan(&d.ID, &d.SessionID, &d.Syntax, &d.CreatedAt)
	return d, err
}

func (q *Queries) GetDiagram(ctx context.Context, id int64) (Diagram, error) {
	row := q.db.QueryRowContext(ctx, getDiagram, id)
	var d Diagram
	err := row.Scan(&d.ID, &d.SessionID, &d.Syntax, &d.CreatedAt)
	return d, err
}