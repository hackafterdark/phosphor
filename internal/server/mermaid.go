package server

import (
	"bytes"
	"context"
	"fmt"
	"html/template"
	"net/http"
	"strconv"
	"time"

	"github.com/hackafterdark/phosphor/pkg/db"
	"github.com/labstack/echo/v5"
)

var (
	mermaidServiceEnabled bool
	mermaidServicePort    int = 8643 // default port
	mermaidSessionID      string
)

// SetMermaidServiceEnabled configures whether the Mermaid rendering endpoint is active.
func SetMermaidServiceEnabled(enabled bool, port int) {
	mermaidServiceEnabled = enabled
	mermaidServicePort = port
	mermaidSessionID = ""
}

// SetMermaidSessionID sets the session ID to associate diagrams with a session.
func SetMermaidSessionID(sessionID string) {
	mermaidSessionID = sessionID
}

// MermaidURL stores the mermaid syntax in the DB and returns a short URL
// with just the diagram ID. Returns empty string if the service is disabled.
func MermaidURL(syntax string) string {
	if !mermaidServiceEnabled {
		return ""
	}
	querier := GetDiagramsQuerier()
	if querier == nil {
		return ""
	}
	diagram, err := querier.CreateDiagram(context.Background(), db.CreateDiagramParams{
		SessionID: mermaidSessionID,
		Syntax:    syntax,
		CreatedAt: time.Now().UnixMilli(),
	})
	if err != nil {
		return ""
	}
	return fmt.Sprintf("http://127.0.0.1:%d/service/mermaid/render?id=%d", mermaidServicePort, diagram.ID)
}

// diagramsQuerier holds the DB querier for diagram lookups.
var diagramsQuerier db.Querier

// SetDiagramsQuerier sets the querier for diagram DB access.
func SetDiagramsQuerier(q db.Querier) {
	diagramsQuerier = q
}

// GetDiagramsQuerier returns the current diagram querier.
func GetDiagramsQuerier() db.Querier {
	return diagramsQuerier
}
func HandleMermaidRender(ctx *echo.Context) error {
	diagramID := ctx.QueryParam("id")
	if diagramID == "" {
		return ctx.String(http.StatusBadRequest, "Missing required query parameter: id")
	}

	id, err := strconv.ParseInt(diagramID, 10, 64)
	if err != nil {
		return ctx.String(http.StatusBadRequest, "Invalid id parameter")
	}

	querier := GetDiagramsQuerier()
	if querier == nil {
		return ctx.String(http.StatusServiceUnavailable, "Database not available")
	}

	diagram, err := querier.GetDiagram(ctx.Request().Context(), id)
	if err != nil {
		return ctx.String(http.StatusNotFound, "Diagram not found")
	}
	if diagram.Syntax == "" {
		return ctx.String(http.StatusNotFound, "Diagram not found")
	}

	theme := ctx.QueryParam("theme")
	if theme == "" {
		theme = "dark"
	}
	width := ctx.QueryParam("width")
	if width == "" {
		width = "800"
	}
	height := ctx.QueryParam("height")
	if height == "" {
		height = "600"
	}

	tmpl, err := template.New("mermaid").Parse(mermaidHTMLTemplate)
	if err != nil {
		return ctx.String(http.StatusInternalServerError, "Template parse error: "+err.Error())
	}

	buf := new(bytes.Buffer)
	err = tmpl.Execute(buf, map[string]any{
		"Syntax":    diagram.Syntax,
		"Theme":     theme,
		"Width":     width,
		"Height":    height,
		"MermaidJS": template.JS(mermaidJS),
	})
	if err != nil {
		return ctx.String(http.StatusInternalServerError, "Template execution error: "+err.Error())
	}

	return ctx.HTML(http.StatusOK, buf.String())
}
