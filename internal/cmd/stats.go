package cmd

import (
	"bytes"
	"context"
	"database/sql"
	_ "embed"
	"encoding/json"
	"fmt"
	"html/template"
	"os"
	"os/user"
	"path/filepath"
	"strings"
	"time"

	"github.com/hackafterdark/phosphor/internal/ui/logo"

	"github.com/hackafterdark/phosphor/pkg/config"
	"github.com/hackafterdark/phosphor/pkg/db"
	"github.com/pkg/browser"
	"github.com/spf13/cobra"
)

//go:embed stats/index.html
var statsTemplate string

//go:embed stats/index.css
var statsCSS string

//go:embed stats/index.js
var statsJS string

var statsCmd = &cobra.Command{
	Use:   "stats",
	Short: "Show usage statistics",
	Long:  "Generate and display usage statistics including token usage, costs, and activity patterns",
	RunE:  runStats,
}

// Day names for day of week statistics.
var dayNames = []string{"Sunday", "Monday", "Tuesday", "Wednesday", "Thursday", "Friday", "Saturday"}

// Stats holds all the statistics data.
type Stats struct {
	GeneratedAt       time.Time          `json:"generated_at"`
	Total             TotalStats         `json:"total"`
	UsageByDay        []DailyUsage       `json:"usage_by_day"`
	UsageByModel      []ModelUsage       `json:"usage_by_model"`
	UsageByHour       []HourlyUsage      `json:"usage_by_hour"`
	UsageByDayOfWeek  []DayOfWeekUsage   `json:"usage_by_day_of_week"`
	RecentActivity    []DailyActivity    `json:"recent_activity"`
	AvgResponseTimeMs float64            `json:"avg_response_time_ms"`
	ToolUsage         []ToolUsage        `json:"tool_usage"`
	HourDayHeatmap    []HourDayHeatmapPt `json:"hour_day_heatmap"`
}

type TotalStats struct {
	TotalSessions            int64   `json:"total_sessions"`
	SessionsWithUsage        int64   `json:"sessions_with_usage"`
	TotalPromptTokens        int64   `json:"total_prompt_tokens"`
	TotalCompletionTokens    int64   `json:"total_completion_tokens"`
	TotalTokens              int64   `json:"total_tokens"`
	TotalCost                float64 `json:"total_cost"`
	ReasoningTokens          int64   `json:"reasoning_tokens"`
	ActivePromptTokens       int64   `json:"active_prompt_tokens"`
	ActiveCompletionTokens   int64   `json:"active_completion_tokens"`
	ActiveTokens             int64   `json:"active_tokens"`
	ActiveCost               float64 `json:"active_cost"`
	ActiveReasoningTokens    int64   `json:"active_reasoning_tokens"`
	SubagentPromptTokens     int64   `json:"subagent_prompt_tokens"`
	SubagentCompletionTokens int64   `json:"subagent_completion_tokens"`
	SubagentTokens           int64   `json:"subagent_tokens"`
	ActiveSubagentTokens     int64   `json:"active_subagent_tokens"`
	PrimaryTokens            int64   `json:"primary_tokens"`
	ActivePrimaryTokens      int64   `json:"active_primary_tokens"`
	TotalMessages            int64   `json:"total_messages"`
	AvgTokensPerSession      float64 `json:"avg_tokens_per_session"`
	AvgMessagesPerSession    float64 `json:"avg_messages_per_session"`
}

type DailyUsage struct {
	Day              string  `json:"day"`
	PromptTokens     int64   `json:"prompt_tokens"`
	CompletionTokens int64   `json:"completion_tokens"`
	ReasoningTokens  int64   `json:"reasoning_tokens"`
	TotalTokens      int64   `json:"total_tokens"`
	Cost             float64 `json:"cost"`
	SessionCount     int64   `json:"session_count"`
}

type ModelUsage struct {
	Model        string `json:"model"`
	Provider     string `json:"provider"`
	MessageCount int64  `json:"message_count"`
}

type HourlyUsage struct {
	Hour         int   `json:"hour"`
	SessionCount int64 `json:"session_count"`
}

type DayOfWeekUsage struct {
	DayOfWeek        int    `json:"day_of_week"`
	DayName          string `json:"day_name"`
	SessionCount     int64  `json:"session_count"`
	PromptTokens     int64  `json:"prompt_tokens"`
	CompletionTokens int64  `json:"completion_tokens"`
}

type DailyActivity struct {
	Day          string  `json:"day"`
	SessionCount int64   `json:"session_count"`
	TotalTokens  int64   `json:"total_tokens"`
	Cost         float64 `json:"cost"`
}

type ToolUsage struct {
	ToolName  string `json:"tool_name"`
	CallCount int64  `json:"call_count"`
}

type HourDayHeatmapPt struct {
	DayOfWeek    int   `json:"day_of_week"`
	Hour         int   `json:"hour"`
	SessionCount int64 `json:"session_count"`
}

func runStats(cmd *cobra.Command, _ []string) error {
	dataDir, _ := cmd.Flags().GetString("data-dir")
	ctx := cmd.Context()

	cfg, err := config.Init("", dataDir, false)
	if err != nil {
		return fmt.Errorf("failed to initialize config: %w", err)
	}
	if dataDir == "" {
		dataDir = cfg.Config().Options.DataDirectory
	}
	conn, err := db.Connect(ctx, dataDir)
	if err != nil {
		return fmt.Errorf("failed to connect to database: %w", err)
	}
	defer conn.Close()

	stats, err := gatherStats(ctx, conn)
	if err != nil {
		return fmt.Errorf("failed to gather stats: %w", err)
	}

	if stats.Total.TotalSessions == 0 && stats.Total.TotalTokens == 0 {
		return fmt.Errorf("no data available: no sessions or recorded usage found in database")
	}

	currentUser, err := user.Current()
	if err != nil {
		return fmt.Errorf("failed to get current user: %w", err)
	}
	username := currentUser.Username
	project, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("failed to get current directory: %w", err)
	}
	project = strings.Replace(project, currentUser.HomeDir, "~", 1)

	htmlPath := filepath.Join(dataDir, "stats/index.html")
	if err := generateHTML(stats, project, username, htmlPath); err != nil {
		return fmt.Errorf("failed to generate HTML: %w", err)
	}

	fmt.Printf("Stats generated: %s\n", htmlPath)

	if err := browser.OpenFile(htmlPath); err != nil {
		fmt.Printf("Could not open browser: %v\n", err)
		fmt.Println("Please open the file manually.")
	}

	return nil
}

func gatherStats(ctx context.Context, conn *sql.DB) (*Stats, error) {
	queries := db.New(conn)

	stats := &Stats{
		GeneratedAt: time.Now(),
	}

	// Total stats.
	total, err := queries.GetTotalStats(ctx)
	if err != nil {
		return nil, fmt.Errorf("get total stats: %w", err)
	}
	activePrompt := toInt64(total.ActivePromptTokens)
	activeCompletion := toInt64(total.ActiveCompletionTokens)
	stats.Total = TotalStats{
		TotalSessions:          total.TotalSessions,
		SessionsWithUsage:      toInt64(total.TotalSessionsWithUsage),
		TotalPromptTokens:      toInt64(total.TotalPromptTokens),
		TotalCompletionTokens:  toInt64(total.TotalCompletionTokens),
		TotalTokens:            toInt64(total.TotalPromptTokens) + toInt64(total.TotalCompletionTokens),
		TotalCost:              toFloat64(total.TotalCost),
		ActivePromptTokens:     activePrompt,
		ActiveCompletionTokens: activeCompletion,
		ActiveTokens:           activePrompt + activeCompletion,
		ActiveCost:             toFloat64(total.ActiveCost),
		TotalMessages:          toInt64(total.TotalMessages),
		AvgTokensPerSession:    toFloat64(total.AvgTokensPerSession),
		AvgMessagesPerSession:  toFloat64(total.AvgMessagesPerSession),
	}

	// Sub-agent split and reasoning totals. Usage rows carry an is_subagent flag
	// captured at record time, so the split survives the session_id being nulled
	// when a session (or its sub-agents) is later pruned. Reasoning tokens are
	// tracked separately from in/out and are never folded into either.
	var subPrompt, subCompletion, activeSub, totalReasoning, activeReasoning int64
	subRow := conn.QueryRowContext(ctx, `
		SELECT
			COALESCE(SUM(CASE WHEN is_subagent = 1 THEN prompt_tokens ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN is_subagent = 1 THEN completion_tokens ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN is_subagent = 1 AND session_id IS NOT NULL THEN prompt_tokens + completion_tokens ELSE 0 END), 0),
			COALESCE(SUM(reasoning_tokens), 0),
			COALESCE(SUM(CASE WHEN session_id IS NOT NULL THEN reasoning_tokens ELSE 0 END), 0)
		FROM token_usage`)
	if err := subRow.Scan(&subPrompt, &subCompletion, &activeSub, &totalReasoning, &activeReasoning); err != nil {
		return nil, fmt.Errorf("get subagent usage: %w", err)
	}
	stats.Total.ReasoningTokens = totalReasoning
	stats.Total.ActiveReasoningTokens = activeReasoning
	stats.Total.SubagentPromptTokens = subPrompt
	stats.Total.SubagentCompletionTokens = subCompletion
	stats.Total.SubagentTokens = subPrompt + subCompletion
	stats.Total.ActiveSubagentTokens = activeSub
	stats.Total.PrimaryTokens = stats.Total.TotalTokens - stats.Total.SubagentTokens
	stats.Total.ActivePrimaryTokens = stats.Total.ActiveTokens - activeSub

	// Usage by day.
	dailyUsage, err := queries.GetUsageByDay(ctx)
	if err != nil {
		return nil, fmt.Errorf("get usage by day: %w", err)
	}
	for _, d := range dailyUsage {
		prompt := nullFloat64ToInt64(d.PromptTokens)
		completion := nullFloat64ToInt64(d.CompletionTokens)
		reasoning := nullFloat64ToInt64(d.ReasoningTokens)
		stats.UsageByDay = append(stats.UsageByDay, DailyUsage{
			Day:              fmt.Sprintf("%v", d.Day),
			PromptTokens:     prompt,
			CompletionTokens: completion,
			ReasoningTokens:  reasoning,
			TotalTokens:      prompt + completion,
			Cost:             d.Cost.Float64,
			SessionCount:     d.SessionCount,
		})
	}

	// Usage by model.
	modelUsage, err := queries.GetUsageByModel(ctx)
	if err != nil {
		return nil, fmt.Errorf("get usage by model: %w", err)
	}
	for _, m := range modelUsage {
		stats.UsageByModel = append(stats.UsageByModel, ModelUsage{
			Model:        m.Model,
			Provider:     m.Provider,
			MessageCount: m.MessageCount,
		})
	}

	// Usage by hour.
	hourlyUsage, err := queries.GetUsageByHour(ctx)
	if err != nil {
		return nil, fmt.Errorf("get usage by hour: %w", err)
	}
	for _, h := range hourlyUsage {
		stats.UsageByHour = append(stats.UsageByHour, HourlyUsage{
			Hour:         int(h.Hour),
			SessionCount: h.SessionCount,
		})
	}

	// Usage by day of week.
	dowUsage, err := queries.GetUsageByDayOfWeek(ctx)
	if err != nil {
		return nil, fmt.Errorf("get usage by day of week: %w", err)
	}
	for _, d := range dowUsage {
		stats.UsageByDayOfWeek = append(stats.UsageByDayOfWeek, DayOfWeekUsage{
			DayOfWeek:        int(d.DayOfWeek),
			DayName:          dayNames[int(d.DayOfWeek)],
			SessionCount:     d.SessionCount,
			PromptTokens:     nullFloat64ToInt64(d.PromptTokens),
			CompletionTokens: nullFloat64ToInt64(d.CompletionTokens),
		})
	}

	// Recent activity (last 30 days).
	recent, err := queries.GetRecentActivity(ctx)
	if err != nil {
		return nil, fmt.Errorf("get recent activity: %w", err)
	}
	for _, r := range recent {
		stats.RecentActivity = append(stats.RecentActivity, DailyActivity{
			Day:          fmt.Sprintf("%v", r.Day),
			SessionCount: r.SessionCount,
			TotalTokens:  nullFloat64ToInt64(r.TotalTokens),
			Cost:         r.Cost.Float64,
		})
	}

	// Average response time.
	avgResp, err := queries.GetAverageResponseTime(ctx)
	if err != nil {
		return nil, fmt.Errorf("get average response time: %w", err)
	}
	stats.AvgResponseTimeMs = toFloat64(avgResp) * 1000

	// Tool usage.
	toolUsage, err := queries.GetToolUsage(ctx)
	if err != nil {
		return nil, fmt.Errorf("get tool usage: %w", err)
	}
	for _, t := range toolUsage {
		if name, ok := t.ToolName.(string); ok && name != "" {
			stats.ToolUsage = append(stats.ToolUsage, ToolUsage{
				ToolName:  name,
				CallCount: t.CallCount,
			})
		}
	}

	// Hour/day heatmap.
	heatmap, err := queries.GetHourDayHeatmap(ctx)
	if err != nil {
		return nil, fmt.Errorf("get hour day heatmap: %w", err)
	}
	for _, h := range heatmap {
		stats.HourDayHeatmap = append(stats.HourDayHeatmap, HourDayHeatmapPt{
			DayOfWeek:    int(h.DayOfWeek),
			Hour:         int(h.Hour),
			SessionCount: h.SessionCount,
		})
	}

	return stats, nil
}

func toInt64(v any) int64 {
	switch val := v.(type) {
	case int64:
		return val
	case float64:
		return int64(val)
	case int:
		return int64(val)
	default:
		return 0
	}
}

func toFloat64(v any) float64 {
	switch val := v.(type) {
	case float64:
		return val
	case int64:
		return float64(val)
	case int:
		return float64(val)
	default:
		return 0
	}
}

func nullFloat64ToInt64(n sql.NullFloat64) int64 {
	if n.Valid {
		return int64(n.Float64)
	}
	return 0
}

func generateHTML(stats *Stats, projName, username, path string) error {
	statsJSON, err := json.Marshal(stats)
	if err != nil {
		return err
	}

	tmpl, err := template.New("stats").Parse(statsTemplate)
	if err != nil {
		return fmt.Errorf("parse template: %w", err)
	}

	data := struct {
		StatsJSON   template.JS
		CSS         template.CSS
		JS          template.JS
		GeneratedAt string
		ProjectName string
		Username    string
		AppTitle    string
		LogoText    template.HTML
	}{
		StatsJSON:   template.JS(statsJSON),
		CSS:         template.CSS(statsCSS),
		JS:          template.JS(statsJS),
		GeneratedAt: stats.GeneratedAt.Format("2006-01-02"),
		ProjectName: projName,
		Username:    username,
		AppTitle:    "Phosphor",
		LogoText:    template.HTML(renderLogoText("Phosphor")),
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return fmt.Errorf("execute template: %w", err)
	}

	// Ensure parent directory exists.
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create directory: %w", err)
	}

	return os.WriteFile(path, buf.Bytes(), 0o644)
}

// renderLogoText renders the app title using the Pagga figlet font,
// converting each character into a styled HTML span.
func renderLogoText(title string) string {
	text, _, _, _ := logo.FigletText(title, "Pagga", false)
	lines := strings.Split(strings.TrimSpace(text), "\n")
	if len(lines) == 0 {
		return ""
	}

	var buf strings.Builder
	buf.WriteString("<div class=\"logo-figlet\">\n")
	for _, line := range lines {
		buf.WriteString("<span class=\"logo-line\">")
		for _, ch := range line {
			if ch == ' ' {
				buf.WriteString("&nbsp;")
			} else {
				buf.WriteString(template.HTMLEscapeString(string(ch)))
			}
		}
		buf.WriteString("</span>\n")
	}
	buf.WriteString("</div>")
	return buf.String()
}
